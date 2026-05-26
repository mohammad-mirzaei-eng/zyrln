package route

import (
	"strings"
	"testing"
)

func TestMatchIRTLD(t *testing.T) {
	if !matchIRTLD("example.ir") {
		t.Fatal("expected .ir match")
	}
	if matchIRTLD("google.com") {
		t.Fatal("unexpected .ir match")
	}
}

func TestDomesticMatcher_Exact(t *testing.T) {
	m := &domesticMatcher{}
	m.addRoot("Digikala.com")
	if !m.matchHost("digikala.com") {
		t.Fatal("expected exact host match")
	}
}

func TestDomesticMatcher_Subdomain(t *testing.T) {
	m := &domesticMatcher{}
	m.addRoot("digikala.com")
	if !m.matchHost("www.digikala.com") {
		t.Fatal("expected subdomain match without DNS")
	}
	if m.matchHost("notdigikala.com") {
		t.Fatal("unexpected suffix false positive")
	}
}

func TestParentDomains(t *testing.T) {
	got := parentDomains("www.digikala.com")
	if len(got) < 2 || got[0] != "www.digikala.com" || got[1] != "digikala.com" {
		t.Fatalf("parentDomains = %v", got)
	}
}

func TestShouldUseDomesticBypass_GoogleExcluded(t *testing.T) {
	if ShouldUseDomesticBypass("www.google.com") {
		t.Fatal("google should not use domestic bypass")
	}
}

func TestShouldUseDomesticBypass_AlwaysOn(t *testing.T) {
	SetDomesticBypassEnabled(false) // no-op; bypass stays on
	if !ShouldUseDomesticBypass("shop.example.ir") {
		t.Fatal("domestic bypass must stay on regardless of SetDomesticBypassEnabled")
	}
	if !GetDomesticBypassEnabled() {
		t.Fatal("GetDomesticBypassEnabled should always be true")
	}
}

func TestParseDomainsText(t *testing.T) {
	orig := domesticRules.Load()
	defer domesticRules.Store(orig)

	input := "digikala.com\n# comment\n\nsnapp.ir\n"
	if err := parseDomainsText(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	m := domesticRules.Load()
	if m == nil || !m.matchHost("digikala.com") {
		t.Fatal("digikala not loaded")
	}
	if !m.matchHost("www.digikala.com") {
		t.Fatal("www.digikala.com should match via suffix")
	}
}

func TestLoadBundledDomesticRules(t *testing.T) {
	origD := domesticRules.Load()
	defer domesticRules.Store(origD)

	if err := loadBundledDomesticRules(); err != nil {
		t.Fatal(err)
	}
	m := domesticRules.Load()
	if m == nil || !m.matchHost("digikala.com") {
		t.Fatal("expected digikala.com in bundled list")
	}
}
