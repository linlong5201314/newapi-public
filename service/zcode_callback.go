package service

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// ValidateZCodeReturnedURL accepts only the broker callback advertised by the
// original provider-issued authorization URL, with its exact one-use state.
// It never treats an arbitrary pasted URL or JWT as authenticated identity.
func ValidateZCodeReturnedURL(original, returned, provider string) (string, error) {
	if len(original) > 16384 || len(returned) > 16384 || !zcodeProvider(provider) {
		return "", ErrAccountProtocol
	}
	o, err := url.Parse(original)
	if err != nil || !ValidZCodeAuthorizationURL(original) {
		return "", ErrAccountProtocol
	}
	u, err := url.Parse(strings.TrimSpace(returned))
	if err != nil || u.Scheme != "https" || u.Host != "zcode.z.ai" || u.Port() != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.Path != "/api/v1/oauth/cli/callback/"+provider {
		return "", ErrAccountProtocol
	}
	state := o.Query().Get("state")
	q := u.Query()
	if state == "" || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(state), []byte(q.Get("state"))) != 1 || len(q["code"]) != 1 || !HeaderCredential(q.Get("code"), 4096) {
		return "", ErrAccountProtocol
	}
	for key := range q {
		if key != "code" && key != "state" {
			return "", ErrAccountProtocol
		}
	}
	return u.String(), nil
}

func CompleteZCodeBrowserReturn(ctx context.Context, proxy, original, returned, provider string) error {
	target, err := ValidateZCodeReturnedURL(original, returned, provider)
	if err != nil {
		return err
	}
	// Destination is fixed above; transport never follows provider redirects and
	// never sends a dashboard token or local credential with this request.
	status, _, _, err := desktopAccountRequest(ctx, proxy, target, make(http.Header))
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusFound && status != http.StatusSeeOther && status != http.StatusTemporaryRedirect {
		return ErrAccountUpstream
	}
	return nil
}
