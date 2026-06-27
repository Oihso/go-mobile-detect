package mobiledetect

import (
	"net/http"
)

// NewFromRequest builds a MobileDetect from an *http.Request, extracting the
// known User-Agent-like and "mobile positive" HTTP headers from the request.
// This is the idiomatic entry point for use inside an http.Handler.
//
// The returned detector is ready to use; configure and use it within the scope
// of handling that single request (one detector per request).
func NewFromRequest(r *http.Request) *MobileDetect {
	return NewFromRequestWithConfig(r, Config{})
}

// NewFromRequestWithConfig is like NewFromRequest but applies a custom Config.
func NewFromRequestWithConfig(r *http.Request, cfg Config) *MobileDetect {
	d := NewWithConfig(cfg)
	d.SetHttpHeaders(HeadersFromRequest(r))
	return d
}

// HeadersFromRequest extracts the headers this library cares about from an
// *http.Request and returns them in the normalized "HTTP_NAME" map form that
// SetHttpHeaders consumes. Only known User-Agent-like and "mobile positive"
// headers are copied, to keep the map small.
func HeadersFromRequest(r *http.Request) map[string]string {
	if r == nil {
		return map[string]string{}
	}

	// Collect every header name we might inspect.
	want := map[string]struct{}{}
	for _, h := range knownUserAgentHttpHeaders {
		want[h] = struct{}{}
	}
	for _, h := range knownCloudFrontHeaders {
		want[h] = struct{}{}
	}
	for h := range knownMobilePositiveHeaders {
		want[h] = struct{}{}
	}

	out := make(map[string]string, len(want))
	for normKey := range want {
		// normKey is either "HTTP_NAME" or a bare header like
		// "Sec-CH-UA-Mobile". Derive the canonical Go header name.
		canonical := phpKeyToCanonical(normKey)
		if v := r.Header.Get(canonical); v != "" {
			out[normKey] = v
		}
	}
	return out
}

// phpKeyToCanonical converts an internal normalized PHP-style key
// ("HTTP_USER_AGENT", "Sec-CH-UA-Mobile") to the canonical Go header name used
// by net/http ("User-Agent", "Sec-Ch-Ua-Mobile").
func phpKeyToCanonical(key string) string {
	s := key
	const prefix = "HTTP_"
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		s = s[len(prefix):]
	}
	// "USER_AGENT" -> "User_Agent"
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			b = append(b, '-')
			continue
		}
		b = append(b, c)
	}
	// net/http canonicalizes the first letter of each dash-separated token.
	return http.CanonicalHeaderKey(string(b))
}
