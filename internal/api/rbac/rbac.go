package rbac

import (
	"sort"
	"strings"
)

type Role int

const (
	NoRole Role = 0
	// Reporter is reserved for machine principals (automation, monitoring).
	// It sits below Creator on purpose: a reporter may only create system
	// incidents, never human-authored events.
	Reporter Role = 5
	Creator  Role = 10
	Operator Role = 30
	Admin    Role = 50
)

// Config holds the role names granted by the identity provider. Every field
// accepts a comma-separated list of names; an empty field leaves the
// corresponding application role unmapped.
type Config struct {
	Creators  string
	Operators string
	Admins    string
	Reporters string
}

type Service struct {
	admins    map[string]struct{}
	operators map[string]struct{}
	creators  map[string]struct{}
	reporters map[string]struct{}
}

func parseRoles(input string) map[string]struct{} {
	m := make(map[string]struct{})
	for _, part := range strings.Split(input, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "/")
		if part != "" {
			m[part] = struct{}{}
		}
	}
	return m
}

func New(cfg Config) *Service {
	return &Service{
		creators:  parseRoles(cfg.Creators),
		operators: parseRoles(cfg.Operators),
		admins:    parseRoles(cfg.Admins),
		reporters: parseRoles(cfg.Reporters),
	}
}

func (s *Service) roleForName(roleName string) Role {
	if _, ok := s.admins[roleName]; ok {
		return Admin
	}
	if _, ok := s.operators[roleName]; ok {
		return Operator
	}
	if _, ok := s.creators[roleName]; ok {
		return Creator
	}
	if _, ok := s.reporters[roleName]; ok {
		return Reporter
	}
	return NoRole
}

func normalizeRoleName(roleName string) string {
	return strings.TrimPrefix(roleName, "/")
}

// RoleNames returns every distinct role name known to the service, sorted.
func (s *Service) RoleNames() []string {
	known := []map[string]struct{}{s.creators, s.operators, s.admins, s.reporters}
	set := make(map[string]struct{}, len(s.creators)+len(s.operators)+len(s.admins)+len(s.reporters))
	for _, names := range known {
		for name := range names {
			set[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// HasAuthorizedRole reports whether at least one of the role names carried by
// the token maps to an application role.
func (s *Service) HasAuthorizedRole(roleNames []string) bool {
	for _, roleName := range roleNames {
		if s.roleForName(normalizeRoleName(roleName)) != NoRole {
			return true
		}
	}
	return false
}

// ResolveRole returns the highest application role granted by the given role names.
func (s *Service) ResolveRole(roleNames []string) Role {
	currentRole := NoRole
	for _, roleName := range roleNames {
		role := s.roleForName(normalizeRoleName(roleName))
		if role == Admin {
			return Admin
		}
		if role > currentRole {
			currentRole = role
		}
	}
	return currentRole
}

func (r Role) IsAdmin() bool    { return r >= Admin }
func (r Role) CanApprove() bool { return r >= Operator }
func (r Role) CanCreate() bool  { return r >= Creator }

// IsReporter reports whether the role is restricted to machine reporting.
func (r Role) IsReporter() bool { return r == Reporter }

// CanViewInternalFields reports whether the role grants the extended event view
// (caller-scoped fields such as creator, contact_email and version).
func (r Role) CanViewInternalFields() bool { return r >= Creator }
