// Package static serves the status dashboard static site from the OBS website
// endpoints for the paths the API does not own, with an in-memory cache in
// front of them.
package static

import (
	"container/list"
	"net/http"
	"sync"
	"time"
)

// entry is one origin response, either the result of a single fetch or a cached
// one. Entries are immutable once built, so the same pointer can be handed to
// several requests at once.
type entry struct {
	status   int
	header   http.Header
	body     []byte
	expires  time.Time
	storable bool
}

// size is the number of bytes the entry occupies, counted against the cache
// budget.
func (e *entry) size() int64 {
	size := int64(len(e.body))

	for name, values := range e.header {
		for _, value := range values {
			size += int64(len(name) + len(value))
		}
	}

	return size
}

// cacheItem ties an entry to its key so that eviction can drop both.
type cacheItem struct {
	key   string
	entry *entry
}

// Cache is a concurrency-safe LRU cache of origin responses, bounded by the
// total size of the bodies and headers it holds.
type Cache struct {
	mu       sync.Mutex
	maxBytes int64
	bytes    int64
	order    *list.List
	items    map[string]*list.Element
}

func newCache(maxBytes int64) *Cache {
	return &Cache{
		maxBytes: maxBytes,
		order:    list.New(),
		items:    make(map[string]*list.Element),
	}
}

// Len returns the number of stored responses, expired ones included.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.items)
}

// Bytes returns the current size of the stored responses.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.bytes
}

// get returns the stored response for key when it has not expired yet.
func (c *Cache) get(key string) (*entry, bool) {
	item, ok := c.touch(key)
	if !ok || !item.entry.expires.After(time.Now()) {
		return nil, false
	}

	return item.entry, true
}

// getStale returns the stored response for key regardless of its expiry. It
// backs the fallback served while every origin is unreachable.
func (c *Cache) getStale(key string) (*entry, bool) {
	item, ok := c.touch(key)
	if !ok {
		return nil, false
	}

	return item.entry, true
}

// touch marks the entry as recently used and returns it.
func (c *Cache) touch(key string) (*cacheItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[key]
	if !ok {
		return nil, false
	}

	item, ok := element.Value.(*cacheItem)
	if !ok {
		return nil, false
	}

	c.order.MoveToFront(element)

	return item, true
}

// put stores a response and evicts the least recently used ones until the cache
// fits its budget again. A response larger than the whole budget is not stored.
func (c *Cache) put(key string, e *entry) {
	size := e.size()
	if size > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.items[key]; ok {
		c.drop(element)
	}

	c.items[key] = c.order.PushFront(&cacheItem{key: key, entry: e})
	c.bytes += size

	for c.bytes > c.maxBytes {
		element := c.order.Back()
		if element == nil {
			return
		}

		c.drop(element)
	}
}

// drop removes an element from the LRU order and the index. The caller holds
// the lock.
func (c *Cache) drop(element *list.Element) {
	item, ok := element.Value.(*cacheItem)
	if !ok {
		c.order.Remove(element)

		return
	}

	c.bytes -= item.entry.size()
	delete(c.items, item.key)
	c.order.Remove(element)
}
