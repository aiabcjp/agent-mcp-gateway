package auth

import (
	"agent-gateway/internal/config"
)

// Authorizer decides whether a set of claims grants access to a resource
// with a given permission level.
type Authorizer interface {
	// Check returns true if the claims authorize the requested permission
	// on the given resource.
	Check(claims *Claims, resource string, permission string) bool

	// AllowedResources returns the list of resource names accessible to
	// the holder of the given claims.
	AllowedResources(claims *Claims) []string
}

// RBACAuthorizer implements Authorizer using a static list of RBAC rules
// loaded from configuration.
type RBACAuthorizer struct {
	rules []config.RBACRule
}

// NewRBACAuthorizer creates an Authorizer backed by the provided RBAC rules.
func NewRBACAuthorizer(rules []config.RBACRule) *RBACAuthorizer {
	return &RBACAuthorizer{rules: rules}
}

// Check returns true if any rule matches one of the claims' groups, includes
// the requested resource (or the wildcard "*"), and includes the requested
// permission.
func (a *RBACAuthorizer) Check(claims *Claims, resource string, permission string) bool {
	if claims == nil {
		return false
	}

	for _, rule := range a.rules {
		if !groupMatches(claims.Groups, rule.Group) {
			continue
		}
		if !resourceMatches(rule.Resources, resource) {
			continue
		}
		if !permissionMatches(rule.Permissions, permission) {
			continue
		}
		return true
	}
	return false
}

// AllowedResources collects all resource names from rules whose group
// matches one of the claims' groups. Duplicate resources are deduplicated.
func (a *RBACAuthorizer) AllowedResources(claims *Claims) []string {
	if claims == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var result []string

	for _, rule := range a.rules {
		if !groupMatches(claims.Groups, rule.Group) {
			continue
		}
		for _, res := range rule.Resources {
			if _, exists := seen[res]; !exists {
				seen[res] = struct{}{}
				result = append(result, res)
			}
		}
	}
	return result
}

// groupMatches reports whether the target group is present in the groups slice.
func groupMatches(groups []string, target string) bool {
	for _, g := range groups {
		if g == target {
			return true
		}
	}
	return false
}

// resourceMatches reports whether the resource list contains the specific
// resource or the wildcard "*".
func resourceMatches(resources []string, target string) bool {
	for _, r := range resources {
		if r == "*" || r == target {
			return true
		}
	}
	return false
}

// permissionMatches reports whether the permission list contains the specific
// permission.
func permissionMatches(permissions []string, target string) bool {
	for _, p := range permissions {
		if p == target {
			return true
		}
	}
	return false
}
