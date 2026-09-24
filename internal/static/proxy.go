package static

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
)

const (
	// maxObjectBytes caps the body the proxy buffers, and with it the largest
	// object the cache can hold. Larger objects are streamed through without
	// being cached.
	maxObjectBytes = 8 << 20

	// Timeouts of the nginx origin shim this proxy replaces.
	connectTimeout  = 3 * time.Second
	responseTimeout = 10 * time.Second
	// idleConnTimeout keeps connections to OBS reusable between requests.
	idleConnTimeout = 90 * time.Second

	cacheHeader = "X-Cache"

	// Values of X-Cache: served from the cache, fetched from an origin, served
	// stale while no origin answered, or not looked up at all.
	cacheHit    = "HIT"
	cacheMiss   = "MISS"
	cacheStale  = "STALE"
	cacheBypass = "BYPASS"
)

var (
	// errOriginUnavailable reports that no origin served the request.
	errOriginUnavailable = errors.New("no static origin served the request")
	// errObjectTooLarge reports a response too large to buffer, which is served
	// straight from the origin instead of through the cache.
	errObjectTooLarge = errors.New("static object exceeds the buffering limit")
)

// Options configures an origin proxy.
type Options struct {
	// Origins are the OBS website endpoints in failover order, host only.
	Origins []string
	// TTL applies to responses that carry no usable Cache-Control.
	TTL time.Duration
	// MaxBytes bounds the whole response cache.
	MaxBytes int64
	// Logger receives the failover and cache decisions.
	Logger *zap.Logger
}

// Proxy serves the static site from the OBS website endpoints, buffering the
// responses in front of them.
type Proxy struct {
	origins []string
	ttl     time.Duration
	log     *zap.Logger
	client  *http.Client
	cache   *Cache
	group   singleflight.Group
}

// NewHandler returns the catch-all handler for the router: a proxy as soon as
// one origin is configured, the plain 404 handler otherwise.
func NewHandler(cfg conf.Static, log *zap.Logger) (gin.HandlerFunc, error) {
	origins, err := cfg.OriginList()
	if err != nil {
		return nil, err
	}

	if len(origins) == 0 {
		return apiErrors.Return404, nil
	}

	ttl, err := cfg.TTL()
	if err != nil {
		return nil, err
	}

	maxBytes, err := cfg.MaxBytes()
	if err != nil {
		return nil, err
	}

	proxy := newProxy(Options{Origins: origins, TTL: ttl, MaxBytes: maxBytes, Logger: log})

	return proxy.handler(), nil
}

func newProxy(opts Options) *Proxy {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: connectTimeout}).DialContext,
		TLSHandshakeTimeout: connectTimeout,
		IdleConnTimeout:     idleConnTimeout,
	}

	return &Proxy{
		origins: opts.Origins,
		ttl:     opts.TTL,
		log:     opts.Logger,
		client:  &http.Client{Transport: transport},
		cache:   newCache(opts.MaxBytes),
	}
}

// Cache exposes the response cache for inspection.
func (p *Proxy) Cache() *Cache {
	return p.cache
}

// handler proxies GET and HEAD. Any other method on an unknown path keeps the
// API's 404 rather than sending writes to a static bucket.
func (p *Proxy) handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet:
			p.serveGet(c)
		case http.MethodHead:
			p.serveHead(c)
		default:
			apiErrors.Return404(c)
		}
	}
}

func (p *Proxy) serveGet(c *gin.Context) {
	key := cacheKey(c.Request)

	if e, ok := p.cache.get(key); ok {
		writeEntry(c, e, cacheHit)

		return
	}

	// The shared fetch outlives the request that starts it, otherwise a
	// disconnected caller would cancel the response every waiter is blocked on.
	ctx := context.WithoutCancel(c.Request.Context())

	results := p.group.DoChan(key, func() (any, error) {
		e, err := p.fetch(ctx, c.Request)
		if err != nil {
			return nil, err
		}

		if e.storable {
			p.cache.put(key, e)
		}

		return e, nil
	})

	select {
	case result := <-results:
		p.serveResult(c, key, result)
	case <-c.Request.Context().Done():
		// The caller gave up before the shared fetch returned; the cache is the
		// only thing left that can answer it.
		p.serveUnavailable(c, key)
	}
}

func (p *Proxy) serveResult(c *gin.Context, key string, result singleflight.Result) {
	if result.Err != nil {
		if errors.Is(result.Err, errObjectTooLarge) {
			p.serveStream(context.WithoutCancel(c.Request.Context()), c, key, cacheMiss)

			return
		}

		p.serveUnavailable(c, key)

		return
	}

	e, ok := result.Val.(*entry)
	if !ok {
		p.serveUnavailable(c, key)

		return
	}

	writeEntry(c, e, cacheMiss)
}

// serveHead keeps HEAD out of the cache: the origin body is empty, so a direct
// pass-through costs a round trip and nothing else.
func (p *Proxy) serveHead(c *gin.Context) {
	p.serveStream(context.WithoutCancel(c.Request.Context()), c, "", cacheBypass)
}

// serveStream copies a response through without buffering it, walking the
// origins in order.
func (p *Proxy) serveStream(ctx context.Context, c *gin.Context, key, cacheStatus string) {
	var failure error

	for _, origin := range p.origins {
		resp, err := p.request(ctx, origin, c.Request)
		if err != nil {
			failure = err
			p.log.Warn("static origin unavailable", zap.String("origin", origin), zap.Error(err))

			continue
		}

		if failedStatus(resp.StatusCode) {
			failure = fmt.Errorf("origin %s returned %s", origin, resp.Status)
			_ = resp.Body.Close()
			p.log.Warn("static origin failed", zap.String("origin", origin), zap.Int("status", resp.StatusCode))

			continue
		}

		copyHeaders(c.Writer.Header(), responseHeaders(resp.Header))
		c.Writer.Header().Set(cacheHeader, cacheStatus)
		c.Writer.WriteHeader(resp.StatusCode)

		_, err = io.Copy(c.Writer, resp.Body)
		_ = resp.Body.Close()

		if err != nil {
			p.log.Warn("static response interrupted", zap.String("path", c.Request.URL.Path), zap.Error(err))
		}

		p.log.Info("static origin served", zap.String("origin", origin), zap.Int("status", resp.StatusCode))

		return
	}

	p.log.Warn("all static origins failed", zap.String("path", c.Request.URL.Path), zap.Error(failure))
	p.serveUnavailable(c, key)
}

// serveUnavailable answers a request that no origin could serve. A cached entry
// is preferred over an error: a fresh one is a hit, an expired one the stale
// fallback, and only an empty cache yields 502.
func (p *Proxy) serveUnavailable(c *gin.Context, key string) {
	if key != "" {
		if e, ok := p.cache.get(key); ok {
			writeEntry(c, e, cacheHit)

			return
		}

		if e, ok := p.cache.getStale(key); ok {
			p.log.Warn("serving stale static content", zap.String("path", c.Request.URL.Path))
			writeEntry(c, e, cacheStale)

			return
		}
	}

	c.String(http.StatusBadGateway, "static origin unavailable")
}

// fetch walks the origins in order and returns the first usable response,
// buffered and ready for the cache.
func (p *Proxy) fetch(ctx context.Context, req *http.Request) (*entry, error) {
	var failure error

	for _, origin := range p.origins {
		resp, err := p.request(ctx, origin, req)
		if err != nil {
			failure = err
			p.log.Warn("static origin unavailable", zap.String("origin", origin), zap.Error(err))

			continue
		}

		if failedStatus(resp.StatusCode) {
			failure = fmt.Errorf("origin %s returned %s", origin, resp.Status)
			_ = resp.Body.Close()
			p.log.Warn("static origin failed", zap.String("origin", origin), zap.Int("status", resp.StatusCode))

			continue
		}

		e, err := p.buffer(resp)
		if err != nil {
			if errors.Is(err, errObjectTooLarge) {
				return nil, err
			}

			failure = err
			p.log.Warn("static origin response unreadable", zap.String("origin", origin), zap.Error(err))

			continue
		}

		p.log.Info("static origin served", zap.String("origin", origin), zap.Int("status", resp.StatusCode))

		return e, nil
	}

	p.log.Warn("all static origins failed", zap.String("path", req.URL.Path), zap.Error(failure))

	return nil, errors.Join(errOriginUnavailable, failure)
}

// buffer reads a response into memory and decides how long it may be cached.
// Bodies past maxObjectBytes are reported as errObjectTooLarge so that the
// caller can stream them instead.
func (p *Proxy) buffer(resp *http.Response) (*entry, error) {
	if resp.ContentLength > maxObjectBytes {
		_ = resp.Body.Close()

		return nil, errObjectTooLarge
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxObjectBytes+1))
	closeErr := resp.Body.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, closeErr
	}

	if len(body) > maxObjectBytes {
		return nil, errObjectTooLarge
	}

	e := &entry{
		status: resp.StatusCode,
		header: responseHeaders(resp.Header),
		body:   body,
	}

	// Only the site content is cached: an error document or a redirect is not
	// worth holding on to, and stale fallback must never resurrect one.
	if resp.StatusCode == http.StatusOK {
		if ttl, ok := cacheTTL(resp.Header, p.ttl); ok {
			e.storable = true
			e.expires = time.Now().Add(ttl)
		}
	}

	return e, nil
}

// request performs one round trip to an origin. OBS selects the bucket from the
// Host header alone and ignores SNI, so Host stays the bare origin hostname
// while the TLS handshake uses the same name.
//
//nolint:gosec // the origin is operator-configured; the request path cannot move the request to another host
func (p *Proxy) request(ctx context.Context, origin string, req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, responseTimeout)

	out, err := http.NewRequestWithContext(ctx, req.Method, "https://"+origin+req.URL.RequestURI(), nil)
	if err != nil {
		cancel()

		return nil, err
	}

	out.Host = origin

	resp, err := p.client.Do(out)
	if err != nil {
		cancel()

		return nil, err
	}

	// The read timeout covers the body, so the deadline is released only once
	// the caller is done with it.
	resp.Body = &deadlineBody{ReadCloser: resp.Body, cancel: cancel}

	return resp, nil
}

// deadlineBody holds the response deadline until the body is closed.
type deadlineBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *deadlineBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()

	return err
}

// cacheKey identifies a response: the same path is served for several hostnames,
// and their content differs.
func cacheKey(req *http.Request) string {
	return req.Method + " " + req.Host + req.URL.Path + "?" + req.URL.RawQuery
}

// failedStatus reports the origin statuses that trigger the failover chain.
func failedStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusGatewayTimeout
}

// writeEntry sends a buffered response with its cache status.
func writeEntry(c *gin.Context, e *entry, cacheStatus string) {
	copyHeaders(c.Writer.Header(), e.header)
	c.Writer.Header().Set(cacheHeader, cacheStatus)
	c.Writer.WriteHeader(e.status)

	if c.Request.Method == http.MethodHead {
		return
	}

	_, _ = c.Writer.Write(e.body)
}

// cacheDirectives are the Cache-Control directives the proxy acts on. An age of
// -1 means the directive was absent or malformed.
type cacheDirectives struct {
	noStore bool
	noCache bool
	maxAge  int
	sMaxAge int
}

// cacheTTL returns the lifetime of a response and whether it may be stored.
func cacheTTL(header http.Header, fallback time.Duration) (time.Duration, bool) {
	directives := parseCacheControl(header)

	if directives.noStore || directives.noCache {
		return 0, false
	}

	switch {
	case directives.sMaxAge >= 0:
		return time.Duration(directives.sMaxAge) * time.Second, true
	case directives.maxAge >= 0:
		return time.Duration(directives.maxAge) * time.Second, true
	default:
		return fallback, true
	}
}

func parseCacheControl(header http.Header) cacheDirectives {
	directives := cacheDirectives{maxAge: -1, sMaxAge: -1}

	for _, value := range header.Values("Cache-Control") {
		for _, part := range strings.Split(value, ",") {
			name, argument, _ := strings.Cut(strings.TrimSpace(part), "=")
			argument = strings.Trim(argument, `"`)

			switch strings.ToLower(name) {
			case "no-store":
				directives.noStore = true
			case "no-cache":
				directives.noCache = true
			case "max-age":
				directives.maxAge = parseSeconds(argument, directives.maxAge)
			case "s-maxage":
				directives.sMaxAge = parseSeconds(argument, directives.sMaxAge)
			}
		}
	}

	return directives
}

// parseSeconds returns the non-negative second count in value, keeping the
// current setting when the directive is malformed.
func parseSeconds(value string, current int) int {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return current
	}

	return seconds
}

// passThroughHeader reports the origin response headers forwarded to the
// client. Hop-by-hop headers and the rest of the origin's metadata stay between
// the proxy and OBS.
func passThroughHeader(name string) bool {
	// Header names carry the canonical spelling Go produces, so ETag arrives
	// as "Etag".
	switch http.CanonicalHeaderKey(name) {
	case "Content-Type", "Content-Length", "Cache-Control", "Etag", "Last-Modified", "Content-Encoding":
		return true
	default:
		return false
	}
}

// responseHeaders extracts the headers worth caching and forwarding.
func responseHeaders(src http.Header) http.Header {
	header := make(http.Header, len(src))

	for name, values := range src {
		if !passThroughHeader(name) {
			continue
		}

		header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}

	return header
}

// copyHeaders appends headers to dst.
func copyHeaders(dst, src http.Header) {
	for name, values := range src {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}
