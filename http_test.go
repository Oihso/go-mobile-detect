package mobiledetect

import (
	"net/http"
	"testing"
)

func TestHeadersFromRequestExtractsUserAgent(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 6_0 like Mac OS X)")
	h := HeadersFromRequest(r)
	if h["HTTP_USER_AGENT"] == "" {
		t.Fatalf("expected HTTP_USER_AGENT extracted, got %v", h)
	}
}

func TestNewFromRequestDetectsMobile(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 6_0 like Mac OS X) Mobile/10A523")
	d := NewFromRequest(r)
	mobile, err := d.IsMobile()
	if err != nil {
		t.Fatal(err)
	}
	if !mobile {
		t.Fatal("expected mobile from request UA")
	}
	tablet, _ := d.IsTablet()
	if tablet {
		t.Fatal("iPhone should not be a tablet")
	}
	if v, _ := d.Is("iOS"); !v {
		t.Fatal("expected Is(\"iOS\")=true")
	}
}

func TestNewFromRequestMobileHeadersOnly(t *testing.T) {
	// No UA, but a mobile-positive Accept header. The raw header check still
	// detects the mobile signal even though IsMobile() short-circuits on the
	// empty User-Agent (matching upstream PHP). This documents that behaviour.
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "text/vnd.wap.wml")
	d := NewFromRequest(r)
	if !d.HasHttpHeaders() {
		t.Fatal("expected headers to be populated")
	}
	if !d.CheckHttpHeadersForMobile() {
		t.Fatal("CheckHttpHeadersForMobile should detect the mobile signal")
	}
	// IsMobile() returns false on an empty UA (mirrors PHP), with no error
	// because SetHttpHeaders recorded the UA as set-but-empty.
	mobile, err := d.IsMobile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mobile {
		t.Fatal("empty UA should short-circuit IsMobile to false (PHP parity)")
	}
}

func TestNewFromRequestCloudFront(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Cloudfront-Is-Mobile-Viewer", "true")
	r.Header.Set("Cloudfront-Is-Tablet-Viewer", "false")
	r.Header.Set("Cloudfront-Is-Desktop-Viewer", "false")
	d := NewFromRequest(r)
	if ua, _ := d.GetUserAgent(); ua != cloudFrontUA {
		t.Fatalf("UA = %q, want %q", ua, cloudFrontUA)
	}
	if mobile, _ := d.IsMobile(); !mobile {
		t.Fatal("expected mobile via CloudFront")
	}
}

func TestNewFromRequestSecChUaMobile(t *testing.T) {
	// Sec-CH-UA-Mobile: ?1 is a mobile signal detected by the header check.
	// With an empty UA, IsMobile() short-circuits (PHP parity), but the raw
	// header detection still recognises it.
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Sec-Ch-Ua-Mobile", "?1")
	d := NewFromRequest(r)
	if !d.CheckHttpHeadersForMobile() {
		t.Fatal("expected Sec-CH-UA-Mobile: ?1 to be a mobile signal")
	}
	if mobile, err := d.IsMobile(); err != nil || mobile {
		t.Fatalf("IsMobile = %v (%v), want false (nil) on empty UA", mobile, err)
	}

	// With a real UA plus the client hint, IsMobile honours the header signal.
	r2, _ := http.NewRequest("GET", "/", nil)
	r2.Header.Set("Sec-Ch-Ua-Mobile", "?1")
	r2.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10) Chrome/91")
	d2 := NewFromRequest(r2)
	mobile, err := d2.IsMobile()
	if err != nil {
		t.Fatal(err)
	}
	if !mobile {
		t.Fatal("expected mobile when both UA and client hint are present")
	}
}

func TestNewFromRequestDesktop(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/91.0")
	d := NewFromRequest(r)
	mobile, _ := d.IsMobile()
	tablet, _ := d.IsTablet()
	if mobile || tablet {
		t.Fatal("desktop UA should be neither mobile nor tablet")
	}
}

func TestNewFromNilRequest(t *testing.T) {
	// Should not panic.
	d := NewFromRequest(nil)
	_, err := d.IsMobile()
	if err != ErrNoUserAgent {
		t.Fatalf("expected ErrNoUserAgent for nil request, got %v", err)
	}
}

func TestPhpKeyToCanonical(t *testing.T) {
	cases := map[string]string{
		"HTTP_USER_AGENT":                  "User-Agent",
		"HTTP_X_OPERAMINI_PHONE_UA":        "X-Operamini-Phone-Ua",
		"HTTP_ACCEPT":                      "Accept",
		"Sec-CH-UA-Mobile":                 "Sec-Ch-Ua-Mobile",
		"HTTP_CLOUDFRONT_IS_MOBILE_VIEWER": "Cloudfront-Is-Mobile-Viewer",
	}
	for in, want := range cases {
		if got := phpKeyToCanonical(in); got != want {
			t.Errorf("phpKeyToCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}
