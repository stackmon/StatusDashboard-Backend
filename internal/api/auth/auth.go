package auth

// ProviderZitadel is the idp_type reported in the audit log.
const ProviderZitadel = "zitadel"

// Claims is the verified identity of the caller.
type Claims struct {
	// Subject is the stable identifier of the caller.
	Subject string
	// Username is a human readable name for audit logging, empty when the
	// provider does not issue one.
	Username string
	// Email is the verified address from the token, empty when the provider
	// does not issue one. Used to default the maintenance contact_email.
	Email string
	// Roles are the project role names used for RBAC resolution.
	Roles []string
	// Provider is the identity provider that verified the token.
	Provider string
}
