package mobiledetect

import (
	"sync"
	"time"
)

// Cache is the contract for the per-instance result cache used by
// MobileDetect. It mirrors the subset of PSR-16 (Psr\SimpleCache\CacheInterface)
// that MobileDetect relies on.
//
// Implementations must be safe for concurrent use because a MobileDetect
// instance can be shared across goroutines in a long-running server.
type Cache interface {
	// Get returns the cached value for key, or (zero, false) on miss.
	Get(key string) (any, bool)
	// Set stores value under key with the given time-to-live. A non-positive
	// ttl means the entry is not cached (and an existing entry is removed).
	Set(key string, value any, ttl time.Duration)
	// Has reports whether a non-expired entry exists for key.
	Has(key string) bool
	// Delete removes the entry for key, if any.
	Delete(key string)
	// Clear removes all entries.
	Clear()
}

// DefaultMaxEntries is the in-memory cache bound. Mirrors Cache::DEFAULT_MAX_ENTRIES.
// It prevents unbounded growth in long-running Go servers that reuse one
// MobileDetect instance across many distinct User-Agents.
const DefaultMaxEntries = 1000

// MemoryCache is an in-memory Cache implementation with FIFO eviction and TTL
// support. It is the default cache used by MobileDetect.
//
// The zero value is NOT usable; use NewMemoryCache (or New which sets sensible
// defaults). All methods are safe for concurrent use.
type MemoryCache struct {
	mu         sync.Mutex
	entries    map[string]memEntry
	order      []string // FIFO insertion order, for eviction
	maxEntries int
	now        func() time.Time // injectable clock for testing
}

type memEntry struct {
	value   any
	expires time.Time // zero means no expiration
}

// NewMemoryCache returns a MemoryCache bounded to maxEntries. If maxEntries <= 0
// DefaultMaxEntries is used.
func NewMemoryCache(maxEntries int) *MemoryCache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &MemoryCache{
		entries:    make(map[string]memEntry),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

// Get returns the cached value for key, or (nil, false) on miss or expiry.
func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !e.expires.IsZero() && !c.now().Before(e.expires) {
		// Expired: evict lazily.
		c.deleteLocked(key)
		return nil, false
	}
	return e.value, true
}

// Set stores value under key with the given ttl. A non-positive ttl removes any
// existing entry (and does not store the new one), mirroring PSR-16 semantics.
// Inserting a brand-new key beyond the cap evicts the oldest entry first (FIFO);
// overwriting an existing key never triggers eviction.
func (c *MemoryCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl > 0 {
		if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
			c.evictOldestLocked()
		}
		e := memEntry{value: value}
		// Tests may inject a zero clock to create entries that never expire;
		// in that case leave e.expires as the zero value (no expiration).
		if !c.now().IsZero() {
			e.expires = c.now().Add(ttl)
		}
		if _, exists := c.entries[key]; !exists {
			c.order = append(c.order, key)
		}
		c.entries[key] = e
		return
	}

	// ttl <= 0: treat as already expired, remove if present (PSR-16).
	c.deleteLocked(key)
}

// Has reports whether a non-expired entry exists for key.
func (c *MemoryCache) Has(key string) bool {
	_, ok := c.Get(key)
	return ok
}

// Delete removes the entry for key, if any.
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(key)
}

// Clear removes all entries.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]memEntry)
	c.order = c.order[:0]
}

// Len returns the number of entries currently stored (including possibly
// expired ones not yet lazily evicted). Useful for tests.
func (c *MemoryCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Keys returns a copy of the cache keys in insertion order. Useful for tests.
func (c *MemoryCache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// MaxEntries returns the configured maximum number of cache entries.
func (c *MemoryCache) MaxEntries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxEntries
}

// EvictExpired removes all entries whose TTL has already elapsed and returns
// the number removed. Entries with no expiration or a future expiration are kept.
func (c *MemoryCache) EvictExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	evicted := 0
	for k, e := range c.entries {
		if !e.expires.IsZero() && !now.Before(e.expires) {
			c.deleteLocked(k)
			evicted++
		}
	}
	return evicted
}

func (c *MemoryCache) deleteLocked(key string) {
	if _, ok := c.entries[key]; !ok {
		return
	}
	delete(c.entries, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *MemoryCache) evictOldestLocked() {
	for len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		if _, ok := c.entries[oldest]; ok {
			delete(c.entries, oldest)
			return
		}
	}
}

// noopCache is a Cache that stores nothing. It is a zero-overhead option for
// callers who want to disable result caching (pass it to NewWithCache); the
// constructors default to MemoryCache, not this.
type noopCache struct{}

func (noopCache) Get(string) (any, bool)         { return nil, false }
func (noopCache) Set(string, any, time.Duration) {}
func (noopCache) Has(string) bool                { return false }
func (noopCache) Delete(string)                  {}
func (noopCache) Clear()                         {}
