package mobiledetect

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryCacheGetSet(t *testing.T) {
	c := NewMemoryCache(0)
	if c.MaxEntries() != DefaultMaxEntries {
		t.Fatalf("MaxEntries = %d, want %d", c.MaxEntries(), DefaultMaxEntries)
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatal("missing key should miss")
	}
	c.Set("k", "v", time.Minute)
	got, ok := c.Get("k")
	if !ok || got != "v" {
		t.Fatalf("Get(k) = %v (%v)", got, ok)
	}
	if !c.Has("k") {
		t.Fatal("Has(k) should be true")
	}
	c.Delete("k")
	if c.Has("k") {
		t.Fatal("Has(k) should be false after Delete")
	}
}

func TestMemoryCacheOverwrite(t *testing.T) {
	c := NewMemoryCache(2)
	c.Set("a", 1, time.Minute)
	c.Set("a", 2, time.Minute)
	if got, _ := c.Get("a"); got != 2 {
		t.Fatalf("overwrite failed: got %v", got)
	}
	// Overwriting should not grow the order slice or trigger eviction.
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestMemoryCacheFIFOEviction(t *testing.T) {
	c := NewMemoryCache(3)
	for i, k := range []string{"a", "b", "c", "d"} {
		c.Set(k, i, time.Minute)
	}
	// After inserting "d" beyond the cap of 3, the oldest ("a") is evicted.
	if c.Has("a") {
		t.Fatal("expected oldest key 'a' to be evicted")
	}
	for _, k := range []string{"b", "c", "d"} {
		if !c.Has(k) {
			t.Fatalf("expected key %q to remain", k)
		}
	}
	keys := c.Keys()
	want := []string{"b", "c", "d"}
	if len(keys) != len(want) {
		t.Fatalf("Keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("Keys[%d] = %q, want %q (order matters)", i, keys[i], want[i])
		}
	}
}

func TestMemoryCacheOverwriteDoesNotEvict(t *testing.T) {
	// Mirrors PHP semantics exactly: overwriting an existing key keeps its
	// original insertion position (array_key_first still returns it), so on
	// overflow the FIRST-inserted key is evicted. With cap=2: set a, b,
	// overwrite a, set c -> count==2>=2 so the oldest key ("a") is evicted.
	c := NewMemoryCache(2)
	c.Set("a", 1, time.Minute)
	c.Set("b", 2, time.Minute)
	c.Set("a", 11, time.Minute) // overwrite; position unchanged, stays oldest
	c.Set("c", 3, time.Minute)  // overflow -> evict "a" (array_key_first)
	if c.Has("a") {
		t.Fatal("'a' (oldest, per PHP array_key_first) should be evicted")
	}
	if !c.Has("b") {
		t.Fatal("'b' should survive")
	}
	if !c.Has("c") {
		t.Fatal("'c' should survive")
	}
}

func TestMemoryCacheTTLExpiry(t *testing.T) {
	c := NewMemoryCache(0)
	now := time.Now()
	clock := &fakeClock{t: now}
	c.now = clock.now

	c.Set("short", "v", 10*time.Millisecond)
	if _, ok := c.Get("short"); !ok {
		t.Fatal("should be present immediately")
	}

	clock.advance(20 * time.Millisecond)
	if _, ok := c.Get("short"); ok {
		t.Fatal("should be expired after advancing the clock")
	}
}

func TestMemoryCacheZeroTTLRemoves(t *testing.T) {
	c := NewMemoryCache(0)
	c.Set("k", "v", time.Minute)
	c.Set("k", "v2", 0) // non-positive TTL => remove, per PSR-16
	if c.Has("k") {
		t.Fatal("non-positive TTL should remove the entry")
	}
}

func TestMemoryCacheNegativeTTLRemoves(t *testing.T) {
	c := NewMemoryCache(0)
	c.Set("k", "v", time.Minute)
	c.Set("k", "v2", -1*time.Second)
	if c.Has("k") {
		t.Fatal("negative TTL should remove the entry")
	}
}

func TestMemoryCacheEvictExpired(t *testing.T) {
	c := NewMemoryCache(0)
	now := time.Now()
	clock := &fakeClock{t: now}
	c.now = clock.now

	c.Set("a", 1, time.Minute)
	c.Set("b", 2, 10*time.Millisecond)
	c.Set("c", 3, 0) // not stored (non-positive ttl)

	clock.advance(20 * time.Millisecond)
	evicted := c.EvictExpired()
	if evicted != 1 {
		t.Fatalf("EvictExpired = %d, want 1", evicted)
	}
	if !c.Has("a") {
		t.Fatal("'a' (future expiry) should survive")
	}
}

func TestMemoryCacheClear(t *testing.T) {
	c := NewMemoryCache(0)
	c.Set("a", 1, time.Minute)
	c.Set("b", 2, time.Minute)
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len = %d after Clear", c.Len())
	}
}

func TestMemoryCacheConcurrent(t *testing.T) {
	c := NewMemoryCache(50)
	const goroutines = 20
	const ops = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for n := 0; n < ops; n++ {
				key := string(rune('A' + (id+n)%26))
				c.Set(key, n, time.Minute)
				_, _ = c.Get(key)
				c.Has(key)
			}
		}(g)
	}
	wg.Wait()
	// No data race / panic == pass. Race detector enabled via -race in CI.
}

func TestNoopCache(t *testing.T) {
	c := noopCache{}
	c.Set("k", "v", time.Minute)
	if _, ok := c.Get("k"); ok {
		t.Fatal("noop cache should always miss")
	}
	if c.Has("k") {
		t.Fatal("noop cache Has should be false")
	}
	c.Delete("k")
	c.Clear()
}

// fakeClock is an injectable clock for TTL tests.
type fakeClock struct {
	t time.Time
}

func (f *fakeClock) now() time.Time { return f.t }
func (f *fakeClock) advance(d time.Duration) {
	f.t = f.t.Add(d)
}
