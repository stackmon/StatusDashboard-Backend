package errors

import "errors"

var (
	// ErrAuthNotAuthenticated is returned when a protected endpoint is called
	// without a usable bearer token.
	ErrAuthNotAuthenticated = errors.New("not authenticated")
	// ErrAuthTokenInvalid is returned when the bearer token cannot be verified.
	ErrAuthTokenInvalid = errors.New("token invalid")
	// ErrAuthForbidden is returned when the authenticated caller holds no
	// role granting access to the endpoint.
	ErrAuthForbidden    = errors.New("access is denied")
	ErrInsufficientRole = errors.New("insufficient role")
)
