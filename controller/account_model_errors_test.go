package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relay/channel/trae"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
)

func TestAccountModelFetchErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"trae expired", &trae.Error{Kind: trae.ErrSessionDead, Status: http.StatusUnauthorized}, service.ErrDesktopLoginExpired},
		{"trae rate limit", &trae.Error{Kind: trae.ErrSoftRate, Status: http.StatusTooManyRequests}, service.ErrAccountUpstream},
		{"plain upstream", errors.New("upstream 401 with private payload"), service.ErrAccountUpstream},
		{"nous expiry", service.ErrDesktopLoginExpired, service.ErrDesktopLoginExpired},
		{"qoder expiry", service.ErrDesktopLoginExpired, service.ErrDesktopLoginExpired},
		{"cline expiry", service.ErrDesktopLoginExpired, service.ErrDesktopLoginExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == nil {
				assert.ErrorIs(t, normalizeAccountModelFetchError(tc.in), service.ErrAccountUpstream)
				return
			}
			assert.ErrorIs(t, normalizeAccountModelFetchError(tc.in), tc.want)
		})
	}
}
