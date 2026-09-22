package rbac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func testConfig(creators, operators, admins string) Config {
	return Config{Creators: creators, Operators: operators, Admins: admins}
}

func TestService_Resolve(t *testing.T) {
	svc := New(testConfig("sd_creators", "sd_operators", "sd_admins"))

	tests := []struct {
		name     string
		roles    []string
		expected Role
	}{
		{
			name:     "Empty roles list returns NoRole",
			roles:    []string{},
			expected: NoRole,
		},
		{
			name:     "Unrecognized role returns NoRole",
			roles:    []string{"some_random_role"},
			expected: NoRole,
		},
		{
			name:     "Creator role returns Creator",
			roles:    []string{"sd_creators"},
			expected: Creator,
		},
		{
			name:     "Operator role returns Operator",
			roles:    []string{"sd_operators"},
			expected: Operator,
		},
		{
			name:     "Admin role returns Admin",
			roles:    []string{"sd_admins"},
			expected: Admin,
		},
		{
			name:     "Reporter role is not recognized when unconfigured",
			roles:    []string{"sd_reporters"},
			expected: NoRole,
		},
		{
			name:     "Multiple roles: Operator supersedes Creator",
			roles:    []string{"sd_creators", "sd_operators"},
			expected: Operator,
		},
		{
			name:     "Multiple roles: Admin supersedes Operator",
			roles:    []string{"sd_operators", "sd_admins"},
			expected: Admin,
		},
		{
			name:     "Multiple roles: Admin supersedes all",
			roles:    []string{"sd_creators", "sd_operators", "sd_admins"},
			expected: Admin,
		},
		{
			name:     "Role normalization: handles leading slash for Creator",
			roles:    []string{"/sd_creators"},
			expected: Creator,
		},
		{
			name:     "Role normalization: handles leading slash for Admin",
			roles:    []string{"/sd_admins"},
			expected: Admin,
		},
		{
			name:     "Mixed normalized and raw roles",
			roles:    []string{"/sd_creators", "sd_operators"},
			expected: Operator,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.ResolveRole(tt.roles)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestService_Resolve_WithReporters(t *testing.T) {
	svc := New(Config{
		Creators:  "sd_creators",
		Operators: "sd_operators",
		Admins:    "sd_admins",
		Reporters: "sd_reporters",
	})

	t.Run("reporter role resolves to Reporter", func(t *testing.T) {
		assert.Equal(t, Reporter, svc.ResolveRole([]string{"sd_reporters"}))
	})

	t.Run("slash-prefixed reporter role resolves to Reporter", func(t *testing.T) {
		assert.Equal(t, Reporter, svc.ResolveRole([]string{"/sd_reporters"}))
	})

	t.Run("creator supersedes reporter", func(t *testing.T) {
		assert.Equal(t, Creator, svc.ResolveRole([]string{"sd_reporters", "sd_creators"}))
	})

	t.Run("admin supersedes reporter", func(t *testing.T) {
		assert.Equal(t, Admin, svc.ResolveRole([]string{"sd_reporters", "sd_admins"}))
	})

	t.Run("reporter is an authorized role", func(t *testing.T) {
		assert.True(t, svc.HasAuthorizedRole([]string{"sd_reporters"}))
	})

	t.Run("reporter holds no write permissions", func(t *testing.T) {
		assert.False(t, Reporter.CanCreate())
		assert.False(t, Reporter.CanApprove())
		assert.False(t, Reporter.IsAdmin())
	})

	t.Run("reporter stays on the public view", func(t *testing.T) {
		assert.True(t, Reporter.IsReporter())
		assert.False(t, Reporter.CanViewInternalFields())
		assert.True(t, Creator.CanViewInternalFields())
		assert.False(t, NoRole.CanViewInternalFields())
	})
}

func TestRole_Permissions(t *testing.T) {
	tests := []struct {
		name       string
		role       Role
		canCreate  bool
		canApprove bool
		isAdmin    bool
	}{
		{
			name:       "NoRole has no permissions",
			role:       NoRole,
			canCreate:  false,
			canApprove: false,
			isAdmin:    false,
		},
		{
			name:       "Creator can create but not approve",
			role:       Creator,
			canCreate:  true,
			canApprove: false,
			isAdmin:    false,
		},
		{
			name:       "Operator can create and approve",
			role:       Operator,
			canCreate:  true,
			canApprove: true,
			isAdmin:    false,
		},
		{
			name:       "Admin has all permissions",
			role:       Admin,
			canCreate:  true,
			canApprove: true,
			isAdmin:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.canCreate, tt.role.CanCreate(), "CanCreate()")
			assert.Equal(t, tt.canApprove, tt.role.CanApprove(), "CanApprove()")
			assert.Equal(t, tt.isAdmin, tt.role.IsAdmin(), "IsAdmin()")
		})
	}
}

func TestService_HasAuthorizedRole(t *testing.T) {
	svc := New(testConfig("sd_creators", "sd_operators", "sd_admins"))

	tests := []struct {
		name     string
		roles    []string
		expected bool
	}{
		{
			name:     "Empty roles list returns false",
			roles:    []string{},
			expected: false,
		},
		{
			name:     "Unrecognized role returns false",
			roles:    []string{"some_random_role"},
			expected: false,
		},
		{
			name:     "Creator role returns true",
			roles:    []string{"sd_creators"},
			expected: true,
		},
		{
			name:     "Operator role returns true",
			roles:    []string{"sd_operators"},
			expected: true,
		},
		{
			name:     "Admin role returns true",
			roles:    []string{"sd_admins"},
			expected: true,
		},
		{
			name:     "Role normalization: handles leading slash",
			roles:    []string{"/sd_creators"},
			expected: true,
		},
		{
			name:     "Mixed recognized and unrecognized roles",
			roles:    []string{"random", "other", "sd_operators"},
			expected: true,
		},
		{
			name:     "Only unrecognized roles",
			roles:    []string{"random", "other", "unknown"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.HasAuthorizedRole(tt.roles)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestService_HasAuthorizedRole_EmptyConfig(t *testing.T) {
	svc := New(testConfig("", "", ""))

	tests := []struct {
		name     string
		roles    []string
		expected bool
	}{
		{
			name:     "No roles configured, empty list returns false",
			roles:    []string{},
			expected: false,
		},
		{
			name:     "No roles configured, any role returns false",
			roles:    []string{"sd_creators", "sd_admins"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.HasAuthorizedRole(tt.roles)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseRoles(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]struct{}
	}{
		{
			name:     "empty string returns empty map",
			input:    "",
			expected: map[string]struct{}{},
		},
		{
			name:     "single role",
			input:    "sd_admins",
			expected: map[string]struct{}{"sd_admins": {}},
		},
		{
			name:     "comma-separated roles",
			input:    "sd_admins,sd_readers",
			expected: map[string]struct{}{"sd_admins": {}, "sd_readers": {}},
		},
		{
			name:     "spaces around commas are trimmed",
			input:    "sd_admins , sd_readers",
			expected: map[string]struct{}{"sd_admins": {}, "sd_readers": {}},
		},
		{
			name:     "leading slash in configured role is normalized",
			input:    "/sd_admins,/sd_readers",
			expected: map[string]struct{}{"sd_admins": {}, "sd_readers": {}},
		},
		{
			name:     "empty entries from double commas are ignored",
			input:    "sd_admins,,sd_readers",
			expected: map[string]struct{}{"sd_admins": {}, "sd_readers": {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRoles(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestService_RoleNames(t *testing.T) {
	tests := []struct {
		name     string
		svc      *Service
		expected []string
	}{
		{
			name:     "returns the sorted union of configured role names",
			svc:      New(testConfig("sd_creators", "sd_operators", "sd_admins")),
			expected: []string{"sd_admins", "sd_creators", "sd_operators"},
		},
		{
			name:     "splits comma-separated configurations",
			svc:      New(testConfig("", "", "sd-admins,sd_readers")),
			expected: []string{"sd-admins", "sd_readers"},
		},
		{
			name:     "normalizes leading slashes",
			svc:      New(testConfig("/sd_creators", "", "")),
			expected: []string{"sd_creators"},
		},
		{
			name:     "deduplicates names shared by several roles",
			svc:      New(testConfig("shared", "shared", "shared")),
			expected: []string{"shared"},
		},
		{
			name:     "includes reporter role names",
			svc:      New(Config{Reporters: "sd_reporters"}),
			expected: []string{"sd_reporters"},
		},
		{
			name:     "no configured roles yields an empty list",
			svc:      New(testConfig("", "", "")),
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.svc.RoleNames())
		})
	}
}

// TestService_CommaSeparatedConfig reproduces the deployment scenario
// SD_RBAC_ROLES_ADMINS="sd-admins,sd_readers" where a single variable may hold
// several project role keys.
func TestService_CommaSeparatedConfig(t *testing.T) {
	svc := New(testConfig("sd_creators", "sd_operators", "sd-admins,sd_readers"))

	// Real token roles from preprod Zitadel (truncated for brevity)
	zitadelRoles := []string{
		"sd_readers",
		"/sd-admins",
		"gitea-users",
	}

	t.Run("user with sd-admins is authorized", func(t *testing.T) {
		assert.True(t, svc.HasAuthorizedRole(zitadelRoles))
	})

	t.Run("user with sd-admins resolves to Admin", func(t *testing.T) {
		assert.Equal(t, Admin, svc.ResolveRole(zitadelRoles))
	})

	t.Run("user with /sd-admins also resolves to Admin", func(t *testing.T) {
		assert.Equal(t, Admin, svc.ResolveRole([]string{"/sd-admins", "other-role"}))
	})

	t.Run("user without any matching role is denied", func(t *testing.T) {
		assert.False(t, svc.HasAuthorizedRole([]string{"gitea-admin", "some_other_role"}))
	})
}

func TestService_Resolve_EmptyConfig(t *testing.T) {
	svc := New(testConfig("", "", ""))

	tests := []struct {
		name     string
		roles    []string
		expected Role
	}{
		{
			name:     "No roles configured, empty list returns NoRole",
			roles:    []string{},
			expected: NoRole,
		},
		{
			name:     "No roles configured, known names still return NoRole",
			roles:    []string{"sd_creators", "sd_operators", "sd_admins"},
			expected: NoRole,
		},
		{
			name:     "No roles configured, slash-prefixed returns NoRole",
			roles:    []string{"/"},
			expected: NoRole,
		},
		{
			name:     "No roles configured, empty string role returns NoRole",
			roles:    []string{""},
			expected: NoRole,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.ResolveRole(tt.roles)
			assert.Equal(t, tt.expected, got)
		})
	}
}
