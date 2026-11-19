package service

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZCodeReturnedURLBinding(t *testing.T) {
	original := "https://bigmodel.cn/login?appId=zcode&state=ownstate&redirect=https%3A%2F%2Fzcode.z.ai%2Fapi%2Fv1%2Foauth%2Fcli%2Fcallback%2Fbigmodel"
	target, err := ValidateZCodeReturnedURL(original, "https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=one-use-code&state=ownstate", "bigmodel")
	require.NoError(t, err)
	require.Contains(t, target, "code=one-use-code")
	for _, returned := range []string{
		"https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=other",
		"https://zcode.z.ai/api/v1/oauth/cli/callback/zai?code=x&state=ownstate",
		"https://zcode.z.ai.attacker.invalid/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate",
		"http://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate",
		"https://zcode.z.ai:443/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate",
		"https://user:secret@zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate",
		"https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate#token",
		"https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate&state=ownstate",
		"https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel?code=x&state=ownstate&redirect=https://evil.invalid",
	} {
		_, err := ValidateZCodeReturnedURL(original, returned, "bigmodel")
		require.Error(t, err, returned)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestDS2TransportFailuresAreStaticAndRedacted(t *testing.T) {
	cases := []struct {
		err  error
		kind string
	}{
		{&url.Error{Op: "Post", URL: "https://user:private-password@proxy.invalid", Err: &net.DNSError{Err: "private-password", Name: "private-host", IsNotFound: true}}, "dns"},
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {x509.UnknownAuthorityError{}, "tls"}, {&net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("private-password")}, "proxy"}, {errors.New("private-password"), "connection"},
	}
	for _, tc := range cases {
		failure := ds2TransportFailure(tc.err)
		require.Equal(t, tc.kind, failure.Kind)
		require.NotContains(t, failure.Error(), "private-password")
		require.NotContains(t, failure.Error(), "private-host")
	}
}
