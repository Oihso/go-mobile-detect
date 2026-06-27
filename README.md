# mobile-detect (Go)

> English | [简体中文](README_ZH.md)

[![Go Reference](https://pkg.go.dev/badge/github.com/anhao/go-mobile-detect.svg)](https://pkg.go.dev/github.com/anhao/go-mobile-detect)

A lightweight, idiomatic Go port of the [PHP **Mobile-Detect**](https://github.com/serbanghita/Mobile-Detect) library for detecting mobile devices (including tablets) from the User-Agent string, optionally combined with a small set of HTTP headers that are strong *"is mobile"* signals.

The detection rules are a **1:1 port** of the upstream PHP 4.x rules and are verified against the **entire upstream test corpus** (1749 real User-Agents across 32 vendors) — the Go port agrees with PHP on `isMobile`, `isTablet`, `version()`, and vendor checks with **0 mismatches**. See [Testing & parity](#testing--parity).

- Module: `github.com/anhao/go-mobile-detect`
- Go version: 1.21+
- License: MIT

---

## Install

```bash
go get github.com/anhao/go-mobile-detect
```

## Quick start

```go
package main

import (
    "fmt"

    "github.com/anhao/go-mobile-detect"
)

func main() {
    d := mobiledetect.New()
    d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/604.1")

    mobile, _ := d.IsMobile() // true
    tablet, _ := d.IsTablet() // false
    isIOS, _ := d.Is("iOS")   // true

    ver, _ := d.Version("iOS", mobiledetect.VersionTypeString) // "14_0"
    v, _   := d.VersionFloat("iOS")                            // 14.0

    fmt.Println(mobile, tablet, isIOS, ver, v)
}
```

## Use with `net/http`

The idiomatic entry point inside an HTTP handler is `NewFromRequest`, which
extracts the known User-Agent-like and "mobile positive" headers from the
request for you:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    d := mobiledetect.NewFromRequest(r)
    if mobile, _ := d.IsMobile(); mobile {
        // serve mobile experience
    }
}
```

If you already have headers in a `map[string]string`, use `SetHttpHeaders`.
Keys are matched case- and dash-insensitively, so both the PHP-style
`"HTTP_USER_AGENT"` and the canonical Go style `"User-Agent"` work.

## API

### Creating a detector

| Function | Description |
|---|---|
| `New()` | Default in-memory cache, default config. |
| `NewWithConfig(cfg)` | Custom [`Config`](#config). |
| `NewWithCache(cfg, cache)` | Inject your own [`Cache`](#caching) (e.g. Redis). |
| `NewFromRequest(r)` | Build from an `*http.Request`. |
| `NewFromRequestWithConfig(r, cfg)` | Build from a request with a custom config. |

### Configuration

```go
type Config struct {
    AutoInitHeaders       bool          // no-op in Go (no global $_SERVER); kept for parity. Default false.
    MaximumUserAgentLength int          // truncate UA before matching; 0 or negative disables. Default 500.
    CacheTTL               time.Duration // TTL for cached detection results. Default 24h.
}
```

### Detection

| Method | Equivalent PHP | Returns |
|---|---|---|
| `IsMobile() (bool, error)` | `isMobile()` | any mobile device detected |
| `IsTablet() (bool, error)` | `isTablet()` | any tablet device detected |
| `Is(rule string) (bool, error)` | `is<Name>()` magic methods | rule matched (case-insensitive), e.g. `"iPhone"`, `"iOS"`, `"AndroidOS"`, `"Chrome"`, `"Huawei"` |
| `IsMobileNamed(rule) bool` | `is<Name>()` | convenience that ignores the error |
| `Version(prop, type) (string, bool)` | `version()` | extracted version string; `type` is `VersionTypeString` or `VersionTypeFloat` |
| `VersionFloat(prop) (float64, bool)` | `version(..., float)` | version as float (e.g. `"4.3.1"` → `4.31`) |
| `Match(regex) bool` | `match()` | match a custom regex against the UA |
| `CheckHttpHeadersForMobile() bool` | `checkHttpHeadersForMobile()` | fast header-based mobile signal |

Detection methods return `ErrNoUserAgent` if no User-Agent has been set, and
short-circuit to `false` on an empty UA (matching PHP).

### Accessors

`GetUserAgent`, `SetUserAgent`, `GetHttpHeaders`, `SetHttpHeaders`, `GetHttpHeader`,
`GetRules`, `GetPhoneDevices`, `GetTabletDevices`, `GetBrowsers`,
`GetOperatingSystems`, `GetProperties`, `GetVersion`, `GetCache`, `SetCache`,
`MatchingRegex`, `Matches`.

### Caching

Every detection result is cached under a per-instance `Cache`. The default is an
in-memory LRU-ish cache with FIFO eviction, bounded to `DefaultMaxEntries`
(1000) entries — this prevents unbounded growth in long-running servers that
reuse one detector across many distinct User-Agents.

```go
// Tune the bound:
d := mobiledetect.NewWithConfig(mobiledetect.Config{})
d.GetCache().(*mobiledetect.MemoryCache) // or pass your own via NewWithCache
```

You can implement the `Cache` interface to back it by Redis, Memcached, etc.:

```go
type Cache interface {
    Get(key string) (any, bool)
    Set(key string, value any, ttl time.Duration)
    Has(key string) bool
    Delete(key string)
    Clear()
}
```

The `MemoryCache` is safe for concurrent use.

## HarmonyOS / 鸿蒙

HarmonyOS is detected as an **operating system** via `Is("HarmonyOS")`, covering
both generations of HarmonyOS devices:

- **Classic HarmonyOS (Android-based)** — the UA carries the `HarmonyOS` token
  alongside the `Android` kernel marker.
- **HarmonyOS NEXT / 纯血鸿蒙** — the UA no longer contains `Android`; it
  identifies itself with the `OpenHarmony` and `ArkWeb` (ArkWeb kernel) tokens.

The OS rule matches the `HarmonyOS`, `OpenHarmony` and `ArkWeb` tokens, so both
generations are recognized as HarmonyOS and treated as mobile devices.

```go
d := mobiledetect.New()

// Classic HarmonyOS (Android-based)
d.SetUserAgent("Mozilla/5.0 (Linux; Android 10; HarmonyOS; ALN-AL00; HMSCore 6.11.0) AppleWebKit/537.36 Mobile Safari/537.36")
d.Is("HarmonyOS") // true
d.Is("AndroidOS") // true (its UA still carries the Android kernel marker)
d.IsMobile()      // true

// HarmonyOS NEXT / 纯血鸿蒙
d.SetUserAgent("Mozilla/5.0 (Phone; OpenHarmony 5.0) AppleWebKit/537.36 Safari/537.36 ArkWeb/4.1.6.1")
d.Is("HarmonyOS") // true
d.Is("AndroidOS") // false (pure HarmonyOS no longer carries Android)
d.IsMobile()      // true
```

> This is an enhancement over the upstream PHP library, which only matches the
> literal `HarmonyOS` token and misses HarmonyOS NEXT devices that identify
> themselves via `OpenHarmony` / `ArkWeb`.

This library detects device **class** (mobile/tablet), **operating system**,
**browser** and **version**. It does not attempt to identify a device's brand or
model — for that, use HTTP Client Hints (`Sec-CH-UA*`) or a dedicated device
database.

## Concurrency

Like the upstream PHP class, `MobileDetect` is a **stateful, per-request**
object. Configure it (call `SetUserAgent` / `SetHttpHeaders`) from a single
goroutine; once configured, the detection methods (`IsMobile`, `IsTablet`,
`Is`, `Version`) are safe to call concurrently. The recommended server pattern
is **one `MobileDetect` per request** — constructing one is cheap.

> **Compiled regexes are cached process-globally**, not per instance, so each
> rule is compiled at most once for the whole process even when you create a
> fresh detector per request. The **result cache**, however, is per instance by
> default; for high-throughput workloads with repeating User-Agents, pass a
> shared `Cache` via `NewWithCache` so results are reused across requests.

> **Regex engine:** most rules are compiled with Go's standard `regexp` (RE2),
> which is linear-time and ReDoS-safe. The handful of rules that need PCRE-only
> features (negative lookahead `(?!...)`) fall back to
> [`github.com/dlclark/regexp2`](https://github.com/dlclark/regexp2) and run
> under a match timeout, since the User-Agent is attacker-controlled.

## Testing & parity

The package is covered by unit tests, benchmarks, and a golden corpus built from
the full upstream test data — **1749 real User-Agents across 32 vendors**. The Go
port agrees with the PHP library on `IsMobile`, `IsTablet`, `Version()` and
vendor checks with **0 mismatches**.

Run everything (including the race detector):

```bash
go test ./... -race
go test ./... -bench=. -run=^$
```

## Differences from the PHP library

- PHP's `is<Name>()` magic methods become `Is("Name")` (case-insensitive) and the
  convenience `IsMobileNamed("Name")`.
- `version()` returns `(string, bool)` / `VersionFloat()` returns `(float64, bool)`
  instead of `false` on miss — idiomatic Go.
- Auto-initialisation from `$_SERVER` is not applicable in Go; use
  `NewFromRequest` or `SetHttpHeaders` explicitly.
- The cache key function is fixed to SHA-1 (the PHP default).

## Credits

- Original PHP library: [Serban Ghita & contributors](https://github.com/serbanghita/Mobile-Detect) — MIT License.
- This Go port: anhao — MIT License.

## License

MIT — see [LICENSE](LICENSE).
