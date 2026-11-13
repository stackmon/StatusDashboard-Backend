package baseurl

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func requestWith(host, proto string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/rss/?mt=Global", nil)
	req.Host = host

	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}

	return req
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		proto      string
		configured string
		scheme     string
		expectHost string
		path       string
	}{
		{
			name:       "no forwarded proto falls back to https",
			scheme:     "https",
			expectHost: "status.example.com",
		},
		{
			name:       "forwarded proto provides the scheme",
			proto:      "http",
			scheme:     "http",
			expectHost: "status.example.com",
		},
		{
			name:       "the first entry of a proxy chain is used",
			proto:      "https, http",
			scheme:     "https",
			expectHost: "status.example.com",
		},
		{
			name:       "an unknown scheme is ignored",
			proto:      "javascript",
			scheme:     "https",
			expectHost: "status.example.com",
		},
		{
			name:       "a malformed host is not trusted with the header",
			host:       "status example.com",
			proto:      "http",
			scheme:     "https",
			expectHost: "status example.com",
		},
		{
			name:       "the configured URL wins over the fallback",
			configured: "https://public.example.com",
			scheme:     "https",
			expectHost: "public.example.com",
		},
		{
			name:       "a configured URL with a path prefix is kept",
			configured: "https://public.example.com/status",
			scheme:     "https",
			expectHost: "public.example.com",
			path:       "/status",
		},
		{
			name:       "an unusable configured URL is ignored",
			configured: "public.example.com",
			scheme:     "https",
			expectHost: "status.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			if host == "" {
				host = "status.example.com"
			}

			u := Resolve(requestWith(host, tc.proto), tc.configured)

			assert.Equal(t, tc.scheme, u.Scheme)
			assert.Equal(t, tc.expectHost, u.Host)
			assert.Equal(t, tc.path, u.Path)
		})
	}
}

func TestSelfKeepsTheQuery(t *testing.T) {
	req := requestWith("status.example.com", "https")

	assert.Equal(t, "https://status.example.com/rss/?mt=Global", Self(Resolve(req, ""), req))
}

func TestLinkAppendsToThePathPrefix(t *testing.T) {
	base := Resolve(requestWith("status.example.com", ""), "https://public.example.com/status")

	assert.Equal(t, "https://public.example.com/status/incidents/42", Link(base, "/incidents/42"))
}
