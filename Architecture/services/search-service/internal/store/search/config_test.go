package search

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		production bool
		wantErr    string // substring; "" = no error
		want       Config
	}{
		{
			name: "dev default: no URL, no auth",
			env:  map[string]string{},
			want: Config{URL: DefaultURL},
		},
		{
			name: "dev http with no auth is fine",
			env:  map[string]string{EnvURL: "http://localhost:9200"},
			want: Config{URL: "http://localhost:9200"},
		},
		{
			name: "dev https with no auth is tolerated",
			env:  map[string]string{EnvURL: "https://localhost:9200"},
			want: Config{URL: "https://localhost:9200"},
		},
		{
			name: "dev skip-verify accepted",
			env:  map[string]string{EnvURL: "https://localhost:9200", EnvInsecureSkipVerify: "true"},
			want: Config{URL: "https://localhost:9200", InsecureSkipVerify: true},
		},
		{
			name: "credentials carried through",
			env:  map[string]string{EnvURL: "https://os.example", EnvUsername: "admin", EnvPassword: "s3cret"},
			want: Config{URL: "https://os.example", Username: "admin", Password: "s3cret"},
		},
		{
			name:    "username without password rejected everywhere",
			env:     map[string]string{EnvURL: "http://localhost:9200", EnvUsername: "admin"},
			wantErr: "must be set together",
		},
		{
			name:    "password without username rejected everywhere",
			env:     map[string]string{EnvURL: "http://localhost:9200", EnvPassword: "x"},
			wantErr: "must be set together",
		},
		{
			name:    "bad skip-verify value",
			env:     map[string]string{EnvInsecureSkipVerify: "yes please"},
			wantErr: "not a boolean",
		},
		{
			name:    "bad URL",
			env:     map[string]string{EnvURL: "opensearch:9200"},
			wantErr: "not an absolute http(s) URL",
		},
		{
			name:    "bad scheme",
			env:     map[string]string{EnvURL: "ftp://opensearch:9200"},
			wantErr: "unsupported scheme",
		},
		{
			name:       "production https without credentials refuses to boot",
			env:        map[string]string{EnvURL: "https://vpc-atpost.ap-south-1.es.amazonaws.com"},
			production: true,
			wantErr:    "production refuses to start",
		},
		{
			name:       "production https with credentials boots",
			env:        map[string]string{EnvURL: "https://vpc-atpost.ap-south-1.es.amazonaws.com", EnvUsername: "atpost", EnvPassword: "pw"},
			production: true,
			want:       Config{URL: "https://vpc-atpost.ap-south-1.es.amazonaws.com", Username: "atpost", Password: "pw"},
		},
		{
			name:       "production skip-verify refused even with credentials",
			env:        map[string]string{EnvURL: "https://os.example", EnvUsername: "a", EnvPassword: "b", EnvInsecureSkipVerify: "1"},
			production: true,
			wantErr:    "disables TLS certificate verification",
		},
		{
			name:       "production http without credentials is not the https rule's concern",
			env:        map[string]string{EnvURL: "http://opensearch:9200"},
			production: true,
			want:       Config{URL: "http://opensearch:9200"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConfigFromEnv(envMap(tc.env), tc.production)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (cfg=%+v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("config mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// TestConfigFromEnv_ProductionErrorNeverEchoesPassword: a password pasted
// into the URL userinfo must not surface in the refusal message.
func TestConfigFromEnv_ProductionErrorNeverEchoesPassword(t *testing.T) {
	_, err := ConfigFromEnv(envMap(map[string]string{
		EnvURL: "https://leaked-user:leaked-pass@os.example:443",
	}), true)
	if err == nil {
		t.Fatal("expected production refusal")
	}
	if strings.Contains(err.Error(), "leaked-pass") || strings.Contains(err.Error(), "leaked-user") {
		t.Fatalf("error leaks URL userinfo: %q", err.Error())
	}
}

// authCapture records the Authorization header of every request the
// store's client makes during index bootstrap.
type authCapture struct {
	mu      sync.Mutex
	headers []string
}

func (a *authCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.headers = append(a.headers, r.Header.Get("Authorization"))
	a.mu.Unlock()
	// Every index already exists: HEAD → 200, anything else → 200 {}.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

func (a *authCapture) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.headers...)
}

// TestNewWithConfig_SendsBasicAuth proves the credentials from Config
// become an Authorization: Basic header on the wire, not just a field on
// a struct.
func TestNewWithConfig_SendsBasicAuth(t *testing.T) {
	cap := &authCapture{}
	srv := httptest.NewServer(cap)
	defer srv.Close()

	store, err := NewWithConfig(Config{URL: srv.URL, Username: "atpost", Password: "pw-123"})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	if store == nil {
		t.Fatal("nil store")
	}
	seen := cap.seen()
	if len(seen) == 0 {
		t.Fatal("index bootstrap made no requests; nothing to check")
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("atpost:pw-123"))
	for i, h := range seen {
		if h != want {
			t.Fatalf("request %d: Authorization = %q, want %q", i, h, want)
		}
	}
}

// TestNew_NoCredentialsSendsNoAuthHeader keeps the dev path honest: the
// url-only constructor must not invent a header.
func TestNew_NoCredentialsSendsNoAuthHeader(t *testing.T) {
	cap := &authCapture{}
	srv := httptest.NewServer(cap)
	defer srv.Close()

	if _, err := New(srv.URL); err != nil {
		t.Fatalf("New: %v", err)
	}
	seen := cap.seen()
	if len(seen) == 0 {
		t.Fatal("index bootstrap made no requests; nothing to check")
	}
	for i, h := range seen {
		if h != "" {
			t.Fatalf("request %d: unexpected Authorization header %q", i, h)
		}
	}
}

// TestNewWithConfig_RejectsEmptyURL: an empty address must not fall
// through to the client's own localhost default.
func TestNewWithConfig_RejectsEmptyURL(t *testing.T) {
	if _, err := NewWithConfig(Config{}); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestIsProductionEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", map[string]string{}, false},
		{"APP_ENV=production", map[string]string{"APP_ENV": "production"}, true},
		{"ENVIRONMENT=prod", map[string]string{"ENVIRONMENT": "prod"}, true},
		{"ENV=Prod", map[string]string{"ENV": " Prod "}, true},
		{"APP_ENV=development wins over stale ENV=prod", map[string]string{"APP_ENV": "development", "ENV": "prod"}, false},
		{"dev", map[string]string{"ENV": "dev"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
				t.Setenv(k, tc.env[k])
			}
			if got := IsProductionEnv(); got != tc.want {
				t.Fatalf("IsProductionEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}
