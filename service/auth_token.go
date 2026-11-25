package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	accessTokenUse        = "access"
	securityProofTokenUse = "security_proof"
	authTokenIssuer       = "new-api"
	authTokenAudience     = "new-api-dashboard"
)

// durationSecondsFromEnv reads a positive integer number of seconds from the
// environment. It deliberately does not log on a bad value: these variables are
// initialized while the package loads, before the log writer is safe to use.
func durationSecondsFromEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// Token lifetimes are variables, not constants, so a deployment can tune them
// through the environment without a rebuild.
//
// RefreshReplayWindow is the grace window in which a refresh token that was
// just rotated away is still accepted. Refresh tokens rotate on every refresh,
// and a client refreshes from more than one place: several browser tabs, a
// dashboard opened in two windows, or — as in a multi-node deployment without a
// shared Redis — the same browser landing on a different instance each request.
// A window that is too narrow turns that ordinary race into "reuse detected",
// which revokes the whole token family (model.RotateUserSessionRefresh) and
// signs the user out mid-session. One day keeps the family intact for any
// realistic client while still flagging a token replayed long after it rotated.
var (
	AccessTokenTTL      = durationSecondsFromEnv("ACCESS_TOKEN_TTL_SECONDS", 2*time.Hour)
	SecurityProofTTL    = durationSecondsFromEnv("SECURITY_PROOF_TTL_SECONDS", time.Minute)
	LoginSessionTTL     = durationSecondsFromEnv("LOGIN_SESSION_TTL_SECONDS", 30*24*time.Hour)
	RefreshReplayWindow = durationSecondsFromEnv("REFRESH_REPLAY_WINDOW_SECONDS", 10*time.Minute)
)

var (
	ErrAuthTokenInvalid = errors.New("authentication token is invalid")
	ErrAuthTokenExpired = errors.New("authentication token has expired")
	ErrProofScope       = errors.New("security proof scope mismatch")
	ErrProofMethod      = errors.New("security proof method mismatch")
	ErrProofContext     = errors.New("security proof context mismatch")
	ErrProofConsumed    = errors.New("security proof has already been consumed")
)

// AuthIdentity is the server-validated identity attached to dashboard requests.
// Role, status and group are deliberately loaded from the user cache instead of JWT claims.
type AuthIdentity = model.AuthSessionIdentity

type authClaims struct {
	TokenUse        string   `json:"token_use"`
	SessionID       string   `json:"sid"`
	UserAuthVersion int64    `json:"uv"`
	SessionVersion  int64    `json:"sv"`
	Method          string   `json:"method,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	ContextHash     string   `json:"context_hash,omitempty"`
	jwt.RegisteredClaims
}

func authSigningKey(purpose string) []byte {
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write([]byte("new-api/auth/" + purpose + "/v1"))
	return mac.Sum(nil)
}

func IssueAccessToken(identity AuthIdentity) (string, int64, error) {
	if identity.UserID <= 0 || identity.SessionID == "" || identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 {
		return "", 0, ErrAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(AccessTokenTTL)
	claims := authClaims{
		TokenUse:        accessTokenUse,
		SessionID:       identity.SessionID,
		UserAuthVersion: identity.UserAuthVersion,
		SessionVersion:  identity.SessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   strconv.Itoa(identity.UserID),
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(authSigningKey(accessTokenUse))
	return signed, expiresAt.Unix(), err
}

func ParseAccessToken(raw string) (AuthIdentity, error) {
	claims, err := parseAuthClaims(raw, accessTokenUse, authSigningKey(accessTokenUse))
	if err != nil {
		return AuthIdentity{}, err
	}
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 || claims.SessionID == "" || claims.UserAuthVersion <= 0 || claims.SessionVersion <= 0 {
		return AuthIdentity{}, ErrAuthTokenInvalid
	}
	return AuthIdentity{
		UserID:          userID,
		SessionID:       claims.SessionID,
		UserAuthVersion: claims.UserAuthVersion,
		SessionVersion:  claims.SessionVersion,
	}, nil
}

// ParseDashboardAccessToken distinguishes new-api dashboard JWTs from opaque
// credentials. A token carrying the dashboard issuer, audience and a known
// token use is always treated as internal, even when its signature, lifetime
// or requested purpose is invalid, so it can never fall through to PAT or
// relay-token authentication.
func ParseDashboardAccessToken(raw string) (identity AuthIdentity, internal bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AuthIdentity{}, false, nil
	}
	claims := &authClaims{}
	parsed, _, parseErr := jwt.NewParser().ParseUnverified(raw, claims)
	if parseErr != nil || parsed == nil {
		return AuthIdentity{}, false, nil
	}
	audienceMatches := slices.Contains(claims.Audience, authTokenAudience)
	knownTokenUse := claims.TokenUse == accessTokenUse || claims.TokenUse == securityProofTokenUse
	if claims.Issuer != authTokenIssuer || !audienceMatches || !knownTokenUse {
		return AuthIdentity{}, false, nil
	}
	identity, err = ParseAccessToken(raw)
	return identity, true, err
}

func IssueSecurityProof(identity AuthIdentity, method string, binding VerificationBinding) (string, int64, error) {
	method = strings.TrimSpace(method)
	if identity.UserID <= 0 || identity.SessionID == "" || identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 || method == "" || binding.Scope == "" || binding.ContextHash == "" {
		return "", 0, ErrAuthTokenInvalid
	}
	now := time.Now()
	expiresAt := now.Add(SecurityProofTTL).Truncate(time.Second)
	proofID, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose: model.AuthFlowPurposeSecurityProof, UserId: identity.UserID,
		SessionId: identity.SessionID, ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", 0, err
	}
	claims := authClaims{
		TokenUse:        securityProofTokenUse,
		SessionID:       identity.SessionID,
		UserAuthVersion: identity.UserAuthVersion,
		SessionVersion:  identity.SessionVersion,
		Method:          method,
		Scopes:          []string{binding.Scope},
		ContextHash:     binding.ContextHash,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   strconv.Itoa(identity.UserID),
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        proofID,
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(authSigningKey(securityProofTokenUse))
	return signed, expiresAt.Unix(), err
}

func verifySecurityProof(raw string, identity AuthIdentity, binding VerificationBinding) (*authClaims, error) {
	claims, err := parseAuthClaims(raw, securityProofTokenUse, authSigningKey(securityProofTokenUse))
	if err != nil {
		return nil, err
	}
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID != identity.UserID || claims.SessionID != identity.SessionID || claims.UserAuthVersion != identity.UserAuthVersion || claims.SessionVersion != identity.SessionVersion {
		return nil, ErrAuthTokenInvalid
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != binding.Scope {
		return nil, ErrProofScope
	}
	if claims.ContextHash == "" {
		return nil, ErrAuthTokenInvalid
	}
	if !hmac.Equal([]byte(claims.ContextHash), []byte(binding.ContextHash)) {
		return nil, ErrProofContext
	}
	return claims, nil
}

func parseAuthClaims(raw, expectedUse string, key []byte) (*authClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrAuthTokenInvalid
	}
	claims := &authClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrAuthTokenInvalid)
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(authTokenIssuer), jwt.WithAudience(authTokenAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrAuthTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrAuthTokenInvalid, err)
	}
	if !parsed.Valid || claims.TokenUse != expectedUse || claims.ID == "" || claims.IssuedAt == nil || claims.NotBefore == nil {
		return nil, ErrAuthTokenInvalid
	}
	return claims, nil
}
