package mobiledetect

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newDetect(t *testing.T) *MobileDetect {
	t.Helper()
	return New()
}

// --- User-Agent handling ------------------------------------------------

func TestSetGetUserAgent(t *testing.T) {
	d := New()
	d.SetUserAgent("hello world")
	got, ok := d.GetUserAgent()
	if !ok || got != "hello world" {
		t.Fatalf("got %q (%v), want %q", got, ok, "hello world")
	}
}

func TestSetUserAgentTrimsAndTruncates(t *testing.T) {
	d := New()
	long := strings.Repeat("a", 600)
	d.SetUserAgent("  " + long + "  ")
	got, _ := d.GetUserAgent()
	if len(got) != defaultMaximumUserAgentLength {
		t.Fatalf("UA length = %d, want %d", len(got), defaultMaximumUserAgentLength)
	}
}

func TestSetUserAgentCustomMaxLength(t *testing.T) {
	d := NewWithConfig(Config{MaximumUserAgentLength: 10})
	d.SetUserAgent("1234567890ABCDEF")
	got, _ := d.GetUserAgent()
	if got != "1234567890" {
		t.Fatalf("got %q, want truncated to 10 chars", got)
	}
}

func TestSetUserAgentZeroMaxLengthDisablesTruncation(t *testing.T) {
	d := NewWithConfig(Config{MaximumUserAgentLength: -1})
	ua := strings.Repeat("a", 800)
	d.SetUserAgent(ua)
	got, _ := d.GetUserAgent()
	if got != ua {
		t.Fatalf("expected no truncation, got len %d", len(got))
	}
}

func TestIsUserAgentEmpty(t *testing.T) {
	d := New()
	if d.IsUserAgentEmpty() {
		t.Fatal("fresh detector should not report empty UA (no UA set)")
	}
	d.SetUserAgent("")
	if !d.IsUserAgentEmpty() {
		t.Fatal("set empty UA should report IsUserAgentEmpty=true")
	}
	d.SetUserAgent("x")
	if d.IsUserAgentEmpty() {
		t.Fatal("non-empty UA should report IsUserAgentEmpty=false")
	}
}

// --- HTTP headers -------------------------------------------------------

func TestSetHttpHeadersResetsUserAgent(t *testing.T) {
	d := New()
	d.SetUserAgent("first")
	h1 := map[string]string{"HTTP_PINK_PONY": "I secretly love ponies"}
	d.SetHttpHeaders(h1)
	if got, _ := d.GetUserAgent(); got != "" {
		t.Fatalf("resetting headers should clear UA, got %q", got)
	}
}

func TestSetHttpHeadersDerivesUserAgent(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{"HTTP_USER_AGENT": "blah"})
	if got, _ := d.GetUserAgent(); got != "blah" {
		t.Fatalf("got %q, want %q", got, "blah")
	}

	// Multiple UA-like headers are concatenated with spaces.
	d.SetHttpHeaders(map[string]string{
		"HTTP_USER_AGENT":           "iphone",
		"HTTP_X_OPERAMINI_PHONE_UA": "some other stuff",
	})
	if got, _ := d.GetUserAgent(); got != "iphone some other stuff" {
		t.Fatalf("got %q", got)
	}

	// Only the X-Device header present.
	d.SetHttpHeaders(map[string]string{"HTTP_X_DEVICE_USER_AGENT": "hello world"})
	if got, _ := d.GetUserAgent(); got != "hello world" {
		t.Fatalf("got %q", got)
	}

	// No headers at all.
	d.SetHttpHeaders(map[string]string{})
	if got, _ := d.GetUserAgent(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSetHttpHeadersCanonicalKeyForm(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{"User-Agent": "canonical-form"})
	if got, _ := d.GetUserAgent(); got != "canonical-form" {
		t.Fatalf("got %q", got)
	}
}

func TestGetHttpHeaderBothForms(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{"HTTP_USER_AGENT": "iPhone UA"})
	for _, in := range []string{"HTTP_USER_AGENT", "User-Agent", "user-agent"} {
		v, ok := d.GetHttpHeader(in)
		if !ok || v != "iPhone UA" {
			t.Fatalf("GetHttpHeader(%q) = %q (%v)", in, v, ok)
		}
	}
	v, ok := d.GetHttpHeader("garbage_is_Garbage")
	if ok {
		t.Fatalf("missing header should return ok=false, got %q", v)
	}
}

func TestCloudFrontMobile(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{
		"HTTP_CLOUDFRONT_IS_DESKTOP_VIEWER": "false",
		"HTTP_CLOUDFRONT_IS_MOBILE_VIEWER":  "true",
		"HTTP_CLOUDFRONT_IS_TABLET_VIEWER":  "false",
	})
	if got, _ := d.GetUserAgent(); got != cloudFrontUA {
		t.Fatalf("UA = %q, want %q", got, cloudFrontUA)
	}
	if mobile, _ := d.IsMobile(); !mobile {
		t.Fatal("expected mobile")
	}
	if tablet, _ := d.IsTablet(); tablet {
		t.Fatal("expected not tablet")
	}
}

func TestCloudFrontTablet(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{
		"HTTP_CLOUDFRONT_IS_DESKTOP_VIEWER": "false",
		"HTTP_CLOUDFRONT_IS_MOBILE_VIEWER":  "false",
		"HTTP_CLOUDFRONT_IS_TABLET_VIEWER":  "true",
	})
	if mobile, _ := d.IsMobile(); mobile {
		t.Fatal("expected not mobile")
	}
	if tablet, _ := d.IsTablet(); !tablet {
		t.Fatal("expected tablet")
	}
}

func TestCloudFrontDesktop(t *testing.T) {
	d := New()
	d.SetHttpHeaders(map[string]string{
		"HTTP_CLOUDFRONT_IS_DESKTOP_VIEWER": "true",
		"HTTP_CLOUDFRONT_IS_MOBILE_VIEWER":  "false",
		"HTTP_CLOUDFRONT_IS_TABLET_VIEWER":  "false",
	})
	if mobile, _ := d.IsMobile(); mobile {
		t.Fatal("expected not mobile")
	}
	if tablet, _ := d.IsTablet(); tablet {
		t.Fatal("expected not tablet")
	}
}

// --- checkHttpHeadersForMobile -----------------------------------------

func TestCheckHttpHeadersForMobilePositive(t *testing.T) {
	cases := []map[string]string{
		{"HTTP_ACCEPT": "application/json; q=0.2, application/x-obml2d; q=0.8"},
		{"HTTP_ACCEPT": "text/*; q=0.1, application/vnd.rim.html"},
		{"HTTP_ACCEPT": "text/vnd.wap.wml"},
		{"HTTP_ACCEPT": "application/vnd.wap.xhtml+xml"},
		{"HTTP_X_WAP_PROFILE": "hello"},
		{"HTTP_PROFILE": "x"},
		{"HTTP_X_OPERAMINI_PHONE_UA": "x"},
		{"HTTP_UA_CPU": "ARM"},
		{"Sec-CH-UA-Mobile": "?1"},
	}
	for i, h := range cases {
		d := New()
		d.SetHttpHeaders(h)
		if !d.CheckHttpHeadersForMobile() {
			t.Fatalf("case %d (%v): expected mobile headers", i, h)
		}
	}
}

func TestCheckHttpHeadersForMobileNegative(t *testing.T) {
	cases := []map[string]string{
		{"HTTP_UA_CPU": "AMD64"},
		{"HTTP_UA_CPU": "X86"},
		{"HTTP_ACCEPT": "text/javascript, application/javascript, */*"},
		{"HTTP_REQUEST_METHOD": "DELETE"},
		{"Sec-CH-UA-Mobile": "?0"},
	}
	for i, h := range cases {
		d := New()
		d.SetHttpHeaders(h)
		if d.CheckHttpHeadersForMobile() {
			t.Fatalf("case %d (%v): expected NOT mobile headers", i, h)
		}
	}
}

// --- isMobile / isTablet / is ------------------------------------------

func TestNoUserAgentReturnsError(t *testing.T) {
	d := New()
	_, err := d.IsMobile()
	if err != ErrNoUserAgent {
		t.Fatalf("IsMobile err = %v, want ErrNoUserAgent", err)
	}
	_, err = d.IsTablet()
	if err != ErrNoUserAgent {
		t.Fatalf("IsTablet err = %v, want ErrNoUserAgent", err)
	}
	_, err = d.Is("iOS")
	if err != ErrNoUserAgent {
		t.Fatalf("Is err = %v, want ErrNoUserAgent", err)
	}
}

func TestEmptyUserAgentNotMobile(t *testing.T) {
	d := New()
	d.SetUserAgent("")
	mobile, _ := d.IsMobile()
	tablet, _ := d.IsTablet()
	is, _ := d.Is("iOS")
	if mobile || tablet || is {
		t.Fatal("empty UA must not match anything")
	}
}

func TestIsMobileIPhone(t *testing.T) {
	ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 6_0_1 like Mac OS X) AppleWebKit/536.26 (KHTML, like Gecko) Version/6.0 Mobile/10A523 Safari/8536.25"
	d := New()
	d.SetUserAgent(ua)
	if mobile, _ := d.IsMobile(); !mobile {
		t.Fatal("expected mobile")
	}
	if tablet, _ := d.IsTablet(); tablet {
		t.Fatal("expected NOT tablet")
	}
	if v, _ := d.Is("iPhone"); !v {
		t.Fatal("expected Is(\"iPhone\")=true")
	}
	if v, _ := d.Is("iphone"); !v { // case-insensitive
		t.Fatal("expected Is(\"iphone\")=true")
	}
	if v, _ := d.Is("iOS"); !v {
		t.Fatal("expected Is(\"iOS\")=true")
	}
}

func TestIsIPad(t *testing.T) {
	ua := "Mozilla/5.0 (iPad; CPU OS 6_0 like Mac OS X) AppleWebKit/536.26 (KHTML, like Gecko) Version/6.0 Mobile/10A403 Safari/8536.25"
	d := New()
	d.SetUserAgent(ua)
	if mobile, _ := d.IsMobile(); !mobile {
		t.Fatal("iPad should be mobile")
	}
	if tablet, _ := d.IsTablet(); !tablet {
		t.Fatal("iPad should be a tablet")
	}
	if v, _ := d.Is("iPad"); !v {
		t.Fatal("expected Is(\"iPad\")=true")
	}
}

func TestIsTabletSamsungGalaxyTab(t *testing.T) {
	ua := "Mozilla/5.0 (Linux; Android 4.4.2; SM-T530 Build/KOT49H) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/42.0.2311.111 Safari/537.36"
	d := New()
	d.SetUserAgent(ua)
	if tablet, _ := d.IsTablet(); !tablet {
		t.Fatal("expected Samsung tablet")
	}
}

func TestIsUnknownRuleReturnsFalse(t *testing.T) {
	d := New()
	d.SetUserAgent("anything")
	v, err := d.Is("DefinitelyNotARealRuleName")
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if v {
		t.Fatal("unknown rule should return false, not error")
	}
}

// --- version -----------------------------------------------------------

func TestVersionIPhone(t *testing.T) {
	ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 6_1_3 like Mac OS X) AppleWebKit/536.26 (KHTML, like Gecko) Version/6.0 Mobile/10B329 Safari/8536.25"
	d := New()
	d.SetUserAgent(ua)
	checks := map[string]string{
		"iOS":    "6_1_3",
		"Webkit": "536.26",
		"Mobile": "10B329",
		"Safari": "6.0",
	}
	for prop, want := range checks {
		got, ok := d.Version(prop, VersionTypeString)
		if !ok || got != want {
			t.Errorf("Version(%q) = %q (ok=%v), want %q", prop, got, ok, want)
		}
	}
}

func TestVersionFloat(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"2_0", 2.0},
		{"4.3.1", 4.31},
		{"18.0.1025.166", 18.01025166},
		{"3_0", 3.0},
	}
	for _, c := range cases {
		normalized := PrepareVersionNo(c.in)
		var got float64
		if _, err := fmt.Sscanf(normalized, "%g", &got); err != nil {
			t.Fatalf("parse %q: %v", normalized, err)
		}
		if got != c.want {
			t.Errorf("prepareVersionNo(%q) -> %q float = %v, want %v", c.in, normalized, got, c.want)
		}
	}
}

func TestVersionAndroidChrome(t *testing.T) {
	ua := "Mozilla/5.0 (Linux; Android 4.0.4; ARCHOS 80G9 Build/IMM76D) AppleWebKit/535.19 (KHTML, like Gecko) Chrome/18.0.1025.166  Safari/535.19"
	d := New()
	d.SetUserAgent(ua)
	cases := []struct {
		prop    string
		wantStr string
		wantFlt float64
	}{
		{"Android", "4.0.4", 4.04},
		{"Webkit", "535.19", 535.19},
		{"Chrome", "18.0.1025.166", 18.01025166},
	}
	for _, c := range cases {
		s, ok := d.Version(c.prop, VersionTypeString)
		if !ok || s != c.wantStr {
			t.Errorf("Version(%q,string) = %q (ok=%v), want %q", c.prop, s, ok, c.wantStr)
		}
		f, ok := d.VersionFloat(c.prop)
		if !ok || f != c.wantFlt {
			t.Errorf("VersionFloat(%q) = %v (ok=%v), want %v", c.prop, f, ok, c.wantFlt)
		}
	}
}

func TestVersionUnknownProperty(t *testing.T) {
	d := New()
	d.SetUserAgent("Mozilla/5.0 ...")
	if _, ok := d.Version("Nope", VersionTypeString); ok {
		t.Fatal("unknown property should return ok=false")
	}
}

// --- cache integration --------------------------------------------------

func TestCachesResults(t *testing.T) {
	d := New()
	c := d.GetCache()
	ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 6_0_1 like Mac OS X) AppleWebKit/536.26"
	d.SetUserAgent(ua)

	// Before detection, cache is empty.
	before := cacheLen(c)
	if before != 0 {
		t.Fatalf("cache should start empty, has %d", before)
	}
	if _, err := d.IsMobile(); err != nil {
		t.Fatal(err)
	}
	after := cacheLen(c)
	if after == 0 {
		t.Fatal("expected cache to be populated after IsMobile")
	}
}

func TestCacheKeyUniquePerUA(t *testing.T) {
	d := New()
	d.SetUserAgent("ua-one")
	k1 := d.cacheKey("mobile")
	d.SetUserAgent("ua-two")
	k2 := d.cacheKey("mobile")
	if k1 == k2 {
		t.Fatal("cache keys should differ per UA")
	}
}

// cacheLen reports the number of entries in a Cache if it is a *MemoryCache.
func cacheLen(c Cache) int {
	if mc, ok := c.(*MemoryCache); ok {
		return mc.Len()
	}
	return 0
}

// --- rules accessors ----------------------------------------------------

func TestRulesAccessorCounts(t *testing.T) {
	d := New()
	want := len(browsers) + len(operatingSystems) + len(phoneDevices) + len(tabletDevices)
	if got := len(d.GetRules()); got != want {
		t.Fatalf("GetRules count = %d, want %d", got, want)
	}
}

func TestVersionString(t *testing.T) {
	d := New()
	v := d.GetVersion()
	if !strings.Contains(v, ".") {
		t.Fatalf("version %q does not look semver", v)
	}
}

func TestMatchCustomRegex(t *testing.T) {
	d := New()
	d.SetUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 6_0 like Mac OS X)")
	if !d.Match(`iPhone`) {
		t.Fatal("expected custom match iPhone")
	}
	if d.Match(`DefinitelyNotPresentXYZ123`) {
		t.Fatal("should not match absent substring")
	}
	if d.MatchingRegex() != `iPhone` {
		t.Fatalf("MatchingRegex = %q", d.MatchingRegex())
	}
}

// keep time import alive for potential TTL tests
var _ = time.Second
