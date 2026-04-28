package sfu

import "testing"

func TestNormalizeLiveKitURLsHTTPS(t *testing.T) {
	apiURL, clientURL, err := normalizeLiveKitURLs("https://calls.cleestudio.com")
	if err != nil {
		t.Fatalf("normalizeLiveKitURLs returned error: %v", err)
	}
	if apiURL != "https://calls.cleestudio.com" {
		t.Fatalf("unexpected api url: %s", apiURL)
	}
	if clientURL != "wss://calls.cleestudio.com" {
		t.Fatalf("unexpected client url: %s", clientURL)
	}
}

func TestNormalizeLiveKitURLsWS(t *testing.T) {
	apiURL, clientURL, err := normalizeLiveKitURLs("ws://localhost:7880")
	if err != nil {
		t.Fatalf("normalizeLiveKitURLs returned error: %v", err)
	}
	if apiURL != "http://localhost:7880" {
		t.Fatalf("unexpected api url: %s", apiURL)
	}
	if clientURL != "ws://localhost:7880" {
		t.Fatalf("unexpected client url: %s", clientURL)
	}
}

func TestNormalizeLiveKitURLsRejectsInvalidScheme(t *testing.T) {
	_, _, err := normalizeLiveKitURLs("ftp://calls.cleestudio.com")
	if err == nil {
		t.Fatal("expected invalid scheme error")
	}
}
