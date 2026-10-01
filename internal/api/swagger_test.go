package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { //nolint:gochecknoinits // set gin test mode before tests
	gin.SetMode(gin.TestMode)
}

func requireMap(t *testing.T, m map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	v, ok := m[key]
	require.True(t, ok, "key %q not found", key)
	result, ok := v.(map[string]interface{})
	require.True(t, ok, "key %q is not map[string]interface{}", key)
	return result
}

func requireSlice(t *testing.T, m map[string]interface{}, key string) []interface{} {
	t.Helper()
	v, ok := m[key]
	require.True(t, ok, "key %q not found", key)
	result, ok := v.([]interface{})
	require.True(t, ok, "key %q is not []interface{}", key)
	return result
}

// writeSpecFile writes content to a fresh temp file and returns the path.
// Each call uses t.TempDir(), so callers are safe under t.Parallel().
func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestFilteredOpenAPIHandler_FileNotFound(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	handler, err := filteredOpenAPIHandler(missing)
	require.Error(t, err, "missing spec file must fail at construction, not on first request")
	assert.Nil(t, handler)
	assert.Contains(t, err.Error(), "read OpenAPI spec")
}

func TestFilteredOpenAPIHandler_InvalidYAML(t *testing.T) {
	t.Parallel()

	path := writeSpecFile(t, ":\ninvalid: [yaml: {{{\n")

	handler, err := filteredOpenAPIHandler(path)
	require.Error(t, err, "invalid YAML must fail at construction, not on first request")
	assert.Nil(t, handler)
	assert.Contains(t, err.Error(), "parse OpenAPI spec")
}

func TestFilteredOpenAPIHandler_ValidSpec(t *testing.T) {
	t.Parallel()

	specYAML := `
info:
  title: Test API
  description: "Some text\n## Authentication\nSecret stuff"
paths:
  /status:
    get:
      tags:
        - Status
      summary: Get status
    post:
      summary: Create status
  /write-only:
    put:
      summary: Write only
tags:
  - name: Status
  - name: Write
security:
  - bearerAuth: []
components:
  securitySchemes:
    bearerAuth:
      type: http
  schemas:
    TokenResponse:
      type: object
    StatusModel:
      type: object
      properties:
        name:
          type: string
        creator:
          type: string
      required:
        - name
        - creator
    Component:
      type: object
      properties:
        name:
          type: string
        creator:
          type: string
      required:
        - name
        - creator
`
	path := writeSpecFile(t, specYAML)

	handler, err := filteredOpenAPIHandler(path)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openapi", nil)

	handler(c)

	require.Equal(t, http.StatusOK, w.Code)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))

	paths := requireMap(t, result, "paths")
	assert.Contains(t, paths, "/status")
	assert.NotContains(t, paths, "/write-only")

	statusMethods := requireMap(t, paths, "/status")
	assert.Contains(t, statusMethods, "get")
	assert.NotContains(t, statusMethods, "post")

	assert.NotContains(t, result, "security")
	assert.NotContains(t, result, "servers",
		"servers block must be omitted so Swagger UI defaults to the origin")

	tags := requireSlice(t, result, "tags")
	assert.Len(t, tags, 1)

	components := requireMap(t, result, "components")
	schemas := requireMap(t, components, "schemas")
	assert.NotContains(t, schemas, "TokenResponse")
	assert.Contains(t, schemas, "StatusModel")
	assert.Contains(t, schemas, "Component")

	// StatusModel is not in the RBAC allow-list; `creator` must survive.
	statusSchema := requireMap(t, schemas, "StatusModel")
	statusProps := requireMap(t, statusSchema, "properties")
	assert.Contains(t, statusProps, "name")
	assert.Contains(t, statusProps, "creator",
		"creator must only be stripped from RBAC-listed schemas")

	// Component IS in the RBAC allow-list; `creator` must be stripped.
	compSchema := requireMap(t, schemas, "Component")
	compProps := requireMap(t, compSchema, "properties")
	assert.Contains(t, compProps, "name")
	assert.NotContains(t, compProps, "creator")
}

func TestStripNonGetMethods_PathsNotMap(t *testing.T) {
	t.Parallel()
	spec := map[string]interface{}{"paths": "not-a-map"}
	stripNonGetMethods(spec)
	assert.Equal(t, "not-a-map", spec["paths"])
}

func TestStripNonGetMethods_NormalPaths(t *testing.T) {
	t.Parallel()
	spec := map[string]interface{}{
		"paths": map[string]interface{}{
			"/items": map[string]interface{}{
				"get":  map[string]interface{}{"tags": []interface{}{"Items"}},
				"post": map[string]interface{}{},
			},
		},
		"tags": []interface{}{
			map[string]interface{}{"name": "Items"},
			map[string]interface{}{"name": "Unused"},
		},
	}
	stripNonGetMethods(spec)

	paths := requireMap(t, spec, "paths")
	items := requireMap(t, paths, "/items")
	assert.Contains(t, items, "get")
	assert.NotContains(t, items, "post")

	tags := requireSlice(t, spec, "tags")
	assert.Len(t, tags, 1)
}

func TestFilterGETPaths(t *testing.T) {
	t.Parallel()
	paths := map[string]interface{}{
		"/write-only":  map[string]interface{}{"put": "update"},
		"/bad":         "not-a-map",
		"/no-get":      map[string]interface{}{"post": "create"},
		"/has-get":     map[string]interface{}{"get": map[string]interface{}{"tags": []interface{}{"Public"}}},
		"/get-no-tags": map[string]interface{}{"get": map[string]interface{}{}},
	}

	filtered, usedTags := filterGETPaths(paths)

	assert.NotContains(t, filtered, "/write-only")
	assert.NotContains(t, filtered, "/bad")
	assert.NotContains(t, filtered, "/no-get")
	assert.Contains(t, filtered, "/has-get")
	assert.Contains(t, filtered, "/get-no-tags")
	assert.Contains(t, usedTags, "Public")
}

func TestCollectTags(t *testing.T) {
	t.Parallel()
	t.Run("not a map", func(t *testing.T) {
		t.Parallel()
		tags := make(map[string]struct{})
		collectTags("string-value", tags)
		assert.Empty(t, tags)
	})

	t.Run("no tags key", func(t *testing.T) {
		t.Parallel()
		tags := make(map[string]struct{})
		collectTags(map[string]interface{}{"summary": "x"}, tags)
		assert.Empty(t, tags)
	})

	t.Run("tags with non-string items", func(t *testing.T) {
		t.Parallel()
		tags := make(map[string]struct{})
		collectTags(map[string]interface{}{
			"tags": []interface{}{42, "Valid"},
		}, tags)
		assert.Contains(t, tags, "Valid")
		assert.Len(t, tags, 1)
	})
}

func TestPruneUnusedTags(t *testing.T) {
	t.Parallel()
	t.Run("tags not a slice", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{"tags": "not-slice"}
		pruneUnusedTags(spec, map[string]struct{}{"X": {}})
		assert.Equal(t, "not-slice", spec["tags"])
	})

	t.Run("tag with empty name skipped", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"tags": []interface{}{
				"not-a-map",
				map[string]interface{}{"name": 123},
				map[string]interface{}{"name": "Used"},
			},
		}
		pruneUnusedTags(spec, map[string]struct{}{"Used": {}})
		kept := requireSlice(t, spec, "tags")
		assert.Len(t, kept, 1)
	})

	t.Run("unused tag removed", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"tags": []interface{}{
				map[string]interface{}{"name": "Keep"},
				map[string]interface{}{"name": "Drop"},
			},
		}
		pruneUnusedTags(spec, map[string]struct{}{"Keep": {}})
		kept := requireSlice(t, spec, "tags")
		assert.Len(t, kept, 1)
	})
}

func TestExtractTagName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tag  interface{}
		want string
	}{
		{"not a map", "string", ""},
		{"name not string", map[string]interface{}{"name": 42}, ""},
		{"valid name", map[string]interface{}{"name": "MyTag"}, "MyTag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractTagName(tt.tag))
		})
	}
}

func TestOverrideServerURL_Removed(t *testing.T) {
	t.Parallel()
	// The `servers` block is intentionally omitted from the served spec so
	// Swagger UI falls back to the origin it was loaded from. This test
	// pins that contract — the cached spec must not carry a `servers` key.
	specYAML := `
info:
  title: T
paths:
  /x:
    get:
      summary: x
servers:
  - url: "https://baked-in.example.com"
`
	path := writeSpecFile(t, specYAML)

	handler, err := filteredOpenAPIHandler(path)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openapi", nil)

	handler(c)

	require.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.NotContains(t, result, "servers")
}

func TestStripAuthDetails(t *testing.T) {
	t.Parallel()
	spec := map[string]interface{}{
		"security": []interface{}{map[string]interface{}{"bearerAuth": []interface{}{}}},
		"info":     map[string]interface{}{"description": "Hello\n## Authentication\nSecret"},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{"bearerAuth": "x"},
			"schemas":         map[string]interface{}{},
		},
	}
	stripAuthDetails(spec)

	assert.NotContains(t, spec, "security")
	info := requireMap(t, spec, "info")
	assert.Equal(t, "Hello", info["description"])
}

func TestStripInfoDescription(t *testing.T) {
	t.Parallel()
	t.Run("info not a map", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{"info": "string"}
		stripInfoDescription(spec)
		assert.Equal(t, "string", spec["info"])
	})

	t.Run("description not a string", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{"info": map[string]interface{}{"description": 42}}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, 42, info["description"])
	})

	t.Run("no authentication section", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{"info": map[string]interface{}{"description": "Just a description"}}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, "Just a description", info["description"])
	})

	t.Run("with authentication section", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"info": map[string]interface{}{"description": "Intro\n## Authentication\nDetails"},
		}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, "Intro", info["description"])
	})
}

func TestStripAuthSchemas(t *testing.T) {
	t.Parallel()
	t.Run("components not a map", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{"components": "string"}
		stripAuthSchemas(spec)
		assert.Equal(t, "string", spec["components"])
	})

	t.Run("schemas not a map", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"components": map[string]interface{}{
				"securitySchemes": "x",
				"schemas":         "not-a-map",
			},
		}
		stripAuthSchemas(spec)
		components := requireMap(t, spec, "components")
		assert.NotContains(t, components, "securitySchemes")
	})

	t.Run("Token schemas deleted and RBAC fields stripped on allow-listed schema", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"components": map[string]interface{}{
				"schemas": map[string]interface{}{
					"TokenRefresh": map[string]interface{}{"type": "object"},
					"Component": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":    map[string]interface{}{"type": "string"},
							"creator": map[string]interface{}{"type": "string"},
						},
					},
					"OtherModel": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"creator": map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		}
		stripAuthSchemas(spec)

		components := requireMap(t, spec, "components")
		schemas := requireMap(t, components, "schemas")
		assert.NotContains(t, schemas, "TokenRefresh")

		// Component is allow-listed → `creator` removed.
		comp := requireMap(t, schemas, "Component")
		compProps := requireMap(t, comp, "properties")
		assert.NotContains(t, compProps, "creator")

		// OtherModel is NOT allow-listed → `creator` preserved (regression
		// guard for the prior global-strip behavior).
		other := requireMap(t, schemas, "OtherModel")
		otherProps := requireMap(t, other, "properties")
		assert.Contains(t, otherProps, "creator")
	})
}

func TestStripRBACFields(t *testing.T) {
	t.Parallel()
	fields := map[string]struct{}{
		"creator": {}, "contact_email": {}, "version": {},
	}

	t.Run("not a map", func(t *testing.T) {
		t.Parallel()
		stripRBACFields("not-a-map", fields)
	})

	t.Run("no properties or required", func(t *testing.T) {
		t.Parallel()
		schema := map[string]interface{}{"type": "object"}
		stripRBACFields(schema, fields)
		assert.Equal(t, "object", schema["type"])
	})

	t.Run("properties with rbac fields", func(t *testing.T) {
		t.Parallel()
		schema := map[string]interface{}{
			"properties": map[string]interface{}{
				"name":          map[string]interface{}{"type": "string"},
				"creator":       map[string]interface{}{"type": "string"},
				"contact_email": map[string]interface{}{"type": "string"},
				"version":       map[string]interface{}{"type": "string"},
			},
		}
		stripRBACFields(schema, fields)

		props := requireMap(t, schema, "properties")
		assert.Contains(t, props, "name")
		assert.NotContains(t, props, "creator")
		assert.NotContains(t, props, "contact_email")
		assert.NotContains(t, props, "version")
	})

	t.Run("required with rbac and non-string items", func(t *testing.T) {
		t.Parallel()
		schema := map[string]interface{}{
			"required": []interface{}{"name", "creator", 42, "contact_email", "version"},
		}
		stripRBACFields(schema, fields)

		required := requireSlice(t, schema, "required")
		assert.Len(t, required, 2)
		assert.Contains(t, required, "name")
		assert.Contains(t, required, 42)
	})
}

func TestStripInfoDescription_Sentinels(t *testing.T) {
	t.Parallel()
	t.Run("sentinels strip enclosed block", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"info": map[string]interface{}{
				"description": "Intro\n<!-- auth-start -->\n## Auth\nsecret\n<!-- auth-end -->\nOutro",
			},
		}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, "Intro\n\nOutro", info["description"])
	})

	t.Run("sentinel start without end trims to end of string", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"info": map[string]interface{}{
				"description": "Intro\n<!-- auth-start -->\n## Auth\nsecret",
			},
		}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, "Intro", info["description"])
	})

	t.Run("legacy heading fallback still works", func(t *testing.T) {
		t.Parallel()
		spec := map[string]interface{}{
			"info": map[string]interface{}{
				"description": "Intro\n## Authentication\nsecret",
			},
		}
		stripInfoDescription(spec)
		info := requireMap(t, spec, "info")
		assert.Equal(t, "Intro", info["description"])
	})
}

func TestSwaggerUIHandler_Routing(t *testing.T) {
	t.Parallel()
	r := gin.New()
	r.GET("/swagger/*any", swaggerUIHandler("/openapi.json"))

	tests := []struct {
		name          string
		path          string
		wantStatus    int
		wantLocation  string // exact, when redirect
		wantBodySub   string // substring, when 200
		wantHdrCTpart string
	}{
		{
			name:         "bare /swagger normalises to /swagger/ via gin's RedirectTrailingSlash",
			path:         "/swagger",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/swagger/",
		},
		{
			name:          "/swagger/ serves index.html via file server",
			path:          "/swagger/",
			wantStatus:    http.StatusOK,
			wantBodySub:   "<html",
			wantHdrCTpart: "text/html",
		},
		{
			name:          "swagger-initializer.js is intercepted with pinned spec URL",
			path:          "/swagger/swagger-initializer.js",
			wantStatus:    http.StatusOK,
			wantBodySub:   `url: "/openapi.json"`,
			wantHdrCTpart: "application/javascript",
		},
		{
			name:          "static asset (.css) is served by file server",
			path:          "/swagger/swagger-ui.css",
			wantStatus:    http.StatusOK,
			wantHdrCTpart: "text/css",
		},
		{
			name:         "/swagger/index.html canonicalises to /swagger/ (FileServer behavior, no loop)",
			path:         "/swagger/index.html",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "./",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.ServeHTTP(w, req)
			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())
			if tc.wantLocation != "" {
				assert.Equal(t, tc.wantLocation, w.Header().Get("Location"))
			}
			if tc.wantBodySub != "" {
				assert.Contains(t, w.Body.String(), tc.wantBodySub)
			}
			if tc.wantHdrCTpart != "" {
				assert.Contains(t, w.Header().Get("Content-Type"), tc.wantHdrCTpart)
			}
		})
	}
}
