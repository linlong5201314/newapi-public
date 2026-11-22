package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func DoXiaoyiWorkRelay(ctx context.Context, channelID int, encryptedSession string, request *http.Request, modelName string, stream bool) (*http.Response, *types.NewAPIError) {
	return doXiaoyiWorkRelay(ctx, channelID, encryptedSession, request, modelName, stream)
}

// The channel lock prevents two concurrent requests from issuing and saving
// competing credentials. Shared channels use the same stored credential, so a
// save compares the original ciphertext and never overwrites another refresh.
func doXiaoyiWorkRelay(ctx context.Context, channelID int, encryptedSession string, request *http.Request, modelName string, stream bool) (*http.Response, *types.NewAPIError) {
	if channelID > 0 {
		lock := model.GetChannelPollingLock(channelID)
		lock.Lock()
		defer lock.Unlock()
	}
	var session XiaoyiWorkOAuthSession
	var plaintextSession string
	if channelID > 0 {
		stored, err := model.GetChannelById(channelID, true)
		if err != nil || stored.Type != constant.ChannelTypeXiaoyiWork || !common.IsEncryptedSecret(stored.Key) {
			return nil, types.NewError(errors.New("Xiaoyi Work stored credential is invalid"), types.ErrorCodeDoRequestFailed)
		}
		decrypted, err := common.DecryptSecret(stored.Key)
		if err != nil {
			return nil, types.NewError(errors.New("Xiaoyi Work credential could not be decrypted"), types.ErrorCodeDoRequestFailed)
		}
		plaintextSession = decrypted
	}
	if common.IsEncryptedSecret(encryptedSession) {
		decrypted, err := common.DecryptSecret(encryptedSession)
		if err != nil {
			return nil, types.NewError(errors.New("Xiaoyi Work credential could not be decrypted"), types.ErrorCodeDoRequestFailed)
		}
		plaintextSession = decrypted
	}
	if common.UnmarshalJsonStr(plaintextSession, &session) != nil || session.Credential == "" || session.UID == "" || session.DeviceID == "" {
		return nil, types.NewError(errors.New("Xiaoyi Work authorization is invalid. Reconnect the account"), types.ErrorCodeDoRequestFailed)
	}
	if request == nil {
		return nil, types.NewError(errors.New("Xiaoyi Work authorization is missing"), types.ErrorCodeDoRequestFailed)
	}
	previousCredential := session.Credential
	if err := session.Fresh(ctx, ""); err != nil {
		return nil, types.NewError(fmt.Errorf("Xiaoyi Work credential refresh failed. Reconnect the account: %w", err), types.ErrorCodeDoRequestFailed)
	}
	persistCredential := func() error {
		if channelID <= 0 {
			return nil
		}
		raw, err := common.Marshal(&session)
		if err != nil {
			return err
		}
		stored, err := common.EncryptSecret(string(raw))
		if err != nil || !common.IsEncryptedSecret(stored) {
			return errors.New("Xiaoyi Work credential could not be securely stored")
		}
		result := model.DB.Model(&model.Channel{}).Where("id = ? AND type = ?", channelID, constant.ChannelTypeXiaoyiWork).Updates(map[string]any{"key": stored})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("Xiaoyi Work credential changed concurrently")
		}
		model.InitChannelCache()
		return nil
	}
	if previousCredential != session.Credential {
		if err := persistCredential(); err != nil {
			return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
		}
	}
	var body []byte
	var err error
	if storage, ok := request.Body.(common.BodyStorage); ok {
		body, err = storage.Bytes()
		if len(body) > 8<<20 {
			return nil, types.NewError(errors.New("Xiaoyi Work request is too large"), types.ErrorCodeDoRequestFailed)
		}
	} else {
		body, err = io.ReadAll(io.LimitReader(request.Body, 8<<20+1))
		if err == nil && len(body) > 8<<20 {
			return nil, types.NewError(errors.New("Xiaoyi Work request is too large"), types.ErrorCodeDoRequestFailed)
		}
	}
	if err != nil {
		return nil, types.NewError(errors.New("Unable to read the Xiaoyi Work request"), types.ErrorCodeDoRequestFailed)
	}
	client, err := xiaoyiWorkHTTPClient("")
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	client.Timeout = 0
	if transport, ok := client.Transport.(*http.Transport); ok {
		transport = transport.Clone()
		// Authorization endpoints have a short request timeout, but an agent's
		// first reasoning chunk can legitimately take longer. Keep cancellation
		// through the request context without imposing that timeout on inference.
		transport.ResponseHeaderTimeout = 0
		client.Transport = transport
	}
	for attempt := 0; attempt < 2; attempt++ {
		upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, XiaoyiWorkSSEEndpoint, bytes.NewReader(body))
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
		}
		upstream.Header = session.Headers(newTraceID())
		upstream.Header.Set("Accept", "text/event-stream")
		response, err := client.Do(upstream)
		if err != nil {
			if ctx.Err() != nil {
				return nil, types.NewError(ctx.Err(), types.ErrorCodeDoRequestFailed)
			}
			return nil, types.NewError(fmt.Errorf("Xiaoyi Work upstream is unavailable: %w", err), types.ErrorCodeDoRequestFailed)
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			response.Body.Close()
			if attempt > 0 || session.Refresh(ctx, client) != nil {
				return nil, types.NewError(errors.New("Xiaoyi Work authorization expired. Reconnect the account"), types.ErrorCodeDoRequestFailed)
			}
			if err := persistCredential(); err != nil {
				return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
			}
			continue
		}
		if response.StatusCode != http.StatusOK {
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			return response, nil
		}
		if stream {
			response.Body = &xiaoyiWorkStreamBody{reader: response.Body, model: modelName, ctx: ctx}
			response.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
			response.Header.Set("Cache-Control", "no-cache, no-transform")
			return response, nil
		}
		defer response.Body.Close()
		aggregated, err := XiaoyiWorkAggregate(response.Body, modelName)
		if err != nil {
			return nil, types.NewError(fmt.Errorf("Xiaoyi Work returned an invalid response: %w", err), types.ErrorCodeBadResponse)
		}
		raw, err := common.Marshal(aggregated)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponse)
		}
		response.Body = io.NopCloser(bytes.NewReader(raw))
		response.Header.Set("Content-Type", "application/json; charset=utf-8")
		response.ContentLength = int64(len(raw))
		return response, nil
	}
	return nil, types.NewError(errors.New("Xiaoyi Work request failed"), types.ErrorCodeDoRequestFailed)
}

type xiaoyiWorkStreamBody struct {
	ctx    context.Context
	reader io.ReadCloser
	model  string
	input  string
	output string
	done   bool
}

func (body *xiaoyiWorkStreamBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for body.output == "" {
		if body.done {
			return 0, io.EOF
		}
		chunk := make([]byte, 32<<10)
		n, err := body.reader.Read(chunk)
		if body.ctx != nil && body.ctx.Err() != nil {
			return 0, body.ctx.Err()
		}
		body.input += string(chunk[:n])
		for {
			index := strings.Index(body.input, "\n\n")
			if index < 0 {
				break
			}
			block := body.input[:index]
			body.input = body.input[index+2:]
			if data := sseData(block); data != "" && data != "[DONE]" {
				frame, frameErr := xiaoyiWorkNormalizedFrame([]byte(data), body.model)
				if frameErr != nil {
					return 0, errors.New("Xiaoyi Work returned an invalid stream frame")
				}
				body.output += "data: " + string(frame) + "\n\n"
			}
		}
		if err == io.EOF {
			if data := sseData(body.input); data != "" && data != "[DONE]" {
				frame, frameErr := xiaoyiWorkNormalizedFrame([]byte(data), body.model)
				if frameErr != nil {
					return 0, errors.New("Xiaoyi Work returned an incomplete stream frame")
				}
				body.output += "data: " + string(frame) + "\n\n"
			}
			body.input = ""
			body.output += "data: [DONE]\n\n"
			body.done = true
		} else if err != nil {
			return 0, err
		}
		if n == 0 && err == nil {
			return 0, errors.New("Xiaoyi Work stream returned no data")
		}
	}
	n := copy(p, body.output)
	body.output = body.output[n:]
	return n, nil
}

func xiaoyiWorkNormalizedFrame(data []byte, model string) ([]byte, error) {
	var frame xiaoyiWorkFrame
	if err := common.Unmarshal(data, &frame); err != nil {
		return nil, err
	}
	if frame.Object == "" {
		frame.Object = "chat.completion.chunk"
	}
	if frame.Created == 0 {
		frame.Created = time.Now().Unix()
	}
	if frame.Model == "" {
		frame.Model = model
	}
	if frame.ID == "" {
		frame.ID = "chatcmpl-" + newTraceID()
	}
	for index := range frame.Choices {
		frame.Choices[index].Index = index
	}
	return common.Marshal(frame)
}

func (body *xiaoyiWorkStreamBody) Close() error { return body.reader.Close() }
