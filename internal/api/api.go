package api

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
	"github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// oidcDiscoveryTimeout bounds the provider discovery and JWKS check at startup.
const oidcDiscoveryTimeout = 15 * time.Second

type API struct {
	r     *gin.Engine
	db    *db.DB
	log   *zap.Logger
	authn *auth.Provider
	rbac  *rbac.Service
}

func New(cfg *conf.Config, log *zap.Logger, database *db.DB) (*API, error) {
	if cfg.LogLevel != conf.DevelopMode {
		gin.SetMode(gin.ReleaseMode)
	}

	rbacService := rbac.New(rbac.Config{
		Creators:  cfg.RBAC.Creators,
		Operators: cfg.RBAC.Operators,
		Admins:    cfg.RBAC.Admins,
		Reporters: cfg.RBAC.Reporters,
	})

	authn, err := newAuthProvider(cfg, rbacService.RoleNames())
	if err != nil {
		return nil, err
	}

	r := gin.New()
	r.Use(Logger(log), gin.Recovery())
	r.Use(ErrorHandle())
	r.Use(CORSMiddleware())
	r.NoRoute(errors.Return404)

	a := &API{
		r:     r,
		db:    database,
		log:   log,
		authn: authn,
		rbac:  rbacService,
	}
	if err = a.InitRoutes(cfg.OpenAPISpecPath); err != nil {
		return nil, fmt.Errorf("init routes: %w", err)
	}
	return a, nil
}

func newAuthProvider(cfg *conf.Config, roleNames []string) (*auth.Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), oidcDiscoveryTimeout)
	defer cancel()

	provider, err := auth.NewProvider(ctx, auth.ProviderConfig{
		Issuer:        cfg.OIDC.Issuer,
		ClientID:      cfg.OIDC.ClientID,
		RolesClaim:    cfg.OIDC.RolesClaim,
		RoleNames:     roleNames,
		UsernameClaim: cfg.OIDC.UsernameClaim,
	})
	if err != nil {
		return nil, fmt.Errorf("could not initialise the OIDC provider: %w", err)
	}

	return provider, nil
}

func (a *API) Router() *gin.Engine {
	return a.r
}
