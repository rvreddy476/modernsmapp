package service

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBuildUPIIntentURL_WellFormed(t *testing.T) {
	id := uuid.New()
	got := buildUPIIntentURL("atpostwallet@partnerbank", "AtPost Wallet", 50000, id)

	if !strings.HasPrefix(got, "upi://pay?") {
		t.Fatalf("expected upi:// scheme; got %q", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if q.Get("pa") != "atpostwallet@partnerbank" {
		t.Fatalf("pa wrong: %q", q.Get("pa"))
	}
	if q.Get("pn") != "AtPost Wallet" {
		t.Fatalf("pn wrong: %q", q.Get("pn"))
	}
	if q.Get("am") != "500.00" {
		t.Fatalf("am should be rupees with two decimals; got %q", q.Get("am"))
	}
	if q.Get("cu") != "INR" {
		t.Fatalf("cu should be INR")
	}
	if q.Get("tn") != id.String() {
		t.Fatalf("tn should be tx id; got %q", q.Get("tn"))
	}
}

func TestBuildUPIIntentURL_RoundsRupees(t *testing.T) {
	got := buildUPIIntentURL("vpa@bank", "name", 12345, uuid.New())
	u, _ := url.Parse(got)
	if u.Query().Get("am") != "123.45" {
		t.Fatalf("expected 123.45; got %q", u.Query().Get("am"))
	}
}

func TestBuildUPIIntentURL_TenPaise(t *testing.T) {
	got := buildUPIIntentURL("vpa@bank", "name", 10, uuid.New())
	u, _ := url.Parse(got)
	if u.Query().Get("am") != "0.10" {
		t.Fatalf("expected 0.10; got %q", u.Query().Get("am"))
	}
}
