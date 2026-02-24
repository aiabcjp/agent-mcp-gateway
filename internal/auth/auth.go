// Package auth provides authentication (OIDC) and authorization (RBAC)
// for the MCP gateway.
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Claims represents the verified identity extracted from a JWT token.
type Claims struct {
	Subject string
	Email   string
	Groups  []string
	Expiry  time.Time
}

// Authenticator verifies bearer tokens and returns the embedded claims.
type Authenticator interface {
	VerifyToken(ctx context.Context, tokenString string) (*Claims, error)
}

// contextKey is an unexported type used for storing Claims in context.Context,
// preventing collisions with keys from other packages.
type contextKey struct{}

// ClaimsFromContext extracts the Claims previously stored in ctx. The second
// return value reports whether claims were present.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(contextKey{}).(*Claims)
	return c, ok
}

// ContextWithClaims returns a new context that carries the given Claims.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

// OIDCAuthenticator verifies JWT tokens using an OpenID Connect provider's
// discovery endpoint and public keys.
type OIDCAuthenticator struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

// NewOIDCAuthenticator creates a new authenticator that validates tokens
// against the OIDC provider at issuer. clientID is used as the expected
// audience.
func NewOIDCAuthenticator(issuer, clientID string) (*OIDCAuthenticator, error) {
	provider, err := oidc.NewProvider(context.Background(), issuer)
	if err != nil {
		return nil, fmt.Errorf("creating OIDC provider for %s: %w", issuer, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: clientID,
	})

	return &OIDCAuthenticator{
		provider: provider,
		verifier: verifier,
	}, nil
}

// oidcClaims is the set of JWT claims we extract from the ID token.
type oidcClaims struct {
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
}

// VerifyToken verifies the raw JWT string against the OIDC provider,
// extracts standard and custom claims, and returns them.
func (a *OIDCAuthenticator) VerifyToken(ctx context.Context, tokenString string) (*Claims, error) {
	idToken, err := a.verifier.Verify(ctx, tokenString)
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	var custom oidcClaims
	if err := idToken.Claims(&custom); err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	return &Claims{
		Subject: idToken.Subject,
		Email:   custom.Email,
		Groups:  custom.Groups,
		Expiry:  idToken.Expiry,
	}, nil
}
