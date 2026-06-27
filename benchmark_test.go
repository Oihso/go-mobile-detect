package mobiledetect

import (
	"sync"
	"testing"
)

// BenchmarkIsMobile measures the cost of a single cold IsMobile+IsTablet
// detection across a representative spread of real User-Agents. The cache is
// shared, so repeated identical UAs hit the cache; the benchmark rotates
// through a set so each is effectively computed once.
func BenchmarkIsMobile(b *testing.B) {
	uas := []string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (Linux; Android 11; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.120 Mobile Safari/537.36",
		"Mozilla/5.0 (Linux; Android 11; SM-T530 Build/KOT49H) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.2311.111 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.0 Safari/605.1.15",
	}
	d := New()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ua := uas[i%len(uas)]
		d.SetUserAgent(ua)
		if _, err := d.IsMobile(); err != nil {
			b.Fatal(err)
		}
		if _, err := d.IsTablet(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompileAllRules compiles every detection rule once, which is the
// one-time warmup cost paid by the first detection on each MobileDetect
// instance.
func BenchmarkCompileAllRules(b *testing.B) {
	for i := 0; i < b.N; i++ {
		d := New()
		for _, r := range allRules {
			for _, alt := range r.alternatives {
				if _, err := d.compile(alt); err != nil {
					b.Fatalf("compile %q: %v", alt, err)
				}
			}
		}
		for _, patterns := range properties {
			for _, p := range patterns {
				pat := replaceVer(p)
				if _, err := d.compile(pat); err != nil {
					b.Fatalf("compile prop %q: %v", pat, err)
				}
			}
		}
	}
}

// replaceVer is a tiny helper mirroring Version()'s [VER] substitution.
func replaceVer(p string) string {
	for i := 0; i < len(p); i++ {
		if p[i] == '[' {
			return p[:i] + versionRegex + p[i+5:]
		}
	}
	return p
}

// BenchmarkIsSingleRule measures Is("iOS") style lookups, which are the common
// per-request use case in a server.
func BenchmarkIsSingleRule(b *testing.B) {
	d := New()
	d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = d.Is("iOS")
	}
}

// TestConcurrentSharedDetector verifies that, once a MobileDetect instance is
// configured (UA set), concurrent READ-ONLY detection calls are safe.
//
// Like the upstream PHP class, MobileDetect is NOT safe for concurrent
// SetUserAgent/SetHttpHeaders calls — configuration must happen before the
// instance is shared. The recommended server pattern is one MobileDetect per
// request (cheap to construct) or to call SetUserAgent from the single
// goroutine owning the request.
func TestConcurrentSharedDetector(t *testing.T) {
	d := New()
	d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) Mobile/15E148")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_, _ = d.IsMobile()
				_, _ = d.IsTablet()
				_, _ = d.Is("iOS")
				_, _ = d.Version("iOS", VersionTypeString)
			}
		}()
	}
	wg.Wait()
}
