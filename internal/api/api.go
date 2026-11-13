package api

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
	"github.com/stackmon/otc-status-dashboard/internal/static"
)

// oidcDiscoveryTimeout bounds the provider discovery and JWKS check at startup.
const oidcDiscoveryTimeout = 15 * time.Second

type API struct {
	r             *gin.Engine
	db            *db.DB
	log           *zap.Logger
	authn         *auth.Provider
	rbac          *rbac.Service
	notifier      *notification.Publisher
	publicBaseURL string
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

	// gin trusts every peer by default, which would let any client pick the
	// address that is logged for it through X-Forwarded-For.
	if err = setTrustedProxies(r, cfg); err != nil {
		return nil, err
	}

	r.Use(Logger(log), gin.Recovery())
	r.Use(ErrorHandle())
	r.Use(SecurityHeaders())
	r.Use(CORSMiddleware())

	catchAll, err := static.NewHandler(cfg.Static, log)
	if err != nil {
		return nil, fmt.Errorf("init static site proxy: %w", err)
	}

	r.NoRoute(catchAll)

	ncfg, err := notification.ConfigFromConf(cfg)
	if err != nil {
		return nil, fmt.Errorf("could not parse notification config: %w", err)
	}

	a := &API{
		r:             r,
		db:            database,
		log:           log,
		authn:         authn,
		rbac:          rbacService,
		notifier:      notification.NewPublisher(ncfg, database),
		publicBaseURL: cfg.PublicBaseURL,
	}
	if err = a.InitRoutes(cfg.OpenAPISpecPath); err != nil {
		return nil, fmt.Errorf("init routes: %w", err)
	}
	return a, nil
}

// setTrustedProxies restricts forwarded-header trust to SD_TRUSTED_PROXIES. With
// no configured proxy no header is trusted and the client IP is the TCP peer.
func setTrustedProxies(r *gin.Engine, cfg *conf.Config) error {
	proxies, err := cfg.TrustedProxyList()
	if err != nil {
		return err
	}

	if err = r.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("set trusted proxies: %w", err)
	}

	return nil
}

func newAuthProvider(cfg *conf.Config, roleNames []string) (*auth.Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), oidcDiscoveryTimeout)
	defer cancel()

	provider, err := auth.NewProvider(ctx, auth.ProviderConfig{
		Issuer:        cfg.OIDC.Issuer,
		ClientID:      cfg.OIDC.ClientID,
		RoleNames:     roleNames,
		UsernameClaim: cfg.OIDC.UsernameClaim,
		ProjectID:     cfg.OIDC.ProjectID,
	})
	if err != nil {
		return nil, fmt.Errorf("could not initialise the OIDC provider: %w", err)
	}

	return provider, nil
}

func (a *API) Router() *gin.Engine {
	return a.r
}

// Publisher returns the notification publisher so the delivery worker's Notify can
// be wired in during app startup.
func (a *API) Publisher() *notification.Publisher {
	return a.notifier
}
