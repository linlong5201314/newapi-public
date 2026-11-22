package qoder

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// nativeStream synchronously reads one bounded native SSE envelope at a time.
// It owns no goroutines/channels; cancellation propagates through upstream HTTP
// context, and Close closes the exact upstream response body. Errors never embed
// upstream payloads, authentication tokens, or URLs.
type nativeStream struct {
	source   io.ReadCloser
	scanner  *bufio.Scanner
	pending  []byte
	done     bool
	finished bool
	err      error
}

// qoderQueueNotice reports whether a rejected native frame is the provider's
// free-tier queue notice rather than a failure of the account or credential.
// The notice is nested up to three levels of escaped JSON; the innermost level
// carries isQueued plus subCode RESOURCE_LIMIT.
func qoderQueueNotice(body string) bool {
	raw := body
	for depth := 0; depth < 4 && raw != ""; depth++ {
		var level struct {
			Message  string `json:"message"`
			SubCode  string `json:"subCode"`
			IsQueued bool   `json:"isQueued"`
		}
		if common.Unmarshal([]byte(raw), &level) != nil {
			return false
		}
		if level.IsQueued && level.SubCode == "RESOURCE_LIMIT" {
			return true
		}
		raw = level.Message
	}
	return false
}

func newNativeStream(body io.ReadCloser) *nativeStream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	return &nativeStream{source: body, scanner: scanner}
}
func (s *nativeStream) Close() error { return s.source.Close() }
func (s *nativeStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.pending) == 0 {
		if s.done {
			return 0, io.EOF
		}
		if s.err != nil {
			return 0, s.err
		}
		chunk, err := s.next()
		if err != nil {
			if errors.Is(err, io.EOF) && s.done {
				s.pending = []byte("data: [DONE]\n\n")
				break
			}
			return 0, err
		}
		raw, err := common.Marshal(chunk)
		if err != nil {
			s.err = service.ErrAccountProtocol
			return 0, s.err
		}
		s.pending = append([]byte("data: "), raw...)
		s.pending = append(s.pending, '\n', '\n')
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *nativeStream) next() (*dto.ChatCompletionsStreamResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.done {
		return nil, io.EOF
	}
	for s.scanner.Scan() {
		line := strings.TrimSuffix(s.scanner.Text(), "\r")
		value, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		var outer struct {
			Body   *string           `json:"body"`
			Status common.RawMessage `json:"statusCodeValue"`
		}
		if common.Unmarshal([]byte(value), &outer) != nil {
			s.err = service.ErrAccountProtocol
			return nil, s.err
		}
		if len(outer.Status) > 0 && !bytes.Equal(outer.Status, []byte("null")) {
			status := strings.Trim(string(outer.Status), "\"")
			code, err := strconv.Atoi(status)
			if err != nil {
				s.err = service.ErrAccountProtocol
				return nil, s.err
			}
			if code != http.StatusOK {
				// Operator-facing diagnostic only: the native envelope carries
				// the provider's reason while the caller gets a generic error.
				detail := ""
				if outer.Body != nil {
					detail = strings.Join(strings.Fields(*outer.Body), " ")
					if len(detail) > 320 {
						detail = detail[:320]
					}
				}
				common.SysLog("qoder stream rejected: status=" + strconv.Itoa(code) + " body=" + detail)
				if outer.Body != nil && qoderQueueNotice(*outer.Body) {
					s.err = service.ErrQoderQueued
					return nil, s.err
				}
				s.err = service.ErrAccountUpstream
				return nil, s.err
			}
		}
		if outer.Body == nil || *outer.Body == "" {
			continue
		}
		if *outer.Body == "[DONE]" {
			if !s.finished {
				s.err = service.ErrAccountProtocol
				return nil, s.err
			}
			s.done = true
			return nil, io.EOF
		}
		var diagnostic struct {
			Error common.RawMessage `json:"error"`
		}
		if common.Unmarshal([]byte(*outer.Body), &diagnostic) != nil {
			s.err = service.ErrAccountProtocol
			return nil, s.err
		}
		if len(diagnostic.Error) > 0 && !bytes.Equal(diagnostic.Error, []byte("null")) {
			s.err = service.ErrAccountUpstream
			return nil, s.err
		}
		var chunk dto.ChatCompletionsStreamResponse
		if common.Unmarshal([]byte(*outer.Body), &chunk) != nil || len(chunk.Choices) > 1 {
			s.err = service.ErrAccountProtocol
			return nil, s.err
		}
		if len(chunk.Choices) == 0 && chunk.Usage == nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 || len(choice.Delta.ToolCalls) > 0 {
				s.err = service.ErrAccountProtocol
				return nil, s.err
			}
			if choice.FinishReason != nil {
				switch *choice.FinishReason {
				case "":
				case "stop", "length", "content_filter":
					s.finished = true
				default:
					s.err = service.ErrAccountProtocol
					return nil, s.err
				}
			}
		}
		if chunk.Usage != nil {
			u := chunk.Usage
			for _, n := range []int{u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.PromptTokensDetails.CachedTokens, u.CompletionTokenDetails.ReasoningTokens} {
				if n < 0 || n > math.MaxInt32 {
					s.err = service.ErrAccountProtocol
					return nil, s.err
				}
			}
			if u.PromptTokensDetails.CachedTokens > u.PromptTokens || u.CompletionTokenDetails.ReasoningTokens > u.CompletionTokens {
				s.err = service.ErrAccountProtocol
				return nil, s.err
			}
			// Never accept provider-injected internal BillingUsage or billing semantics.
			chunk.Usage = &dto.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: u.PromptTokensDetails.CachedTokens}, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: u.CompletionTokenDetails.ReasoningTokens}}
		}
		return &chunk, nil
	}
	// A finish_reason alone does not authorize hiding a later upstream error or
	// truncated transport. The native [DONE] envelope is required for success.
	s.err = service.ErrAccountProtocol
	return nil, s.err
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewError(service.ErrAccountProtocol, types.ErrorCodeBadResponseBody)
	}
	reader := newNativeStream(resp.Body)
	defer reader.Close()
	translated := *resp
	translated.Header = http.Header{}
	if info.IsStream {
		// Inspect before writing SSE headers: a queued account is not a successful stream.
		first, err := reader.next()
		if err != nil {
			if errors.Is(err, service.ErrQoderQueued) {
				return nil, types.NewErrorWithStatusCode(err, types.ErrorCode("qoder_queued"), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
			}
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		raw, err := common.Marshal(first)
		if err != nil {
			return nil, types.NewError(service.ErrAccountProtocol, types.ErrorCodeBadResponseBody)
		}
		reader.pending = append(append([]byte("data: "), raw...), 10, 10)
		translated.Body = reader
		translated.Header.Set("Content-Type", "text/event-stream")
		translated.ContentLength = -1
		usage, apiErr := openai.OaiStreamHandler(c, info, &translated)
		if reader.err != nil {
			if errors.Is(reader.err, service.ErrQoderQueued) {
				return usage, types.NewErrorWithStatusCode(reader.err, types.ErrorCode("qoder_queued"), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
			}
			return usage, types.NewError(reader.err, types.ErrorCodeBadResponseBody)
		}
		return usage, apiErr
	}
	result, err := aggregateNative(reader, info.UpstreamModelName)
	if err != nil {
		if errors.Is(err, service.ErrQoderQueued) {
			return nil, types.NewErrorWithStatusCode(err, types.ErrorCode("qoder_queued"), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
		}
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	raw, err := common.Marshal(result)
	if err != nil {
		return nil, types.NewError(service.ErrAccountProtocol, types.ErrorCodeBadResponseBody)
	}
	translated.Body = io.NopCloser(bytes.NewReader(raw))
	translated.Header.Set("Content-Type", "application/json")
	translated.ContentLength = int64(len(raw))
	return openai.OpenaiHandler(c, info, &translated)
}

func aggregateNative(reader *nativeStream, modelKey string) (*dto.TextResponse, error) {
	result := &dto.TextResponse{Object: "chat.completion", Model: modelKey, Choices: []dto.OpenAITextResponseChoice{{Index: 0, Message: dto.Message{Role: "assistant"}}}}
	var content, reasoning strings.Builder
	finished := false
	for {
		chunk, err := reader.next()
		if errors.Is(err, io.EOF) && reader.done {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk.Id != "" {
			result.Id = chunk.Id
		}
		if chunk.Created != 0 {
			result.Created = chunk.Created
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if chunk.Usage != nil {
			result.Usage = *chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil {
				content.WriteString(*choice.Delta.Content)
			}
			if choice.Delta.ReasoningContent != nil {
				reasoning.WriteString(*choice.Delta.ReasoningContent)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				result.Choices[0].FinishReason = *choice.FinishReason
				finished = true
			}
		}
		if content.Len()+reasoning.Len() > 8<<20 {
			return nil, service.ErrAccountProtocol
		}
	}
	if !finished {
		return nil, service.ErrAccountProtocol
	}
	result.Choices[0].Content = content.String()
	if reasoning.Len() > 0 {
		s := reasoning.String()
		result.Choices[0].ReasoningContent = &s
	}
	return result, nil
}
