package service

import "testing"

// TestSameSubnetIPv4Same24 verifies that two IPv4 addresses in the same
// /24 are reported as the same subnet (typical DHCP rotation within an
// ISP NAT pool). This is the benign case that A11 explicitly allows
// without re-auth.
func TestSameSubnetIPv4Same24(t *testing.T) {
	if !sameSubnet("203.0.113.5", "203.0.113.99") {
		t.Fatal("expected 203.0.113.5 and 203.0.113.99 to be in the same /24")
	}
}

// TestSameSubnetIPv4Different24 verifies that two IPv4 addresses with
// different /24 prefixes are flagged as a subnet change. This is the
// signal A11 uses to force re-authentication on refresh.
func TestSameSubnetIPv4Different24(t *testing.T) {
	if sameSubnet("203.0.113.5", "203.0.114.5") {
		t.Fatal("expected 203.0.113.5 and 203.0.114.5 to be in different /24s")
	}
}

// TestSameSubnetIPv6Same48 verifies that two IPv6 addresses sharing a
// /48 prefix are reported as the same subnet.
func TestSameSubnetIPv6Same48(t *testing.T) {
	if !sameSubnet("2001:db8:1::1", "2001:db8:1:abcd::1") {
		t.Fatal("expected addresses sharing /48 prefix to be same subnet")
	}
}

// TestSameSubnetIPv6Different48 verifies that two IPv6 addresses with
// distinct /48 prefixes are flagged as different subnets.
func TestSameSubnetIPv6Different48(t *testing.T) {
	if sameSubnet("2001:db8:1::1", "2001:db8:2::1") {
		t.Fatal("expected different /48 prefixes to be different subnets")
	}
}

// TestSameSubnetEmptyOrInvalid verifies the "no signal" cases: empty
// strings and unparseable addresses must not false-positive into a
// denied refresh. The fingerprint check treats empty inputs as
// missing telemetry and must allow the refresh through (real risk is
// graded by the other signals).
func TestSameSubnetEmptyOrInvalid(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"", "1.2.3.4"},
		{"1.2.3.4", ""},
		{"", ""},
		{"not-an-ip", "1.2.3.4"},
		{"1.2.3.4", "not-an-ip"},
	}
	for _, c := range cases {
		if !sameSubnet(c.a, c.b) {
			t.Errorf("sameSubnet(%q,%q) returned false; missing-telemetry must default to true", c.a, c.b)
		}
	}
}

// TestSameUserAgentFamilyMozillaVersionBump verifies that two Mozilla
// user agents differing only in version number stay in the same family
// — A11 should not force re-auth for routine browser auto-updates.
func TestSameUserAgentFamilyMozillaVersionBump(t *testing.T) {
	a := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"
	b := "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36"
	if !sameUserAgentFamily(a, b) {
		t.Fatal("expected both Mozilla product UAs to share family")
	}
}

// TestSameUserAgentFamilyMozillaVsDalvik verifies that switching from a
// browser to a mobile app webview is detected as a UA family change —
// the kind of signal A11 uses to deny a refresh.
func TestSameUserAgentFamilyMozillaVsDalvik(t *testing.T) {
	a := "Mozilla/5.0 (X11; Linux x86_64)"
	b := "Dalvik/2.1.0 (Linux; U; Android 13)"
	if sameUserAgentFamily(a, b) {
		t.Fatal("expected Mozilla and Dalvik to be different UA families")
	}
}
