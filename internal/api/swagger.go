package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	"gopkg.in/yaml.v3"
)

// loadOpenAPISpec reads the YAML spec at path, parses it, and applies the
// public-spec strip pipeline (non-GET methods, auth details, `servers`).
// The returned map is safe to serve directly as JSON.
//
// The `servers` block is intentionally dropped so Swagger UI falls back to
// the origin it was loaded from. Trusting client-controlled headers
// (`Host`, `X-Forwarded-Proto`) to rewrite this URL would let a caller
// poison the advertised API base URL.
func loadOpenAPISpec(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read OpenAPI spec: %w", err)
	}

	var spec map[string]interface{}
	if err = yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse OpenAPI spec: %w", err)
	}

	stripNonGetMethods(spec)
	stripAuthDetails(spec)
	delete(spec, "servers")

	return spec, nil
}

// filteredOpenAPIHandler returns a gin.HandlerFunc that serves the pre-parsed
// spec at specPath. The spec is read, parsed, and stripped exactly once — at
// handler construction. A failure here surfaces at process boot (callers
// propagate it up to New()), so a broken deployment is caught by liveness /
// rollback rather than by the first user hitting /openapi.json.
//
// specPath is resolved by os.ReadFile, so relative paths are interpreted
// against the process working directory. The default ("openapi.yaml") matches
// the container's WORKDIR layout; tests and non-standard deployments inject
// an explicit path via configuration.
func filteredOpenAPIHandler(specPath string) (gin.HandlerFunc, error) {
	spec, err := loadOpenAPISpec(specPath)
	if err != nil {
		return nil, err
	}

	return func(c *gin.Context) {
		c.JSON(http.StatusOK, spec)
	}, nil
}

// swaggerInitializerJSTmpl is the JS bootstrap snippet served at
// /swagger/swagger-initializer.js. It mirrors the upstream swaggo/files
// template but pins the spec URL so we don't depend on swaggo/gin-swagger
// (which transitively pulls in swaggo/swag and its ~10 OpenAPI deps).
const swaggerInitializerJSTmpl = `window.onload = function() {
  window.ui = SwaggerUIBundle({
    url: %q,
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });
};
`

// swaggerUIHandler serves the Swagger UI static assets bundled in
// github.com/swaggo/files and points the UI at the supplied spec URL.
// It replaces ginSwagger.WrapHandler so we can drop the swaggo/gin-swagger
// and swaggo/swag dependencies entirely.
//
// We rely on gin's default RedirectTrailingSlash=true to normalise the
// bare `/swagger` path to `/swagger/` before the handler runs; we do not
// reimplement that here. Requests for `/swagger/index.html` are
// canonicalised by net/http.FileServer to `./` (→ `/swagger/`), so the
// directory index is reached via either form.
func swaggerUIHandler(specURL string) gin.HandlerFunc {
	initializerJS := fmt.Sprintf(swaggerInitializerJSTmpl, specURL)
	fileServer := http.StripPrefix("/swagger", http.FileServer(swaggerFiles.HTTP))

	return func(c *gin.Context) {
		if c.Param("any") == "/swagger-initializer.js" {
			c.Header("Content-Type", "application/javascript; charset=utf-8")
			c.String(http.StatusOK, initializerJS)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

// stripNonGetMethods removes all non-GET operations from the OpenAPI spec
// and prunes unused tags.
func stripNonGetMethods(spec map[string]interface{}) {
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return
	}

	filtered, usedTags := filterGETPaths(paths)
	spec["paths"] = filtered

	pruneUnusedTags(spec, usedTags)
}

func filterGETPaths(paths map[string]interface{}) (map[string]interface{}, map[string]struct{}) {
	filtered := make(map[string]interface{})
	usedTags := make(map[string]struct{})

	for path, methods := range paths {
		methodMap, isMap := methods.(map[string]interface{})
		if !isMap {
			continue
		}

		getOp, hasGet := methodMap["get"]
		if !hasGet {
			continue
		}

		filtered[path] = map[string]interface{}{"get": getOp}
		collectTags(getOp, usedTags)
	}

	return filtered, usedTags
}

func collectTags(operation interface{}, tags map[string]struct{}) {
	opMap, isMap := operation.(map[string]interface{})
	if !isMap {
		return
	}

	tagList, hasTags := opMap["tags"].([]interface{})
	if !hasTags {
		return
	}

	for _, t := range tagList {
		if tag, isString := t.(string); isString {
			tags[tag] = struct{}{}
		}
	}
}

func pruneUnusedTags(spec map[string]interface{}, usedTags map[string]struct{}) {
	tags, hasTags := spec["tags"].([]interface{})
	if !hasTags {
		return
	}

	var kept []interface{}

	for _, t := range tags {
		if name := extractTagName(t); name != "" {
			if _, used := usedTags[name]; used {
				kept = append(kept, t)
			}
		}
	}

	spec["tags"] = kept
}

func extractTagName(tag interface{}) string {
	tagMap, isMap := tag.(map[string]interface{})
	if !isMap {
		return ""
	}

	name, isString := tagMap["name"].(string)
	if !isString {
		return ""
	}

	return name
}

// stripAuthDetails removes authentication-related content from the spec:
// security block, securitySchemes, Token* schemas, and auth description.
func stripAuthDetails(spec map[string]interface{}) {
	delete(spec, "security")

	stripInfoDescription(spec)
	stripAuthSchemas(spec)
}

// stripInfoDescription removes the auth section from info.description.
//
// The auth section is delimited by HTML comment sentinels so a future rename
// of the markdown heading (e.g. `## Authentication` → `## Auth`) does not
// silently leak the section into the public docs. Author convention in
// openapi.yaml:
//
//	<!-- auth-start -->
//	## Authentication
//	... (any prose, may be renamed freely) ...
//	<!-- auth-end -->
//
// For backward compatibility with the legacy convention (no sentinels),
// we also fall back to trimming at the literal `## Authentication` heading.
// The fallback is intentionally narrow — it will miss renamed headings, so
// new auth prose must use the sentinel form.
func stripInfoDescription(spec map[string]interface{}) {
	info, isMap := spec["info"].(map[string]interface{})
	if !isMap {
		return
	}

	desc, isString := info["description"].(string)
	if !isString {
		return
	}

	const (
		sentinelStart = "<!-- auth-start -->"
		sentinelEnd   = "<!-- auth-end -->"
	)

	if start := strings.Index(desc, sentinelStart); start >= 0 {
		head := desc[:start]
		tail := ""
		if end := strings.Index(desc[start:], sentinelEnd); end >= 0 {
			tail = desc[start+end+len(sentinelEnd):]
		}
		info["description"] = strings.TrimSpace(head + tail)
		return
	}

	if idx := strings.Index(desc, "## Authentication"); idx >= 0 {
		info["description"] = strings.TrimSpace(desc[:idx])
	}
}

// rbacSchemaFields returns the schema/property pairs that should be hidden
// from the public OpenAPI spec. Matching by schema name + property (instead
// of property name alone) avoids accidentally stripping fields with the
// same name from unrelated schemas — e.g. a future `Component.version` for
// software version must not be stripped just because `version` is also an
// RBAC concurrency token on the RBAC schemas below.
//
// Keep this list in sync with the RBAC branch as those schemas land. Today
// the current openapi.yaml does not declare any of these properties, so
// stripRBACFields is a no-op in production — it is forward-compatible
// defense for the unmerged RBAC branch.
//
// Returned as a function (not a package-level var) to keep the policy
// table immutable from the perspective of other packages and to satisfy
// the gochecknoglobals lint rule. The map is small and built once per
// spec load (handler construction), so allocation cost is irrelevant.
func rbacSchemaFields() map[string]map[string]struct{} {
	return map[string]map[string]struct{}{
		"Component":        {"creator": {}, "contact_email": {}, "version": {}},
		"ComponentRequest": {"creator": {}, "contact_email": {}, "version": {}},
		"Incident":         {"creator": {}, "contact_email": {}, "version": {}},
		"Event":            {"creator": {}, "contact_email": {}, "version": {}},
	}
}

func stripAuthSchemas(spec map[string]interface{}) {
	components, isMap := spec["components"].(map[string]interface{})
	if !isMap {
		return
	}

	delete(components, "securitySchemes")

	schemas, isMap := components["schemas"].(map[string]interface{})
	if !isMap {
		return
	}

	rbacFields := rbacSchemaFields()
	for name, schema := range schemas {
		if strings.HasPrefix(name, "Token") {
			delete(schemas, name)
			continue
		}
		fields, hasFields := rbacFields[name]
		if !hasFields {
			continue
		}
		stripRBACFields(schema, fields)
	}
}

// stripRBACFields removes the named properties (and any matching `required`
// entries) from a single schema. The caller owns the policy of *which*
// schema/field pairs to strip via rbacSchemaFields.
func stripRBACFields(schema interface{}, fields map[string]struct{}) {
	schemaMap, isMap := schema.(map[string]interface{})
	if !isMap {
		return
	}

	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		for field := range fields {
			delete(props, field)
		}
	}

	if required, ok := schemaMap["required"].([]interface{}); ok {
		var kept []interface{}
		for _, r := range required {
			if name, isStr := r.(string); isStr {
				if _, hidden := fields[name]; hidden {
					continue
				}
			}
			kept = append(kept, r)
		}
		schemaMap["required"] = kept
	}
}
