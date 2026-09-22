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

	mappedPort, errPort := container.MappedPort(ctx, "5432/tcp")
	if errPort != nil {
		log.Printf("failed to resolve the mapped postgres port: %s", errPort)
		if errTerm := testcontainers.TerminateContainer(container); errTerm != nil {
			log.Printf("failed to terminate container: %s", errTerm)
		}
		os.Exit(1)
	}
	port := mappedPort.Port()

	// Only set up cleanup once the container is reachable
	defer func() {
		if err = testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}()
	databaseURL = fmt.Sprintf(databaseURL, dbUser, dbPassword, port, dbName)

	// Apply migrations (add sslmode=disable for test container)
	migrationURL := databaseURL + "?sslmode=disable"
	if errMigr := applyMigrations(migrationURL); errMigr != nil {
		log.Printf("failed to apply migrations: %s", err)
		return
	}

	defer testIDP.server.Close()

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

func initTests(t *testing.T) *gin.Engine {
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

	authn := testIDP.provider(t, testRBACService().RoleNames()...)

	initRoutesV2(t, r, d, authn, logger)

	return r
}

func initRoutesV2(t *testing.T, c *gin.Engine, dbInst *db.DB, authn *auth.Provider, logger *zap.Logger) {
	t.Helper()
	t.Log("init routes for V2")

	rbacSvc := testRBACService()

	v2Api := c.Group("v2")

	v2Api.GET("components", v2.GetComponentsHandler(dbInst, logger))
	v2Api.POST("components",
		api.AuthenticationMW(authn, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
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
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchIncidentHandler(dbInst, logger))
	v2Api.POST("incidents/:eventID/extract",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentExtractHandler(dbInst, logger))
	v2Api.PATCH("incidents/:eventID/updates/:updateID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
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
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		v2.PatchIncidentHandler(dbInst, logger))
	v2Api.POST("events/:eventID/extract",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(dbInst, logger),
		api.ValidateComponentsMW(dbInst, logger),
		v2.PostIncidentExtractHandler(dbInst, logger))
	v2Api.PATCH("events/:eventID/updates/:updateID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
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
