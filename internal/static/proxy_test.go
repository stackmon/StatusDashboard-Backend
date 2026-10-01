package static

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
)

const (
	// testFrontHost is the hostname the frontend ingress serves.
	testFrontHost = "test.status.otc-service.com"
	testTTL       = time.Minute
	testMaxBytes  = 1 << 20
)

// fakeOrigin stands in for an OBS website endpoint: it counts its requests and
// remembers the Host header they carried.
type fakeOrigin struct {
	server *httptest.Server
	hits   atomic.Int64

	mu       sync.Mutex
	lastHost string
}

func newFakeOrigin(t *testing.T, handler http.HandlerFunc) *fakeOrigin {
	t.Helper()

	origin := &fakeOrigin{}
	origin.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.hits.Add(1)

		origin.mu.Lock()
		origin.lastHost = r.Host
		origin.mu.Unlock()

		handler(w, r)
	}))
	t.Cleanup(origin.server.Close)

	return origin
}

// endpoint is the bare host:port the proxy is configured with.
func (o *fakeOrigin) endpoint() string {
	return strings.TrimPrefix(o.server.URL, "https://")
}

func (o *fakeOrigin) hostSeen() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.lastHost
}

// newTestRouter wires the proxy the way api.New does, over an engine that also
// carries an API route.
func newTestRouter(t *testing.T, origins ...*fakeOrigin) (*gin.Engine, *Proxy) {
	t.Helper()

	endpoints := make([]string, 0, len(origins))
	for _, origin := range origins {
		endpoints = append(endpoints, origin.endpoint())
	}

	proxy := newProxy(Options{
		Origins:  endpoints,
		TTL:      testTTL,
		MaxBytes: testMaxBytes,
		Logger:   zaptest.NewLogger(t),
	})
	proxy.client = &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/v2/components", func(c *gin.Context) { c.String(http.StatusOK, "api response") })
	router.NoRoute(proxy.handler())

	return router, proxy
}

func send(router http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = testFrontHost

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

func request(t *testing.T, router http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	return send(router, method, target)
}

func TestProxyCachesOriginResponse(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("ETag", `"v1"`)
		_, _ = io.WriteString(w, "<html>site</html>")
	})

	router, proxy := newTestRouter(t, origin)

	first := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, cacheMiss, first.Header().Get(cacheHeader))
	assert.Equal(t, "<html>site</html>", first.Body.String())
	assert.Equal(t, 1, proxy.Cache().Len())
	assert.Positive(t, proxy.Cache().Bytes())

	second := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, cacheHit, second.Header().Get(cacheHeader))
	assert.Equal(t, "<html>site</html>", second.Body.String())
	assert.Equal(t, "text/html", second.Header().Get("Content-Type"))
	assert.Equal(t, `"v1"`, second.Header().Get("ETag"))
	assert.Equal(t, int64(1), origin.hits.Load(), "a cache hit must not reach the origin")

	// The cached response is served while the origin is unreachable.
	origin.server.Close()

	offline := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, offline.Code)
	assert.Equal(t, cacheHit, offline.Header().Get(cacheHeader))
	assert.Equal(t, int64(1), origin.hits.Load())
}

func TestProxySendsOriginAsHost(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "asset")
	})

	router, _ := newTestRouter(t, origin)

	w := request(t, router, http.MethodGet, "/assets/app.js?v=2")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "asset", w.Body.String())

	// OBS picks the bucket from the Host header, so the frontend hostname must
	// not leak into the origin request.
	assert.Equal(t, origin.endpoint(), origin.hostSeen())
	assert.NotEqual(t, testFrontHost, origin.hostSeen())
}

func TestProxyDoesNotCacheNoStore(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "dynamic")
	})

	router, proxy := newTestRouter(t, origin)

	for range 2 {
		w := request(t, router, http.MethodGet, "/index.html")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, cacheMiss, w.Header().Get(cacheHeader))
	}

	assert.Equal(t, int64(2), origin.hits.Load())
	assert.Zero(t, proxy.Cache().Len())
}

func TestProxyDoesNotCacheOriginErrors(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such object", http.StatusNotFound)
	})

	router, proxy := newTestRouter(t, origin)

	for range 2 {
		w := request(t, router, http.MethodGet, "/missing.html")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, cacheMiss, w.Header().Get(cacheHeader))
	}

	assert.Equal(t, int64(2), origin.hits.Load())
	assert.Zero(t, proxy.Cache().Len())
}

func TestProxyKeepsQueryInTheCacheKey(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.RawQuery)
	})

	router, _ := newTestRouter(t, origin)

	assert.Equal(t, "v=1", request(t, router, http.MethodGet, "/app.js?v=1").Body.String())
	assert.Equal(t, "v=2", request(t, router, http.MethodGet, "/app.js?v=2").Body.String())
	assert.Equal(t, "v=1", request(t, router, http.MethodGet, "/app.js?v=1").Body.String())

	assert.Equal(t, int64(2), origin.hits.Load())
}

func TestAPIRoutesBypassTheCache(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "served by the origin")
	})

	router, proxy := newTestRouter(t, origin)

	for range 2 {
		w := request(t, router, http.MethodGet, "/v2/components")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "api response", w.Body.String())
		assert.Empty(t, w.Header().Get(cacheHeader))
	}

	assert.Zero(t, origin.hits.Load())
	assert.Zero(t, proxy.Cache().Len())
}

func TestProxyServesStaleWhenOriginsFail(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=0")
		_, _ = io.WriteString(w, "<html>cached</html>")
	})

	router, _ := newTestRouter(t, origin)

	first := request(t, router, http.MethodGet, "/index.html")
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, cacheMiss, first.Header().Get(cacheHeader))

	// max-age=0 expires the entry right away, so the next request goes back to
	// the origin; with the origin gone the expired entry is served instead.
	origin.server.Close()

	stale := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, stale.Code)
	assert.Equal(t, cacheStale, stale.Header().Get(cacheHeader))
	assert.Equal(t, "<html>cached</html>", stale.Body.String())
}

func TestProxyServesStaleToWaiterThatGivesUp(t *testing.T) {
	var block atomic.Bool

	release := make(chan struct{})
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=0")
		if block.Load() {
			<-release
		}
		_, _ = io.WriteString(w, "<html>cached</html>")
	})

	router, proxy := newTestRouter(t, origin)

	primed := request(t, router, http.MethodGet, "/index.html")
	require.Equal(t, http.StatusOK, primed.Code)
	require.Equal(t, 1, proxy.Cache().Len())

	block.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil).WithContext(ctx)
	req.Host = testFrontHost

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, cacheStale, w.Header().Get(cacheHeader))
	assert.Equal(t, "<html>cached</html>", w.Body.String())

	// Let the abandoned fetch finish before the test logger goes away.
	close(release)

	settled := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, settled.Code)
}

func TestProxyReturnsBadGatewayWithoutCache(t *testing.T) {
	origin := newFakeOrigin(t, func(_ http.ResponseWriter, _ *http.Request) {})
	router, _ := newTestRouter(t, origin)

	origin.server.Close()

	w := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Empty(t, w.Header().Get(cacheHeader))
}

func TestProxyFailsOverToTheNextOrigin(t *testing.T) {
	unreachable := newFakeOrigin(t, func(_ http.ResponseWriter, _ *http.Request) {})
	unreachable.server.Close()

	live := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>backup</html>")
	})

	router, proxy := newTestRouter(t, unreachable, live)

	w := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<html>backup</html>", w.Body.String())
	assert.Equal(t, cacheMiss, w.Header().Get(cacheHeader))
	assert.Equal(t, int64(1), live.hits.Load())
	assert.Equal(t, 1, proxy.Cache().Len())
}

func TestProxyFailsOverOnOriginError(t *testing.T) {
	failing := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	live := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>backup</html>")
	})

	router, _ := newTestRouter(t, failing, live)

	w := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<html>backup</html>", w.Body.String())
	assert.Equal(t, int64(1), failing.hits.Load())
	assert.Equal(t, int64(1), live.hits.Load())
}

func TestProxyHeadIsAnsweredFromTheCachedGet(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "<html>site</html>")
	})

	router, proxy := newTestRouter(t, origin)

	cached := request(t, router, http.MethodGet, "/index.html")
	require.Equal(t, http.StatusOK, cached.Code)
	require.Equal(t, cacheMiss, cached.Header().Get(cacheHeader))

	w := request(t, router, http.MethodHead, "/index.html")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, cacheHit, w.Header().Get(cacheHeader))
	assert.Equal(t, cached.Header().Get("Content-Type"), w.Header().Get("Content-Type"))
	assert.Equal(t, cached.Header().Get("Content-Length"), w.Header().Get("Content-Length"))
	assert.Empty(t, w.Body.String())

	assert.Equal(t, int64(1), origin.hits.Load())
	assert.Equal(t, 1, proxy.Cache().Len())
}

// A response marked no-cache must not be reused without validation, but it is
// still worth keeping: index.html is served that way, and the stored copy is
// what keeps the site reachable while no origin answers.
func TestProxyServesNoCacheEntryOnlyAsStale(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, no-cache, must-revalidate")
		_, _ = io.WriteString(w, "<html>index</html>")
	})

	router, proxy := newTestRouter(t, origin)

	first := request(t, router, http.MethodGet, "/index.html")
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, cacheMiss, first.Header().Get(cacheHeader))
	require.Equal(t, 1, proxy.Cache().Len())

	again := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, cacheMiss, again.Header().Get(cacheHeader))
	assert.Equal(t, int64(2), origin.hits.Load())

	origin.server.Close()

	stale := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusOK, stale.Code)
	assert.Equal(t, cacheStale, stale.Header().Get(cacheHeader))
	assert.Equal(t, "<html>index</html>", stale.Body.String())

	head := request(t, router, http.MethodHead, "/index.html")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Equal(t, cacheStale, head.Header().Get(cacheHeader))
	assert.Empty(t, head.Body.String())
}

// no-store forbids keeping the response at all, so an unreachable origin has
// nothing to fall back to.
func TestProxyDoesNotKeepNoStoreResponse(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "<html>volatile</html>")
	})

	router, proxy := newTestRouter(t, origin)

	w := request(t, router, http.MethodGet, "/index.html")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Zero(t, proxy.Cache().Len())

	origin.server.Close()

	gone := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusBadGateway, gone.Code)
}

// Streaming skips the cache buffer, but it must not skip the header filtering
// the buffered path applies: the origin's own metadata stays between the proxy
// and OBS.
func TestProxyStreamedResponseHidesOriginHeaders(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("X-Amz-Request-Id", "REQ123")
		w.Header().Set("Server", "obs")
		w.WriteHeader(http.StatusOK)
	})

	router, _ := newTestRouter(t, origin)

	w := request(t, router, http.MethodHead, "/index.html")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, cacheMiss, w.Header().Get(cacheHeader))
	assert.Equal(t, "text/html", w.Header().Get("Content-Type"))
	assert.Equal(t, "max-age=60", w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Header().Get("X-Amz-Request-Id"))
	assert.Empty(t, w.Header().Get("Server"))
}

func TestProxyRejectsUnsupportedMethods(t *testing.T) {
	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "served by the origin")
	})

	router, _ := newTestRouter(t, origin)

	w := request(t, router, http.MethodPost, "/index.html")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.JSONEq(t, `{"errMsg":"page not found"}`, w.Body.String())
	assert.Zero(t, origin.hits.Load())
}

func TestProxyFetchesMissingKeyOnce(t *testing.T) {
	var starting sync.Once

	started := make(chan struct{})
	release := make(chan struct{})

	origin := newFakeOrigin(t, func(w http.ResponseWriter, _ *http.Request) {
		starting.Do(func() { close(started) })
		<-release
		_, _ = io.WriteString(w, "<html>slow</html>")
	})

	router, _ := newTestRouter(t, origin)

	const callers = 8

	recorders := make([]*httptest.ResponseRecorder, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			recorders[i] = send(router, http.MethodGet, "/index.html")
		}()
	}

	<-started

	// Give the remaining callers time to join the in-flight fetch before it is
	// allowed to answer.
	time.Sleep(100 * time.Millisecond)

	close(release)
	wg.Wait()

	for _, w := range recorders {
		require.NotNil(t, w)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "<html>slow</html>", w.Body.String())
	}

	assert.Equal(t, int64(1), origin.hits.Load(), "concurrent callers must share one origin fetch")
}

func TestNewHandlerWithoutOriginsKeepsNotFound(t *testing.T) {
	handler, err := NewHandler(conf.Static{}, zaptest.NewLogger(t))
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.NoRoute(handler)

	w := request(t, router, http.MethodGet, "/index.html")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.JSONEq(t, `{"errMsg":"page not found"}`, w.Body.String())
}

func TestNewHandlerRejectsUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		cfg       conf.Static
		errSubstr string
	}{
		{
			name:      "origin with a scheme",
			cfg:       conf.Static{Origins: "https://bucket.example.com", CacheTTL: "5m", CacheMaxBytes: "1024"},
			errSubstr: "SD_STATIC_ORIGINS",
		},
		{
			name:      "malformed duration",
			cfg:       conf.Static{Origins: "bucket.example.com", CacheTTL: "5 minutes", CacheMaxBytes: "1024"},
			errSubstr: "SD_STATIC_CACHE_TTL",
		},
		{
			name:      "non-positive byte budget",
			cfg:       conf.Static{Origins: "bucket.example.com", CacheTTL: "5m", CacheMaxBytes: "0"},
			errSubstr: "SD_STATIC_CACHE_MAX_BYTES",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := NewHandler(tc.cfg, zaptest.NewLogger(t))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSubstr)
			assert.Nil(t, handler)
		})
	}
}
