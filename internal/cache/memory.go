package cache

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// memoryEntry is an internal cache node stored in the LRU doubly-linked list.
type memoryEntry struct {
	key       string
	value     *CachedResponse
	expiresAt time.Time
}

// MemoryCache is a thread-safe in-process LRU cache with configurable maximum
// entry count. When the cache is full, the least-recently-used (LRU) entry is
// evicted to make room for new entries.
//
// MemoryCache does not require any external dependencies and is suitable for:
//   - Single-instance proxy deployments
//   - Unit and integration tests (no Redis required)
//
// For distributed deployments where multiple proxy instances share a cache,
// use RedisCache instead.
type MemoryCache struct {
	mu      sync.Mutex
	maxSize int
	items   map[string]*list.Element // key -> list node
	lru     *list.List               // front = most recently used

	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// NewMemoryCache creates an in-memory LRU cache that holds at most maxSize entries.
// If maxSize <= 0, a default of 1000 entries is used.
func NewMemoryCache(maxSize int) *MemoryCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &MemoryCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element, maxSize),
		lru:     list.New(),
	}
}

// Get retrieves a cached response for the given key.
// If the entry has expired, it is removed and a miss is returned.
func (c *MemoryCache) Get(key string) (*CachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	entry := elem.Value.(*memoryEntry)

	// Check TTL expiry
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		c.lru.Remove(elem)
		delete(c.items, key)
		c.misses.Add(1)
		return nil, false
	}

	// Move to front (most recently used)
	c.lru.MoveToFront(elem)
	c.hits.Add(1)
	return entry.value, true
}

// Set adds or updates an entry in the cache.
// If the cache is at capacity, the LRU entry is evicted first.
func (c *MemoryCache) Set(key string, resp *CachedResponse, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If key already exists, update it and move to front
	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*memoryEntry)
		entry.value = resp
		if ttl > 0 {
			entry.expiresAt = time.Now().Add(ttl)
		} else {
			entry.expiresAt = time.Time{}
		}
		c.lru.MoveToFront(elem)
		return nil
	}

	// Evict LRU entry if at capacity
	if c.lru.Len() >= c.maxSize {
		c.evictOldestLocked()
	}

	entry := &memoryEntry{
		key:   key,
		value: resp,
	}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	elem := c.lru.PushFront(entry)
	c.items[key] = elem
	return nil
}

// Delete removes an entry from the cache by key.
func (c *MemoryCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.lru.Remove(elem)
		delete(c.items, key)
	}
	return nil
}

// Flush removes all entries from the cache.
func (c *MemoryCache) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element, c.maxSize)
	c.lru.Init()
	return nil
}

// Stats returns a snapshot of cache metrics.
func (c *MemoryCache) Stats() Stats {
	c.mu.Lock()
	size := int64(c.lru.Len())
	c.mu.Unlock()

	return Stats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Size:      size,
	}
}

// evictOldestLocked removes the LRU (least-recently-used = back of list) entry.
// Must be called with c.mu held.
func (c *MemoryCache) evictOldestLocked() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*memoryEntry)
	c.lru.Remove(elem)
	delete(c.items, entry.key)
	c.evictions.Add(1)
}
