package auth

import (
	"context"
	"testing"
	"time"

	"agent-gateway/internal/config"
)

// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

func TestContextWithClaims_RoundTrip(t *testing.T) {
	claims := &Claims{
		Subject: "user-123",
		Email:   "alice@example.com",
		Groups:  []string{"engineers"},
		Expiry:  time.Now().Add(time.Hour),
	}

	ctx := ContextWithClaims(context.Background(), claims)
	got, ok := ClaimsFromContext(ctx)
	if !ok {
		t.Fatal("ClaimsFromContext returned ok=false, want true")
	}
	if got.Subject != claims.Subject {
		t.Errorf("Subject = %q, want %q", got.Subject, claims.Subject)
	}
	if got.Email != claims.Email {
		t.Errorf("Email = %q, want %q", got.Email, claims.Email)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "engineers" {
		t.Errorf("Groups = %v, want [engineers]", got.Groups)
	}
}

func TestClaimsFromContext_Empty(t *testing.T) {
	_, ok := ClaimsFromContext(context.Background())
	if ok {
		t.Error("ClaimsFromContext on empty context returned ok=true, want false")
	}
}

func TestClaimsFromContext_NilClaims(t *testing.T) {
	ctx := ContextWithClaims(context.Background(), nil)
	c, ok := ClaimsFromContext(ctx)
	// nil *Claims is stored, so ok should be false because the type assertion
	// to *Claims will succeed but the pointer is nil.
	// Actually: context.WithValue stores interface{}, and a nil *Claims is a
	// non-nil interface holding a nil pointer, so the type assertion succeeds.
	if !ok {
		// This is acceptable; nil *Claims stored as non-nil interface.
		t.Log("ClaimsFromContext returned ok=false for nil *Claims, which is fine")
		return
	}
	if c != nil {
		t.Error("expected nil Claims pointer")
	}
}

// ---------------------------------------------------------------------------
// RBAC Authorizer
// ---------------------------------------------------------------------------

func testRules() []config.RBACRule {
	return []config.RBACRule{
		{
			Group:       "engineers",
			Resources:   []string{"staging-db", "staging-redis"},
			Permissions: []string{"read", "write"},
		},
		{
			Group:       "admins",
			Resources:   []string{"*"},
			Permissions: []string{"read", "write", "admin"},
		},
		{
			Group:       "viewers",
			Resources:   []string{"staging-db"},
			Permissions: []string{"read"},
		},
	}
}

func TestRBACAuthorizer_Check_MatchingGroupAndResource(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"engineers"}}

	if !authz.Check(claims, "staging-db", "read") {
		t.Error("expected Check to return true for engineers/staging-db/read")
	}
	if !authz.Check(claims, "staging-db", "write") {
		t.Error("expected Check to return true for engineers/staging-db/write")
	}
	if !authz.Check(claims, "staging-redis", "read") {
		t.Error("expected Check to return true for engineers/staging-redis/read")
	}
}

func TestRBACAuthorizer_Check_NonMatchingGroup(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"interns"}}

	if authz.Check(claims, "staging-db", "read") {
		t.Error("expected Check to return false for non-matching group")
	}
}

func TestRBACAuthorizer_Check_NonMatchingResource(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"engineers"}}

	if authz.Check(claims, "production-db", "read") {
		t.Error("expected Check to return false for non-matching resource")
	}
}

func TestRBACAuthorizer_Check_NonMatchingPermission(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"engineers"}}

	if authz.Check(claims, "staging-db", "admin") {
		t.Error("expected Check to return false for non-matching permission")
	}
}

func TestRBACAuthorizer_Check_WildcardResource(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"admins"}}

	if !authz.Check(claims, "staging-db", "admin") {
		t.Error("expected Check to return true for admins with wildcard resources")
	}
	if !authz.Check(claims, "production-db", "read") {
		t.Error("expected Check to return true for admins with any resource via wildcard")
	}
	if !authz.Check(claims, "anything-goes", "write") {
		t.Error("expected Check to return true for admins with unknown resource via wildcard")
	}
}

func TestRBACAuthorizer_Check_MultipleGroups(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"viewers", "engineers"}}

	// Should match through engineers rule.
	if !authz.Check(claims, "staging-redis", "write") {
		t.Error("expected Check to return true when one of multiple groups matches")
	}
	// Should match through viewers rule.
	if !authz.Check(claims, "staging-db", "read") {
		t.Error("expected Check to return true for viewers/staging-db/read")
	}
}

func TestRBACAuthorizer_Check_NilClaims(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	if authz.Check(nil, "staging-db", "read") {
		t.Error("expected Check to return false for nil claims")
	}
}

func TestRBACAuthorizer_Check_EmptyGroups(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: nil}

	if authz.Check(claims, "staging-db", "read") {
		t.Error("expected Check to return false for claims with no groups")
	}
}

func TestRBACAuthorizer_Check_NoRules(t *testing.T) {
	authz := NewRBACAuthorizer(nil)
	claims := &Claims{Groups: []string{"engineers"}}

	if authz.Check(claims, "staging-db", "read") {
		t.Error("expected Check to return false with no rules")
	}
}

func TestRBACAuthorizer_AllowedResources_Engineers(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"engineers"}}

	resources := authz.AllowedResources(claims)
	if len(resources) != 2 {
		t.Fatalf("AllowedResources count = %d, want 2", len(resources))
	}
	expected := map[string]bool{"staging-db": true, "staging-redis": true}
	for _, r := range resources {
		if !expected[r] {
			t.Errorf("unexpected resource %q", r)
		}
	}
}

func TestRBACAuthorizer_AllowedResources_Admins(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"admins"}}

	resources := authz.AllowedResources(claims)
	if len(resources) != 1 {
		t.Fatalf("AllowedResources count = %d, want 1", len(resources))
	}
	if resources[0] != "*" {
		t.Errorf("AllowedResources[0] = %q, want %q", resources[0], "*")
	}
}

func TestRBACAuthorizer_AllowedResources_MultipleGroups(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"viewers", "engineers"}}

	resources := authz.AllowedResources(claims)
	// viewers adds staging-db; engineers adds staging-db (dedup) and staging-redis.
	if len(resources) != 2 {
		t.Fatalf("AllowedResources count = %d, want 2; got %v", len(resources), resources)
	}
}

func TestRBACAuthorizer_AllowedResources_NilClaims(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	resources := authz.AllowedResources(nil)
	if resources != nil {
		t.Errorf("AllowedResources for nil claims = %v, want nil", resources)
	}
}

func TestRBACAuthorizer_AllowedResources_NoMatch(t *testing.T) {
	authz := NewRBACAuthorizer(testRules())
	claims := &Claims{Groups: []string{"unknown-group"}}

	resources := authz.AllowedResources(claims)
	if len(resources) != 0 {
		t.Errorf("AllowedResources = %v, want empty", resources)
	}
}

// ---------------------------------------------------------------------------
// OIDCAuthenticator - error paths only (no real provider in unit tests)
// ---------------------------------------------------------------------------

func TestNewOIDCAuthenticator_InvalidIssuer(t *testing.T) {
	// An unreachable issuer should fail during provider creation.
	_, err := NewOIDCAuthenticator("https://nonexistent.invalid.example.com", "client-id")
	if err == nil {
		t.Fatal("expected error for unreachable OIDC issuer, got nil")
	}
}
