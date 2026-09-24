package static

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func entryWith(body string) *entry {
	return &entry{
		status:   http.StatusOK,
		header:   http.Header{},
		body:     []byte(body),
		expires:  time.Now().Add(time.Minute),
		storable: true,
	}
}

func TestCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newCache(10)

	cache.put("a", entryWith("aaaaa"))
	cache.put("b", entryWith("bbbbb"))

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, int64(10), cache.Bytes())

	cache.put("c", entryWith("ccccc"))

	assert.Equal(t, 2, cache.Len())
	assert.Equal(t, int64(10), cache.Bytes())

	_, ok := cache.get("a")
	assert.False(t, ok, "the least recently used entry is evicted")

	for _, key := range []string{"b", "c"} {
		_, ok = cache.get(key)
		assert.True(t, ok, "key %q survives", key)
	}
}

func TestCacheKeepsRecentlyUsedEntry(t *testing.T) {
	cache := newCache(10)

	cache.put("a", entryWith("aaaaa"))
	cache.put("b", entryWith("bbbbb"))

	_, ok := cache.get("a")
	assert.True(t, ok)

	cache.put("c", entryWith("ccccc"))

	_, ok = cache.get("b")
	assert.False(t, ok, "the entry not touched since is evicted")

	_, ok = cache.get("a")
	assert.True(t, ok)
}

func TestCacheRefusesEntryLargerThanItsBudget(t *testing.T) {
	cache := newCache(4)

	cache.put("a", entryWith("aaaaa"))

	assert.Zero(t, cache.Len())
	assert.Zero(t, cache.Bytes())
}

func TestCacheCountsHeadersAgainstTheBudget(t *testing.T) {
	cache := newCache(8)

	large := entryWith("aaaa")
	large.header = http.Header{"Etag": []string{"0123456789"}}

	cache.put("a", large)
	assert.Zero(t, cache.Len())

	cache.put("a", entryWith("aaaa"))
	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, int64(4), cache.Bytes())
}

func TestCacheGetRespectsExpiry(t *testing.T) {
	cache := newCache(64)

	expired := entryWith("body")
	expired.expires = time.Now().Add(-time.Second)

	cache.put("a", expired)

	_, ok := cache.get("a")
	assert.False(t, ok, "an expired entry is not served as a hit")

	stale, ok := cache.getStale("a")
	assert.True(t, ok, "an expired entry is still available for the stale fallback")
	assert.Equal(t, "body", string(stale.body))
}

func TestCacheTTLFromCacheControl(t *testing.T) {
	tests := []struct {
		name     string
		header   http.Header
		expected time.Duration
		storable bool
	}{
		{
			name:     "no header falls back",
			header:   http.Header{},
			expected: testTTL,
			storable: true,
		},
		{
			name:     "max-age wins over the fallback",
			header:   http.Header{"Cache-Control": []string{"max-age=60"}},
			expected: time.Minute,
			storable: true,
		},
		{
			name:     "s-maxage wins over max-age",
			header:   http.Header{"Cache-Control": []string{"max-age=60, s-maxage=30"}},
			expected: 30 * time.Second,
			storable: true,
		},
		{
			name:     "quoted max-age",
			header:   http.Header{"Cache-Control": []string{`max-age="120"`}},
			expected: 2 * time.Minute,
			storable: true,
		},
		{
			name:     "zero max-age expires at once",
			header:   http.Header{"Cache-Control": []string{"public, max-age=0"}},
			expected: 0,
			storable: true,
		},
		{
			name:     "no-store is never stored",
			header:   http.Header{"Cache-Control": []string{"no-store"}},
			storable: false,
		},
		{
			name:     "no-cache is never stored",
			header:   http.Header{"Cache-Control": []string{"public, no-cache"}},
			storable: false,
		},
		{
			name:     "malformed max-age falls back",
			header:   http.Header{"Cache-Control": []string{"max-age=abc"}},
			expected: testTTL,
			storable: true,
		},
		{
			name:     "negative max-age falls back",
			header:   http.Header{"Cache-Control": []string{"max-age=-5"}},
			expected: testTTL,
			storable: true,
		},
		{
			name:     "other directives are ignored",
			header:   http.Header{"Cache-Control": []string{"public, must-revalidate"}},
			expected: testTTL,
			storable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ttl, storable := cacheTTL(tc.header, testTTL)

			assert.Equal(t, tc.storable, storable)

			if tc.storable {
				assert.Equal(t, tc.expected, ttl)
			}
		})
	}
}

func TestResponseHeadersKeepOnlyThePassThroughSet(t *testing.T) {
	src := http.Header{
		"Content-Type":     []string{"text/html"},
		"Content-Length":   []string{"12"},
		"Cache-Control":    []string{"max-age=60"},
		"Etag":             []string{`"v1"`},
		"Last-Modified":    []string{"Wed, 01 Jan 2025 00:00:00 GMT"},
		"Content-Encoding": []string{"gzip"},
		"Connection":       []string{"keep-alive"},
		"X-Obs-Request-Id": []string{"request-1"},
	}

	got := responseHeaders(src)

	for name := range src {
		if passThroughHeader(name) {
			assert.Equal(t, src.Values(name), got.Values(name), "header %q is forwarded", name)

			continue
		}

		assert.Empty(t, got.Values(name), "header %q stays with the origin", name)
	}
}
