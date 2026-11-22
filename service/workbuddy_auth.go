package service

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
)

var ErrAccountUpstream = errors.New("Account provider request failed; retry or restart authorization")
var ErrAccountProtocol = errors.New("Account provider returned an invalid response; restart authorization")
var ErrAccountEgress = errors.New("Account proxy changed or is unavailable; restart authorization")
var ErrAccountPollEarly = errors.New("Wait before polling authorization again")

// ErrAccountServerAddress marks a gateway misconfiguration, never a proxy or
// provider problem: the configured public ServerAddress is missing or unusable
// for the account authorization origin contract.
var ErrAccountServerAddress = errors.New("Server address is missing or invalid for account authorization")

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
	// Same one-time-code constraint as accountAuthJSON: a domestic provider
	// reached through a proxy can need more than 8s to answer, and a premature
	// timeout spends the code without ever creating the channel.
	client.Timeout = accountAuthRequestTimeout
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
