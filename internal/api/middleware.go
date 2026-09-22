package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

const (
	eventContextKey = "event"
)

func ValidateComponentsMW(dbInst *db.DB, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Info("start to validate given components")
		type Components struct {
			Components []int `json:"components" binding:"required,min=1"`
		}

		var components Components

		if err := c.ShouldBindBodyWithJSON(&components); err != nil {
			apiErrors.RaiseBadRequestErr(c, fmt.Errorf("%w: %w", apiErrors.ErrComponentInvalidFormat, err))
			return
		}

		// TODO: move this list to the memory cache
		// We should check, that all components are presented in our db.
		dbComps, err := dbInst.GetComponentsAsMap()
		if err != nil {
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		for _, comp := range components.Components {
			if _, ok := dbComps[comp]; !ok {
				apiErrors.RaiseBadRequestErr(c, apiErrors.NewErrComponentDSNotExist(comp))
				return
			}
		}

		c.Next()
	}
}

// authAudit emits a structured audit log event for authentication/authorization decisions.
// All fields follow a consistent schema for SIEM integration.
func authAudit(logger *zap.Logger, action, result, idpType, username, reason string) {
	fields := []zap.Field{
		zap.String("event", "auth_audit"),
		zap.String("action", action),
		zap.String("result", result),
	}
	if idpType != "" {
		fields = append(fields, zap.String("idp_type", idpType))
	}
	if username != "" {
		fields = append(fields, zap.String("username", username))
	}
	if reason != "" {
		fields = append(fields, zap.String("reason", reason))
	}

	if result == "success" {
		logger.Info("auth_audit", fields...)
	} else {
		logger.Warn("auth_audit", fields...)
	}
}

// authenticate verifies the bearer token and stores the caller identity in the
// gin context. Failures are logged as audit events.
func authenticate(authn *auth.Authenticator, rawToken string, c *gin.Context, logger *zap.Logger) error {
	claims, err := authn.Verify(c.Request.Context(), rawToken)
	if err != nil {
		authAudit(logger, "token_validation", "failure", "", "", err.Error())
		return apiErrors.ErrAuthTokenInvalid
	}

	if claims.Subject == "" {
		authAudit(logger, "token_validation", "failure", claims.Provider, "", "missing_subject_claim")
		return apiErrors.ErrAuthTokenInvalid
	}

	roles := claims.Roles
	if roles == nil {
		roles = []string{}
	}

	c.Set(v2.UserIDContextKey, claims.Subject)
	c.Set(v2.UserIDRolesContextKey, roles)

	logger.Debug("authenticated request",
		zap.String("provider", claims.Provider),
		zap.String("user_id", claims.Subject),
		zap.Strings("roles", roles),
	)
	authAudit(logger, "token_validation", "success", claims.Provider, claims.Username, "")

	return nil
}

// AuthenticationMW validates JWT tokens.
// Missing or invalid tokens result in 401.
func AuthenticationMW(authn *auth.Authenticator, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			authAudit(logger, "token_validation", "failure", "", "", "missing_authorization_header")
			apiErrors.RaiseNotAuthorizedErr(c, apiErrors.ErrAuthNotAuthenticated)
			return
		}

		rawToken := strings.TrimPrefix(authHeader, "Bearer ")
		if err := authenticate(authn, rawToken, c, logger); err != nil {
			apiErrors.RaiseNotAuthorizedErr(c, err)
			return
		}

		c.Next()
	}
}

// SetJWTClaims performs soft authentication for public-read endpoints.
// If no Authorization header is present, the request proceeds anonymously.
// If a token is present but invalid/forged, access is denied (401).
func SetJWTClaims(authn *auth.Authenticator, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		rawToken := strings.TrimPrefix(authHeader, "Bearer ")
		if err := authenticate(authn, rawToken, c, logger); err != nil {
			apiErrors.RaiseNotAuthorizedErr(c, err)
			return
		}

		c.Next()
	}
}

// RBACAuthorizationMW resolves user roles from the token claims for write operations (POST/PATCH).
// Users without a configured role are rejected with 403 Forbidden.
func RBACAuthorizationMW(rbacService *rbac.Service, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rolesVal, exists := c.Get(v2.UserIDRolesContextKey)
		if !exists {
			authAudit(logger, "authorization", "denied", "", "", "roles_not_in_context")
			apiErrors.RaiseNotAuthorizedErr(c, apiErrors.ErrAuthNotAuthenticated)
			return
		}

		roles, ok := rolesVal.([]string)
		if !ok {
			authAudit(logger, "authorization", "denied", "", "", "roles_invalid_type")
			apiErrors.RaiseNotAuthorizedErr(c, apiErrors.ErrAuthNotAuthenticated)
			return
		}

		userID, _ := c.Get(v2.UserIDContextKey)
		userIDStr, _ := userID.(string)

		if !rbacService.HasAuthorizedRole(roles) {
			authAudit(logger, "authorization", "denied", "", userIDStr, "no_matching_rbac_role")
			apiErrors.RaiseForbiddenErr(c, apiErrors.ErrAuthForbidden)
			return
		}

		role := rbacService.ResolveRole(roles)
		c.Set(v2.RoleContextKey, role)
		authAudit(logger, "authorization", "success", "", userIDStr, fmt.Sprintf("role=%d", int(role)))

		c.Next()
	}
}

// resolveCallerRole returns the highest application role granted by the role
// names stored in the request context, or NoRole when the caller is anonymous.
func resolveCallerRole(c *gin.Context, rbacService *rbac.Service) rbac.Role {
	rolesVal, exists := c.Get(v2.UserIDRolesContextKey)
	if !exists {
		return rbac.NoRole
	}

	roles, ok := rolesVal.([]string)
	if !ok {
		return rbac.NoRole
	}

	return rbacService.ResolveRole(roles)
}

// DenyReporterScopeMW rejects machine reporters on human-facing write endpoints.
// A reporter role only grants POST /v2/events for system incidents; every other
// write route forbids it. Callers holding another role — including role names
// this deployment does not map — keep their previous access.
func DenyReporterScopeMW(rbacService *rbac.Service, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !resolveCallerRole(c, rbacService).IsReporter() {
			c.Next()
			return
		}

		userID, _ := c.Get(v2.UserIDContextKey)
		userIDStr, _ := userID.(string)
		authAudit(logger, "authorization", "denied", "", userIDStr, "reporter_scope_violation")

		apiErrors.RaiseForbiddenErr(c, apiErrors.ErrInsufficientRole)
	}
}

func CheckEventExistenceMW(dbInst *db.DB, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger.Debug("checking event existence")

		var incID v2.IncidentID
		if err := c.ShouldBindUri(&incID); err != nil {
			logger.Debug("event existence check failed: invalid event ID in URI", zap.Error(err))
			apiErrors.RaiseBadRequestErr(c, err)
			return
		}

		event, err := dbInst.GetIncident(incID.ID)
		if err != nil {
			if errors.Is(err, db.ErrDBIncidentDSNotExist) {
				apiErrors.RaiseStatusNotFoundErr(c, apiErrors.ErrIncidentDSNotExist)
				return
			}
			logger.Error("event existence check failed: database error", zap.Error(err))
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		c.Set(eventContextKey, event)
		c.Next()
	}
}

func ErrorHandle() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}

		status := c.Writer.Status()

		var err error
		err = c.Errors.Last()
		if status >= http.StatusInternalServerError {
			err = apiErrors.ErrInternalError
		}

		c.JSON(-1, apiErrors.ReturnError(err))
	}
}

func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now().UTC()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		end := time.Now().UTC()
		latency := end.Sub(start)

		fields := []zapcore.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("ip", c.ClientIP()),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Duration("latency", latency),
		}

		if query != "" {
			fields = append(fields, zap.String("query", query))
		}

		switch {
		case c.Writer.Status() >= http.StatusInternalServerError:
			msg := fmt.Sprintf("panic was recovered, %s", apiErrors.ErrInternalError)
			if c.Errors.Last() != nil {
				msg = c.Errors.Last().Error()
			}
			log.Error(msg, fields...)
		case c.Writer.Status() >= http.StatusBadRequest:
			for _, e := range c.Errors.Errors() {
				log.Info(e, fields...)
			}
		default:
			log.Info(path, fields...)
		}
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set(
			"Access-Control-Allow-Headers",
			"Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, "+
				"Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
