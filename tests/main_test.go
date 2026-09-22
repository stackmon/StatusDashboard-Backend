package tests

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/api"
	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	v1 "github.com/stackmon/otc-status-dashboard/internal/api/v1"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

const (
	pgImage   = "postgres:15-alpine"
	pgDump    = "dump_test.sql"
	pgDumpDir = "testdata"

	dbName     = "status_dashboard"
	dbUser     = "pg"
	dbPassword = "pass"
)

var databaseURL = "postgresql://%s:%s@localhost:%s/%s"

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx,
		pgImage,
		postgres.WithInitScripts(filepath.Join(pgDumpDir, pgDump)),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)

	if err != nil {
		log.Printf("failed to start container: %s", err)
		os.Exit(1)
	}

	// Only set up cleanup if container was created successfully
	defer func() {
		if err = testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}()

	ports, _ := container.Ports(ctx)
	port := ports["5432/tcp"][0].HostPort
	databaseURL = fmt.Sprintf(databaseURL, dbUser, dbPassword, port, dbName)

	// Apply migrations (add sslmode=disable for test container)
	migrationURL := databaseURL + "?sslmode=disable"
	if errMigr := applyMigrations(migrationURL); errMigr != nil {
		log.Printf("failed to apply migrations: %s", err)
		return
	}

	m.Run()
}

func applyMigrations(dbURL string) error {
	// Get the project root directory
	migrationsPath := filepath.Join("..", "db", "migrations")

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationsPath),
		dbURL,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	// Apply all migrations
	if errMig := m.Up(); errMig != nil && !errors.Is(errMig, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("migrations applied successfully")
	return nil
}

func initTests(t *testing.T) (*gin.Engine, *db.DB) {
	t.Helper()
	t.Log("init structs")

	d, err := db.New(&conf.Config{
		DB: databaseURL,
		// if you want to debug gorm, uncomment it
		//LogLevel: conf.DevelopMode,
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.NoRoute(apiErrors.Return404)
	r.Use(api.ErrorHandle())

	logger, _ := zap.NewDevelopment()

	// Provide RBAC role names and the local HMAC secret so conf.Validate() passes.
	t.Setenv("SD_SECRET_KEY", testHMACSecret)
	t.Setenv("SD_RBAC_GROUPS_CREATORS", creatorRole)
	t.Setenv("SD_RBAC_GROUPS_OPERATORS", operatorRole)
	t.Setenv("SD_RBAC_GROUPS_ADMINS", adminRole)

	cfg, err := conf.LoadConf()
	require.NoError(t, err)

	// SD_OIDC_ISSUER is not set here, so the tests exercise the transitional
	// local HMAC branch, like a deployment without Zitadel configured.
	authn := auth.NewAuthenticator(nil, cfg.SecretKeyV1)

	initRoutesV1(t, r, d, authn, logger)
	initRoutesV2(t, r, d, authn, logger)

	return r, d
}

func initRoutesV1(t *testing.T, c *gin.Engine, dbInst *db.DB, authn *auth.Authenticator, logger *zap.Logger) {
	t.Helper()
	t.Log("init routes for V1")

	v1Api := c.Group("v1")

	v1Api.GET("component_status", v1.GetComponentsStatusHandler(dbInst, logger))
	v1Api.POST("component_status",
		api.AuthenticationMW(authn, logger),
		v1.PostComponentStatusHandler(dbInst, logger))

	v1Api.GET("incidents", v1.GetIncidentsHandler(dbInst, logger))
}

func initRoutesV2(t *testing.T, c *gin.Engine, dbInst *db.DB, authn *auth.Authenticator, logger *zap.Logger) {
	t.Helper()
	t.Log("init routes for V2")

	rbacSvc := testRBACService()

	v2Api := c.Group("v2")

	v2Api.GET("components", v2.GetComponentsHandler(dbInst, logger))
	v2Api.POST("components",
		api.AuthenticationMW(authn, logger),
		v2.PostComponentHandler(dbInst, logger))
	v2Api.GET("components/:id", v2.GetComponentHandler(dbInst, logger))

	// Incidents routes (deprecated).
	v2Api.GET("incidents",
		api.SetJWTClaims(authn, logger),
		v2.GetIncidentsHandler(dbInst, logger, rbacSvc))
	v2Api.POST("incidents",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentHandler(dbInst, logger))
	v2Api.GET("incidents/:eventID",
		api.SetJWTClaims(authn, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.GetIncidentHandler(dbInst, logger, rbacSvc))
	v2Api.PATCH("incidents/:eventID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchIncidentHandler(dbInst, logger))
	v2Api.POST("incidents/:eventID/extract",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentExtractHandler(dbInst, logger))
	v2Api.PATCH("incidents/:eventID/updates/:updateID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchEventUpdateTextHandler(dbInst, logger))

	// Events routes.
	v2Api.GET("events",
		api.SetJWTClaims(authn, logger),
		v2.GetEventsHandler(dbInst, logger, rbacSvc))
	v2Api.POST("events",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentHandler(dbInst, logger))
	v2Api.GET("events/:eventID",
		api.SetJWTClaims(authn, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.GetIncidentHandler(dbInst, logger, rbacSvc))
	v2Api.PATCH("events/:eventID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchIncidentHandler(dbInst, logger))
	v2Api.POST("events/:eventID/extract",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentExtractHandler(dbInst, logger))
	v2Api.PATCH("events/:eventID/updates/:updateID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchEventUpdateTextHandler(dbInst, logger))

	v2Api.GET("availability", v2.GetComponentsAvailabilityHandler(dbInst, logger))
}

func truncateIncidents(t *testing.T) {
	t.Helper()
	t.Log("cleaning up incident-related tables before test")

	gormDB, err := gorm.Open(gormpostgres.Open(databaseURL), &gorm.Config{})
	require.NoError(t, err, "failed to open gorm connection for truncation")

	result := gormDB.Exec("TRUNCATE TABLE incident, incident_status, incident_component_relation RESTART IDENTITY")
	require.NoError(t, result.Error, "failed to truncate incident tables")

	sqlDB, err := gormDB.DB()
	require.NoError(t, err, "failed to get sql.DB from gorm for closing")
	err = sqlDB.Close()
	require.NoError(t, err, "failed to close gorm connection for truncation")
}

// restoreFixtureIncident re-inserts the dump_test.sql fixture incident
// so that V1 GET tests see the expected data even after other tests modify the DB.
func restoreFixtureIncident(t *testing.T) {
	t.Helper()
	truncateIncidents(t)

	gormDB, err := gorm.Open(gormpostgres.Open(databaseURL), &gorm.Config{})
	require.NoError(t, err)

	sqls := []string{
		`INSERT INTO incident (id, text, start_date, end_date, impact, type, system, status)
		 VALUES (1, 'Closed incident without any update', '2025-05-22 10:12:42', '2025-05-22 11:12:42', 1, 'incident', true, 'resolved')`,
		`INSERT INTO incident_component_relation (incident_id, component_id) VALUES (1, 1)`,
		`INSERT INTO incident_status (incident_id, "timestamp", text, status)
		 VALUES (1, '2025-05-22 11:12:42', 'close incident', 'resolved')`,
		`SELECT setval('incident_id_seq', (SELECT COALESCE(MAX(id), 0) FROM incident))`,
	}
	for _, s := range sqls {
		require.NoError(t, gormDB.Exec(s).Error)
	}

	sqlDB, err := gormDB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}
