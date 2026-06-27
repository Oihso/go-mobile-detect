// Package mobiledetect is a lightweight, idiomatic Go port of the PHP
// Mobile-Detect library (https://github.com/serbanghita/Mobile-Detect).
//
// It detects mobile devices (including tablets) from the User-Agent string,
// optionally combined with a set of HTTP headers that are strong "is mobile"
// signals.
//
// Quick start:
//
//	d := mobiledetect.New()
//	d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) ...")
//	d.IsMobile()  // -> true
//	d.IsTablet()  // -> false
//	d.Is("iOS")   // -> true
//	ver, _ := d.Version("iOS", mobiledetect.VersionTypeString) // -> "14_0"
//
// The detection rules are kept in sync with the upstream PHP 4.x release.
package mobiledetect

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
)

// Version is the current release of this Go port. Bumped together with the
// upstream PHP rules.
const Version = "4.11.0"

// VersionType selects the return formatting of MobileDetect.Version.
type VersionType int

const (
	// VersionTypeString returns the version as captured in the User-Agent.
	VersionTypeString VersionType = iota
	// VersionTypeFloat returns a normalized major.minor float (e.g. "2_0" -> 2.0,
	// "4.3.1" -> 4.31), matching the PHP prepareVersionNo behaviour.
	VersionTypeFloat
)

// ErrNoUserAgent is returned when a detection is attempted before a User-Agent
// has been set (and auto-initialisation from headers is disabled or unavailable).
var ErrNoUserAgent = errors.New("mobiledetect: no valid user-agent has been set")

// ErrInvalidRule is returned by Is when the requested rule name does not exist.
var ErrInvalidRule = errors.New("mobiledetect: unknown detection rule")

// defaultMaximumUserAgentLength mirrors the PHP 'maximumUserAgentLength' config.
const defaultMaximumUserAgentLength = 500

// defaultCacheTTL mirrors the PHP 'cacheTtl' config.
var defaultCacheTTL = 86400 * time.Second

// Config tunes a MobileDetect instance.
type Config struct {
	// AutoInitHeaders controls whether the constructor tries to populate the
	// User-Agent and HTTP headers from environment variables. When false you
	// must call SetUserAgent yourself. Defaults to false in Go (unlike PHP,
	// where it reads from $_SERVER), because a Go server does not have a
	// global request environment. Use NewFromHeaders or SetHttpHeaders to
	// pass request headers explicitly.
	AutoInitHeaders bool

	// MaximumUserAgentLength truncates the User-Agent before matching. A
	// non-positive value disables truncation. Defaults to 500 (PHP default).
	MaximumUserAgentLength int

	// CacheTTL is the time-to-live used for detection result caching.
	// Defaults to 24h (PHP default of 86400s).
	CacheTTL time.Duration
}

// withDefaults returns cfg with zero values replaced by the package defaults.
func (cfg Config) withDefaults() Config {
	out := cfg
	if out.MaximumUserAgentLength == 0 {
		out.MaximumUserAgentLength = defaultMaximumUserAgentLength
	}
	if out.CacheTTL == 0 {
		out.CacheTTL = defaultCacheTTL
	}
	return out
}

// MobileDetect is the entry point for device detection. Like the upstream PHP
// class it is a stateful, per-request object: call SetUserAgent / SetHttpHeaders
// from a single goroutine to configure it, then the detection methods
// (IsMobile, IsTablet, Is, Version) are safe to call concurrently once
// configuration is complete. Detection results are cached under a per-instance
// Cache (concurrency-safe). In a server, the recommended pattern is one
// MobileDetect per request, or reuse the same instance behind a mutex if you
// must reconfigure it.
type MobileDetect struct {
	config       Config
	cache        Cache
	userAgent    string
	userAgentSet bool
	httpHeaders  map[string]string

	// flatHeaders is the memoized flattened header string used by cacheKey.
	// It is recomputed only when the headers change (see SetHttpHeaders), so
	// cacheKey does not rebuild it on every detection call.
	flatHeaders string

	// matchingRegex / matches mirror the PHP debug fields, updated on a
	// successful match. They are only written during detection and are
	// intended for debugging; concurrent reads may race. Access via the
	// MatchingRegex / Matches methods.
	matchMu       sync.Mutex
	matchingRegex string
	matches       []string
}

// New returns a MobileDetect with sensible defaults: an in-memory cache bounded
// to DefaultMaxEntries and the default config. AutoInitHeaders is false.
func New() *MobileDetect {
	return NewWithConfig(Config{})
}

// NewWithConfig returns a MobileDetect using the given config and a default
// in-memory cache.
func NewWithConfig(cfg Config) *MobileDetect {
	return NewWithCache(cfg, NewMemoryCache(DefaultMaxEntries))
}

// NewWithCache returns a MobileDetect using the given config and cache.
// Pass a custom Cache (e.g. backed by Redis) to share results across
// processes. The cache must be safe for concurrent use.
func NewWithCache(cfg Config, cache Cache) *MobileDetect {
	if cache == nil {
		cache = NewMemoryCache(DefaultMaxEntries)
	}
	d := &MobileDetect{
		config:      cfg.withDefaults(),
		cache:       cache,
		httpHeaders: map[string]string{},
	}
	return d
}

// GetVersion returns the library version (mirrors PHP getVersion()).
func (d *MobileDetect) GetVersion() string {
	return Version
}

// --- HTTP headers -------------------------------------------------------

// SetHttpHeaders replaces the stored HTTP headers and re-derives the User-Agent
// from the known User-Agent-like headers (see GetUaHttpHeaders). If CloudFront
// viewer headers are present the User-Agent is forced to cloudFrontUA.
//
// Header keys are matched case- and dash-insensitively, so both the PHP-style
// "HTTP_USER_AGENT" and the canonical Go style "User-Agent" work. Values are
// stored under their normalized key.
func (d *MobileDetect) SetHttpHeaders(headers map[string]string) {
	d.httpHeaders = map[string]string{}
	d.flatHeaders = ""
	if len(headers) == 0 {
		// Setting new headers resets the User-Agent (PHP semantics).
		d.userAgent = ""
		d.userAgentSet = false
		return
	}

	for k, v := range headers {
		d.httpHeaders[normalizeHeaderKey(k)] = v
	}
	// Memoize the flattened header string used by cacheKey.
	d.flatHeaders = flattenHeaders(d.httpHeaders)

	// Re-derive the User-Agent from known UA-like headers. Both the PHP-style
	// key ("HTTP_USER_AGENT") and the canonical form ("User-Agent") resolve,
	// because GetHttpHeader normalizes and also tries the HTTP_-prefixed form.
	var ua strings.Builder
	for _, alt := range knownUserAgentHttpHeaders {
		if v, ok := d.GetHttpHeader(alt); ok && v != "" {
			ua.WriteString(v)
			ua.WriteByte(' ')
		}
	}
	if ua.Len() > 0 {
		d.setUserAgent(strings.TrimRight(ua.String(), " "))
	} else {
		// No UA-like header was found. To match the upstream PHP auto-init
		// contract (which sets an empty UA so that isMobile() does not throw
		// but simply returns false), record the User-Agent as set-but-empty.
		// Detection methods therefore won't error, but an empty UA short
		// -circuits to false (headers are not consulted in that case, as in
		// PHP).
		d.userAgent = ""
		d.userAgentSet = true
	}

	// CloudFront override: if mobile/tablet viewer headers are present, force UA.
	if d.hasHeader(knownCloudFrontHeadersNorm[0]) || d.hasHeader(knownCloudFrontHeadersNorm[1]) {
		d.setUserAgent(cloudFrontUA)
	}
}

// GetHttpHeaders returns a copy of the stored HTTP headers.
func (d *MobileDetect) GetHttpHeaders() map[string]string {
	out := make(map[string]string, len(d.httpHeaders))
	for k, v := range d.httpHeaders {
		out[k] = v
	}
	return out
}

// HasHttpHeaders reports whether any HTTP headers are stored.
func (d *MobileDetect) HasHttpHeaders() bool {
	return len(d.httpHeaders) > 0
}

// GetHttpHeader retrieves a particular header. It accepts either the PHP-style
// key ("HTTP_USER_AGENT") or the canonical form ("User-Agent") and resolves
// them case/dash-insensitively, in either direction (a bare "User-Agent" will
// find a header stored as "HTTP_USER_AGENT" and vice-versa).
func (d *MobileDetect) GetHttpHeader(header string) (string, bool) {
	key := normalizeHeaderKey(header)
	if v, ok := d.httpHeaders[key]; ok {
		return v, true
	}
	const prefix = "HTTP_"
	// Try the HTTP_-prefixed form ("User-Agent" -> "HTTP_USER_AGENT").
	if !strings.HasPrefix(key, prefix) {
		if v, ok := d.httpHeaders[prefix+key]; ok {
			return v, true
		}
	}
	// Try the HTTP_-stripped form ("HTTP_USER_AGENT" -> "USER_AGENT").
	if strings.HasPrefix(key, prefix) {
		if v, ok := d.httpHeaders[strings.TrimPrefix(key, prefix)]; ok {
			return v, true
		}
	}
	return "", false
}

// hasHeader reports whether a normalized header exists and is non-empty.
func (d *MobileDetect) hasHeader(name string) bool {
	v, ok := d.httpHeaders[name]
	return ok && v != ""
}

// GetUaHttpHeaders returns the list of headers that may carry the User-Agent.
func (d *MobileDetect) GetUaHttpHeaders() []string {
	return knownUserAgentHttpHeaders
}

// GetMobileHeaders returns the "mobile positive" headers.
func (d *MobileDetect) GetMobileHeaders() map[string]mobileHeaderMatch {
	return knownMobilePositiveHeaders
}

// GetCloudFrontHttpHeaders returns the CloudFront viewer headers.
func (d *MobileDetect) GetCloudFrontHttpHeaders() []string {
	return knownCloudFrontHeaders
}

// --- User-Agent ---------------------------------------------------------

// SetUserAgent sets the User-Agent used for detection, applying trimming and
// the configured maximum length. Returns the prepared value.
func (d *MobileDetect) SetUserAgent(userAgent string) string {
	d.setUserAgent(userAgent)
	return d.userAgent
}

func (d *MobileDetect) setUserAgent(userAgent string) {
	prepared := strings.TrimSpace(userAgent)
	if max := d.config.MaximumUserAgentLength; max > 0 && len(prepared) > max {
		prepared = prepared[:max]
	}
	d.userAgent = prepared
	d.userAgentSet = true
}

// GetUserAgent returns the current User-Agent. The bool reports whether a
// User-Agent has been explicitly set (mirrors PHP hasUserAgent).
func (d *MobileDetect) GetUserAgent() (string, bool) {
	return d.userAgent, d.userAgentSet
}

// IsUserAgentEmpty reports whether the User-Agent has been set but is the empty
// string. Mirrors PHP isUserAgentEmpty().
func (d *MobileDetect) IsUserAgentEmpty() bool {
	return d.userAgentSet && d.userAgent == ""
}

// MatchingRegex returns the last regex that produced a positive match (debug).
func (d *MobileDetect) MatchingRegex() string {
	d.matchMu.Lock()
	defer d.matchMu.Unlock()
	return d.matchingRegex
}

// Matches returns the last captured groups from a successful match (debug).
func (d *MobileDetect) Matches() []string {
	d.matchMu.Lock()
	defer d.matchMu.Unlock()
	out := make([]string, len(d.matches))
	copy(out, d.matches)
	return out
}

// --- Rules accessors ----------------------------------------------------

// GetPhoneDevices returns the phone detection rules.
func (d *MobileDetect) GetPhoneDevices() map[string]rule { return phoneDevices }

// GetTabletDevices returns the tablet detection rules.
func (d *MobileDetect) GetTabletDevices() map[string]rule { return tabletDevices }

// GetBrowsers returns the browser detection rules.
func (d *MobileDetect) GetBrowsers() map[string]rule { return browsers }

// GetOperatingSystems returns the operating system detection rules.
func (d *MobileDetect) GetOperatingSystems() map[string]rule { return operatingSystems }

// GetProperties returns the version-extraction properties.
func (d *MobileDetect) GetProperties() map[string][]string { return properties }

// GetRules returns the merged rule set used by Is() and IsMobile's full scan.
func (d *MobileDetect) GetRules() map[string]rule { return allRules }

// --- Cache -------------------------------------------------------------

// GetCache returns the cache in use.
func (d *MobileDetect) GetCache() Cache { return d.cache }

// SetCache replaces the cache. Useful for swapping in a shared backend.
func (d *MobileDetect) SetCache(cache Cache) {
	if cache == nil {
		cache = NewMemoryCache(DefaultMaxEntries)
	}
	d.cache = cache
}

// --- Detection ---------------------------------------------------------

// CheckHttpHeadersForMobile checks the HTTP headers for signs of mobile. This
// is the fastest possible mobile check; it is used inside IsMobile. It returns
// true if any "mobile positive" header is present and, where applicable, its
// value contains one of the expected substrings.
func (d *MobileDetect) CheckHttpHeadersForMobile() bool {
	// Iterate in the upstream PHP order: the FIRST present header decides the
	// result. If that header carries a 'matches' spec and none of the expected
	// substrings are found, PHP returns false immediately (it does not keep
	// scanning the remaining headers). We reproduce that exactly here.
	for _, header := range knownMobilePositiveHeadersOrder {
		value, ok := d.httpHeaders[header]
		if !ok {
			continue
		}
		matchType := knownMobilePositiveHeadersNorm[header]
		if len(matchType.matches) == 0 {
			// Bare presence is enough.
			return true
		}
		for _, m := range matchType.matches {
			if strings.Contains(value, m) {
				return true
			}
		}
		// First present header has a 'matches' spec but nothing matched:
		// not a mobile signal (PHP returns false here, stopping the scan).
		return false
	}
	return false
}

// IsMobile reports whether any type of mobile device is detected.
// Returns an error if no User-Agent has been set.
func (d *MobileDetect) IsMobile() (bool, error) {
	if !d.userAgentSet {
		return false, ErrNoUserAgent
	}
	if d.IsUserAgentEmpty() {
		return false, nil
	}

	key := d.cacheKey("mobile")
	if cached, ok := d.cache.Get(key); ok {
		if b, isBool := cached.(bool); isBool {
			return b, nil
		}
	}

	// Special case: Amazon CloudFront mobile viewer.
	if cfMobile, _ := d.GetHttpHeader(knownCloudFrontHeaders[0]); d.userAgent == cloudFrontUA && cfMobile == "true" {
		d.cache.Set(key, true, d.config.CacheTTL)
		return true, nil
	}

	if d.HasHttpHeaders() && d.CheckHttpHeadersForMobile() {
		d.cache.Set(key, true, d.config.CacheTTL)
		return true, nil
	}

	result := d.matchFirstRule(d.GetRules())
	d.cache.Set(key, result, d.config.CacheTTL)
	return result, nil
}

// IsTablet reports whether any type of tablet device is detected.
// Returns an error if no User-Agent has been set.
func (d *MobileDetect) IsTablet() (bool, error) {
	if !d.userAgentSet {
		return false, ErrNoUserAgent
	}
	if d.IsUserAgentEmpty() {
		return false, nil
	}

	key := d.cacheKey("tablet")
	if cached, ok := d.cache.Get(key); ok {
		if b, isBool := cached.(bool); isBool {
			return b, nil
		}
	}

	// Special case: Amazon CloudFront tablet viewer.
	if cfTablet, _ := d.GetHttpHeader(knownCloudFrontHeaders[1]); d.userAgent == cloudFrontUA && cfTablet == "true" {
		d.cache.Set(key, true, d.config.CacheTTL)
		return true, nil
	}

	// PHP joins array rules with "|" and matches each tablet rule as one pattern.
	for _, r := range tabletDevices {
		if r.pattern() == "" {
			continue
		}
		if d.match(r.pattern()) {
			d.cache.Set(key, true, d.config.CacheTTL)
			return true, nil
		}
	}

	d.cache.Set(key, false, d.config.CacheTTL)
	return false, nil
}

// Is checks whether the rule named ruleName matches the User-Agent. ruleName is
// matched case-insensitively against the merged rule set (browsers, operating
// systems, phone and tablet devices), e.g. "iOS", "iPhone", "AndroidOS",
// "Chrome". It is the Go equivalent of the PHP is<Name>() magic methods.
//
// Returns ErrInvalidRule if ruleName is not a known rule.
func (d *MobileDetect) Is(ruleName string) (bool, error) {
	if !d.userAgentSet {
		return false, ErrNoUserAgent
	}
	if d.IsUserAgentEmpty() {
		return false, nil
	}

	key := d.cacheKey(ruleName)
	if cached, ok := d.cache.Get(key); ok {
		if b, isBool := cached.(bool); isBool {
			return b, nil
		}
	}

	result := d.matchRuleNamed(ruleName)
	d.cache.Set(key, result, d.config.CacheTTL)
	return result, nil
}

// IsMobileNamed is a convenience that panics-free returns the boolean result
// of Is(ruleName), ignoring the error (returns false on missing UA / unknown
// rule). Handy for one-liners like d.IsMobileNamed("iPhone").
func (d *MobileDetect) IsMobileNamed(ruleName string) bool {
	v, _ := d.Is(ruleName)
	return v
}

// Match checks a custom regex against the User-Agent. Mirrors PHP match().
// The pattern is compiled case-insensitively (PHP used the 'is' flags: i +
// dotall). On a positive match it records the regex and captures for debugging.
func (d *MobileDetect) Match(regex string) bool {
	return d.match(regex)
}

// Version extracts the version of the given property from the User-Agent.
// See GetProperties() for the list of valid property names (e.g. "iOS",
// "Android", "Chrome", "Build", "Mobile"). ok is false if the property is
// unknown or no version could be extracted.
//
// The PHP library returns false on miss; here we return ("", false).
func (d *MobileDetect) Version(propertyName string, versionType VersionType) (string, bool) {
	if propertyName == "" || !d.userAgentSet {
		return "", false
	}

	patterns, ok := properties[propertyName]
	if !ok {
		return "", false
	}

	for _, matchStr := range patterns {
		pattern := strings.Replace(matchStr, "[VER]", versionRegex, 1)
		re, err := d.compile(pattern)
		if err != nil {
			continue
		}
		groups, matched := re.findGroups(d.userAgent)
		if matched && len(groups) >= 2 && groups[1] != "" {
			if versionType == VersionTypeFloat {
				return prepareVersionNo(groups[1]), true
			}
			return groups[1], true
		}
	}
	return "", false
}

// VersionFloat is a convenience wrapper around Version that returns the float
// value (and 0, false on miss). The returned float mirrors PHP
// prepareVersionNo, e.g. "4.3.1" -> 4.31, "2_0" -> 2.0.
func (d *MobileDetect) VersionFloat(propertyName string) (float64, bool) {
	s, ok := d.Version(propertyName, VersionTypeFloat)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// PrepareVersionNo normalizes a version fragment the way the PHP library does:
// it replaces '_', ' ' and '/' with '.', keeps the major and the (de-dotted)
// minor, and returns "major.minor" as a string. Exported for parity with PHP.
//
// Examples: "2_0" -> "2.0", "4.3.1" -> "4.31", "18.0.1025.166" -> "18.01025166".
func PrepareVersionNo(ver string) string {
	return prepareVersionNo(ver)
}

func prepareVersionNo(ver string) string {
	ver = strings.NewReplacer("_", ".", " ", ".", "/", ".").Replace(ver)
	parts := strings.SplitN(ver, ".", 2)
	if len(parts) == 2 {
		parts[1] = strings.ReplaceAll(parts[1], ".", "")
	}
	return strings.Join(parts, ".")
}

// --- internal matching -------------------------------------------------

// match compiles regex (case-insensitive, dotall) and tests it against the UA.
func (d *MobileDetect) match(regex string) bool {
	re, err := d.compile(regex)
	if err != nil {
		return false
	}
	groups, matched := re.findGroups(d.userAgent)
	if matched {
		d.matchMu.Lock()
		d.matchingRegex = regex
		d.matches = groups
		d.matchMu.Unlock()
	}
	return matched
}

// compile returns the cached compiled regex for pattern. The compiled-regex
// cache is process-global (not per-instance): the rule set is static and
// immutable, so a pattern is compiled at most once for the whole process even
// when callers create a fresh MobileDetect per request. This is what keeps the
// library fast under the recommended one-detector-per-request pattern.
func (d *MobileDetect) compile(pattern string) (*compiledRegex, error) {
	return compileGlobal(pattern)
}

// regexMatchTimeout bounds backtracking work for the (few) patterns that fall
// back to regexp2. The User-Agent is attacker-controlled, so this caps the
// worst-case cost of a pathological input (defence-in-depth on top of the
// MaximumUserAgentLength truncation). RE2-compiled patterns are linear-time and
// need no timeout.
const regexMatchTimeout = 100 * time.Millisecond

// compiledRegex wraps either a Go RE2 regexp (the fast, linear-time, ReDoS-safe
// path used for the vast majority of rules) or a regexp2 regexp (used only for
// the handful of patterns that need PCRE features such as negative lookahead,
// which RE2 cannot express).
type compiledRegex struct {
	re2  *regexp.Regexp  // non-nil when the pattern is RE2-compatible
	re2c *regexp2.Regexp // used only when re2 is nil
}

// findGroups runs the regex against s and returns the full match group slice
// (index 0 is the whole match, index 1+ are capture groups), mirroring the
// slice shape of Go's regexp.FindStringSubmatch.
func (c *compiledRegex) findGroups(s string) ([]string, bool) {
	if c.re2 != nil {
		m := c.re2.FindStringSubmatch(s)
		if m == nil {
			return nil, false
		}
		return m, true
	}
	m, err := c.re2c.FindStringMatch(s)
	if err != nil || m == nil {
		return nil, false
	}
	groups := m.Groups()
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		caps := g.Captures
		if len(caps) > 0 {
			out = append(out, caps[0].String())
		} else {
			out = append(out, "")
		}
	}
	return out, true
}

// globalCompiled is the process-wide compiled-regex cache, keyed by pattern.
//
//nolint:gochecknoglobals // shared immutable compile cache.
var (
	globalCompiledMu sync.RWMutex
	globalCompiled   = map[string]*compiledRegex{}
)

// compileGlobal returns the cached compiled regex for pattern, compiling it once
// for the whole process. Patterns are compiled with the PHP 'is' flags:
// case-insensitive and dotall ('.' matches newlines). It tries Go's RE2 engine
// first (fast, linear-time) and only falls back to regexp2 when the pattern uses
// PCRE-only constructs that RE2 cannot compile (e.g. negative lookahead).
func compileGlobal(pattern string) (*compiledRegex, error) {
	globalCompiledMu.RLock()
	c, ok := globalCompiled[pattern]
	globalCompiledMu.RUnlock()
	if ok {
		return c, nil
	}

	built, err := buildCompiled(pattern)
	if err != nil {
		return nil, err
	}

	globalCompiledMu.Lock()
	// Another goroutine may have compiled the same pattern concurrently.
	if existing, ok := globalCompiled[pattern]; ok {
		globalCompiledMu.Unlock()
		return existing, nil
	}
	globalCompiled[pattern] = built
	globalCompiledMu.Unlock()
	return built, nil
}

// buildCompiled compiles pattern, preferring RE2 and falling back to regexp2.
func buildCompiled(pattern string) (*compiledRegex, error) {
	// RE2 first: prepend the PHP 'is' flags as an inline group. RE2 rejects
	// PCRE-only syntax (lookaround, backreferences) at compile time, which is
	// exactly our signal to fall back to regexp2.
	if re, err := regexp.Compile("(?is)" + pattern); err == nil {
		return &compiledRegex{re2: re}, nil
	}
	re, err := regexp2.Compile(pattern, regexp2.IgnoreCase|regexp2.Singleline)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = regexMatchTimeout
	return &compiledRegex{re2c: re}, nil
}

// matchFirstRule mirrors PHP matchUserAgentWithFirstFoundMatchingRule: it walks
// the merged rule set and returns true on the first matching rule, testing each
// alternative individually (PHP behaviour for IsMobile). Joining alternatives
// into one big regex was measured to be no faster (RE2 does the same total scan
// work) while churning far more memory rebuilding the joined string, so we keep
// the per-alternative form — it also preserves precise Matches() debug info.
func (d *MobileDetect) matchFirstRule(rules map[string]rule) bool {
	for _, r := range rules {
		for _, alt := range r.alternatives {
			if alt == "" {
				continue
			}
			if d.match(alt) {
				return true
			}
		}
	}
	return false
}

// matchRuleNamed mirrors PHP matchUserAgentWithRule: it lower-cases the rule
// name, looks it up in the merged rule set (case-insensitively) and matches the
// joined pattern. Returns false (no error) for unknown rule names.
func (d *MobileDetect) matchRuleNamed(ruleName string) bool {
	ruleName = strings.ToLower(ruleName)
	r, ok := lookupRuleCI(allRules, ruleName)
	if !ok {
		return false
	}
	pattern := r.pattern()
	if pattern == "" {
		return false
	}
	return d.match(pattern)
}

// lookupRuleCI finds a rule by its lower-cased name. Pre-computed for speed.
func lookupRuleCI(rules map[string]rule, lowerName string) (rule, bool) {
	r, ok := rules[lowerName]
	if ok {
		return r, true
	}
	// Fall back to a case-insensitive scan (covers already-lowercased maps and
	// any key casing). Cached so the linear scan happens at most once per name.
	return lookupRuleCIScan(rules, lowerName)
}

// rulesLower caches a lower-cased view of allRules for fast Is() lookups.
// allRules is static, so this is built once at init time.
//
//nolint:gochecknoglobals // memoized lookup.
var rulesLower = buildRulesLower()

// buildRulesLower builds a lower-cased view of allRules.
func buildRulesLower() map[string]rule {
	out := make(map[string]rule, len(allRules))
	for k, v := range allRules {
		out[strings.ToLower(k)] = v
	}
	return out
}

func lookupRuleCIScan(_ map[string]rule, lowerName string) (rule, bool) {
	r, ok := rulesLower[lowerName]
	return r, ok
}

// cacheKey builds a deterministic key for the detection cache, mirroring PHP's
// createCacheKey: "<key>:<ua>:<headers>" hashed with sha1. The flattened-headers
// part is memoized in d.flatHeaders (recomputed only on header change), so this
// only hashes on each call rather than rebuilding the header string every time.
func (d *MobileDetect) cacheKey(key string) string {
	ua := d.userAgent
	if !d.userAgentSet {
		ua = ""
	}
	raw := key + ":" + ua + ":" + d.flatHeaders
	h := sha1.Sum([]byte(raw))
	return hex.EncodeToString(h[:])
}

// flattenHeaders mirrors PHP flattenHeaders: "Name: value\n" joined & trimmed.
func flattenHeaders(headers map[string]string) string {
	var b strings.Builder
	for name, value := range headers {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
