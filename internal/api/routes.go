package api

import (
	"fmt"

	"github.com/stackmon/otc-status-dashboard/internal/api/rss"
	v1 "github.com/stackmon/otc-status-dashboard/internal/api/v1"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	newRSS "github.com/stackmon/otc-status-dashboard/internal/rss"
)

const (
	v1Group = "v1"
	v2Group = "v2"
)

// InitRoutes registers all HTTP routes. A failure to read the OpenAPI spec is
// returned so that a misconfigured deployment fails at boot instead of serving
// 500s on the first request.
func (a *API) InitRoutes(openAPISpecPath string) error {
	a.initV1Routes()
	a.initV2Routes()
	a.initRSSRoutes()

	openAPIHandler, err := filteredOpenAPIHandler(openAPISpecPath)
	if err != nil {
		return fmt.Errorf("init /openapi.json handler: %w", err)
	}
	a.r.GET("/openapi.json", openAPIHandler)
	a.r.GET("/swagger/*any", swaggerUIHandler("/openapi.json"))
	return nil
}

// initV1Routes registers the deprecated /v1 routes kept for compatibility.
func (a *API) initV1Routes() {
	v1API := a.r.Group(v1Group)
	{
		v1API.GET("component_status", v1.GetComponentsStatusHandler(a.db, a.log))
		v1API.POST("component_status",
			AuthenticationMW(a.authn, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			v1.PostComponentStatusHandler(a.db, a.log),
		)

		v1API.GET("incidents", v1.GetIncidentsHandler(a.db, a.log))
	}
}

// initV2Routes registers the current /v2 routes.
func (a *API) initV2Routes() {
	v2API := a.r.Group(v2Group)
	{
		v2API.GET("components", v2.GetComponentsHandler(a.db, a.log))
		v2API.POST("components",
			AuthenticationMW(a.authn, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			v2.PostComponentHandler(a.db, a.log))
		v2API.GET("components/:id", v2.GetComponentHandler(a.db, a.log))

		// Incidents section. Deprecated.
		// will be removed in a later version.
		v2API.GET("incidents",
			SetJWTClaims(a.authn, a.log),
			v2.GetIncidentsHandler(a.db, a.log, a.rbac))
		v2API.POST("incidents",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			ValidateComponentsMW(a.db, a.log),
			v2.PostIncidentHandler(a.db, a.log),
		)
		v2API.GET("incidents/:eventID",
			SetJWTClaims(a.authn, a.log),
			v2.GetIncidentHandler(a.db, a.log, a.rbac))
		v2API.PATCH("incidents/:eventID",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			v2.PatchIncidentHandler(a.db, a.log))
		v2API.POST("incidents/:eventID/extract",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			ValidateComponentsMW(a.db, a.log),
			v2.PostIncidentExtractHandler(a.db, a.log))
		v2API.PATCH("incidents/:eventID/updates/:updateID",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			v2.PatchEventUpdateTextHandler(a.db, a.log))

		// Events section.
		// Get /v2/events returns events page with pagination.
		v2API.GET("events",
			SetJWTClaims(a.authn, a.log),
			v2.GetEventsHandler(a.db, a.log, a.rbac))
		// POST /v2/events intentionally omits DenyReporterScopeMW: reporting an
		// event is the one write a reporter role is allowed to perform.
		v2API.POST("events",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			ValidateComponentsMW(a.db, a.log),
			v2.PostIncidentHandler(a.db, a.log))
		v2API.GET("events/:eventID",
			SetJWTClaims(a.authn, a.log),
			v2.GetIncidentHandler(a.db, a.log, a.rbac))
		v2API.PATCH("events/:eventID",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			v2.PatchIncidentHandler(a.db, a.log))
		v2API.POST("events/:eventID/extract",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			ValidateComponentsMW(a.db, a.log),
			v2.PostIncidentExtractHandler(a.db, a.log))
		v2API.PATCH("events/:eventID/updates/:updateID",
			AuthenticationMW(a.authn, a.log),
			RBACAuthorizationMW(a.rbac, a.log),
			DenyReporterScopeMW(a.rbac, a.log),
			CheckEventExistenceMW(a.db, a.log),
			v2.PatchEventUpdateTextHandler(a.db, a.log))
		// Availability section.
		v2API.GET("availability", v2.GetComponentsAvailabilityHandler(a.db, a.log))

		// For testing purposes only.
		v2API.GET("rss/", newRSS.HandleRSS(a.db, a.log))
	}
}

// initRSSRoutes registers the RSS feed consumed by the frontend.
func (a *API) initRSSRoutes() {
	rssFEED := a.r.Group("rss")
	{
		rssFEED.GET("/", rss.HandleRSS(a.db, a.log))
	}
}
