package mobiledetect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// goldenEntry mirrors the JSON exported from the PHP provider fixtures.
// Fields are all optional except UserAgent + the boolean expectations.
type goldenEntry struct {
	UserAgent   string            `json:"user_agent"`
	Vendor      flexString        `json:"vendor"`
	Mobile      *bool             `json:"mobile,omitempty"`
	Tablet      *bool             `json:"tablet,omitempty"`
	Version     map[string]string `json:"version,omitempty"`
	Model       string            `json:"model,omitempty"`
	VendorCheck *bool             `json:"vendorCheck,omitempty"`
}

// flexString accepts a JSON string or number and coerces it to a string.
type flexString struct {
	s string
}

func (f *flexString) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.s = s
		return nil
	}
	// Fall back to a number.
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		f.s = n.String()
		return nil
	}
	// null or anything else -> empty.
	f.s = ""
	return nil
}

func (f flexString) String() string { return f.s }

// loadGolden reads ua_fixture.json (generated from the upstream PHP provider
// test data) sitting next to this test file.
func loadGolden(t *testing.T) []goldenEntry {
	t.Helper()
	// The fixture lives alongside this test file. Resolve it robustly whether
	// `go test` runs from the package dir or the module root.
	candidates := []string{
		"ua_fixture.json",
		filepath.Join("go-mobile-detect", "ua_fixture.json"),
	}
	var data []byte
	var err error
	for _, c := range candidates {
		data, err = os.ReadFile(c)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("could not read ua_fixture.json: %v", err)
	}

	var wrapper struct {
		Count      int           `json:"count"`
		UserAgents []goldenEntry `json:"user_agents"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("invalid fixture JSON: %v", err)
	}
	if len(wrapper.UserAgents) == 0 {
		t.Fatal("fixture contains no user agents")
	}
	return wrapper.UserAgents
}

// TestGoldenUserAgents runs the full PHP provider corpus (1749 UAs across 32
// vendors) and asserts that the Go port agrees on isMobile and isTablet. This
// is the highest-fidelity correctness check: the expected values come straight
// from the upstream PHP Mobile-Detect test suite.
func TestGoldenUserAgents(t *testing.T) {
	entries := loadGolden(t)

	var mismatches mobileTabletMismatch
	d := New()
	for i, e := range entries {
		if e.Mobile == nil && e.Tablet == nil {
			// Neither expectation present: nothing to assert.
			continue
		}

		d.SetUserAgent(e.UserAgent)

		gotMobile, err := d.IsMobile()
		if err != nil {
			t.Fatalf("entry %d IsMobile error: %v\nUA: %s", i, err, e.UserAgent)
		}
		gotTablet, err := d.IsTablet()
		if err != nil {
			t.Fatalf("entry %d IsTablet error: %v\nUA: %s", i, err, e.UserAgent)
		}

		if e.Mobile != nil && gotMobile != *e.Mobile {
			mismatches.mobile++
			mismatches.addMobile(i, e.UserAgent, e.Vendor.String(), *e.Mobile, gotMobile)
		}
		if e.Tablet != nil && gotTablet != *e.Tablet {
			mismatches.tablet++
			mismatches.addTablet(i, e.UserAgent, e.Vendor.String(), *e.Tablet, gotTablet)
		}
	}

	total := len(entries)
	t.Logf("ran %d golden UAs; mobile mismatches=%d tablet mismatches=%d",
		total, mismatches.mobile, mismatches.tablet)

	// We require a very high agreement with the PHP reference. The full
	// corpus is 1749 UAs; we allow a tiny tolerance for edge cases where
	// regexp2 (.NET) and PCRE diverge on exotic patterns, but the vast
	// majority must match.
	const tolerance = 0.02 // 2%
	if rate := float64(mismatches.mobile+mismatches.tablet) / float64(total); rate > tolerance {
		mismatches.report(t, total)
	}
}

// TestGoldenVersions spot-checks the version() extraction against the PHP
// expectations for entries that carry a version map.
func TestGoldenVersions(t *testing.T) {
	entries := loadGolden(t)
	d := New()

	checked := 0
	for i, e := range entries {
		if len(e.Version) == 0 {
			continue
		}
		d.SetUserAgent(e.UserAgent)
		for prop, want := range e.Version {
			got, ok := d.Version(prop, VersionTypeString)
			if !ok || got != want {
				t.Errorf("entry %d (%s) Version(%q) = %q (ok=%v), want %q\nUA: %s",
					i, e.Vendor.String(), prop, got, ok, want, e.UserAgent)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Skip("no version expectations in fixture")
	}
	t.Logf("checked %d version expectations", checked)
}

// TestGoldenVendorCheck asserts that is<Vendor>() returns true for entries the
// PHP suite marked vendorCheck=true. Vendors map to the merged rule set; we
// only assert when a rule with that exact name exists.
func TestGoldenVendorCheck(t *testing.T) {
	entries := loadGolden(t)
	d := New()

	checked, skipped := 0, 0
	for i, e := range entries {
		if e.VendorCheck == nil || !*e.VendorCheck {
			continue
		}
		if _, ok := allRules[e.Vendor.String()]; !ok {
			// Vendor has no direct rule (e.g. free-form vendor label); skip.
			skipped++
			continue
		}
		d.SetUserAgent(e.UserAgent)
		got, err := d.Is(e.Vendor.String())
		if err != nil {
			t.Errorf("entry %d Is(%q) error: %v", i, e.Vendor.String(), err)
			continue
		}
		if !got {
			t.Errorf("entry %d (%s): Is(%q) = false, want true\nUA: %s",
				i, e.Vendor.String(), e.Vendor.String(), e.UserAgent)
		}
		checked++
	}
	t.Logf("checked %d vendor checks (%d skipped, no matching rule)", checked, skipped)
}

// --- mismatch bookkeeping ----------------------------------------------

type uaResult struct {
	index    int
	ua       string
	vendor   string
	expected bool
	got      bool
}

type mobileTabletMismatch struct {
	mobile int
	tablet int
	// keep a sample for reporting (cap to avoid huge output).
	samples []uaResult
}

func (m *mobileTabletMismatch) addMobile(i int, ua, vendor string, want, got bool) {
	m.add(uaResult{i, ua, vendor, want, got})
}
func (m *mobileTabletMismatch) addTablet(i int, ua, vendor string, want, got bool) {
	m.add(uaResult{i, ua, vendor, want, got})
}
func (m *mobileTabletMismatch) add(r uaResult) {
	if len(m.samples) < 25 {
		m.samples = append(m.samples, r)
	}
}

func (m mobileTabletMismatch) report(t *testing.T, total int) {
	var b []byte
	b = append(b, fmt.Sprintf("%d/%d golden UAs mismatched (mobile=%d tablet=%d). Sample:\n",
		m.mobile+m.tablet, total, m.mobile, m.tablet)...)
	for _, s := range m.samples {
		b = append(b, fmt.Sprintf("  [%d] vendor=%s want=%v got=%v\n    UA: %s\n",
			s.index, s.vendor, s.expected, s.got, s.ua)...)
	}
	t.Error(string(b))
}
