package errors

import "errors"

var (
	ErrAuthNotAuthenticated = errors.New("not authenticated")
	ErrAuthTokenInvalid     = errors.New("token invalid")
	ErrAuthForbidden        = errors.New("access is denied")
	ErrInsufficientRole     = errors.New("insufficient role")
)
