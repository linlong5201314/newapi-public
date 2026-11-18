package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
)

var ErrAccountUpstream = errors.New("Account provider request failed; retry or restart authorization")
var ErrAccountProtocol = errors.New("Account provider returned an invalid response; restart authorization")
var ErrAccountEgress = errors.New("Account proxy changed or is unavailable; restart authorization")
var ErrAccountPollEarly = errors.New("Wait before polling authorization again")

// accountAuthJSON never follows redirects and never includes upstream bodies or
// URL-bearing transport errors in outward errors (state/token are URL secrets).
func AccountAuthorizationJSON(ctx context.Context, proxy string, request *http.Request, out any) error {
	upstream, err := GetHttpClientWithProxy(proxy)
	if err != nil {
		return ErrAccountEgress
	}
	if upstream == nil {
		return ErrAccountEgress
	}
	client := *upstream
	client.Timeout = 8 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request.WithContext(ctx))
	if err != nil {
		return ErrAccountUpstream
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return ErrAccountProtocol
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrAccountUpstream
	}
	if err := common.Unmarshal(raw, out); err != nil {
		return ErrAccountProtocol
	}
	return nil
}
