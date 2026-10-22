package fieldkeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/chat-shared/fieldcrypt"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var devKeys = "v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))

// countingHandler counts warnings and keeps every message for leak checks.
type countingHandler struct {
	mu    sync.Mutex
	warns int
	text  strings.Builder
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Level == slog.LevelWarn {
		h.warns++
	}
	h.text.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool { h.text.WriteString(" " + a.String()); return true })
	h.text.WriteString("\n")
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

type fakeWrapper struct{ fail error }

func (f fakeWrapper) GenerateDataKey(_ context.Context, kc map[string]string) ([]byte, []byte, error) {
	if f.fail != nil {
		return nil, nil, f.fail
	}
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return k, append([]byte("wrapped:"), k...), nil
}

func (f fakeWrapper) Unwrap(_ context.Context, w []byte, _ map[string]string) ([]byte, error) {
	return bytes.TrimPrefix(w, []byte("wrapped:")), nil
}

type memStore struct{ keys []fieldcrypt.WrappedKey }

func (m *memStore) ListWrappedKeys(context.Context) ([]fieldcrypt.WrappedKey, error) {
	return m.keys, nil
}
func (m *memStore) CreateFirstWrappedKey(_ context.Context, k fieldcrypt.WrappedKey) error {
	if len(m.keys) > 0 {
		return fieldcrypt.ErrActiveKeyExists
	}
	k.Version = 1
	m.keys = append(m.keys, k)
	return nil
}
func (m *memStore) RotateWrappedKey(_ context.Context, k fieldcrypt.WrappedKey) (uint32, error) {
	k.Version = uint32(len(m.keys) + 1)
	m.keys = append(m.keys, k)
	return k.Version, nil
}

func deps(env map[string]string, h *countingHandler) Deps {
	return Deps{
		Getenv: envOf(env),
		NewWrapper: func(context.Context, string) (fieldcrypt.KeyWrapper, error) {
			return fakeWrapper{}, nil
		},
		Store: &memStore{},
		Log:   slog.New(h),
	}
}

// APP_ENV=production with no key ring refuses to boot — the brief's hard
// rule — and so does a development keyring in production.
func TestProductionRefusesToBootWithoutAKMSKeyRing(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no keys (APP_ENV)":     {"APP_ENV": "production"},
		"no keys (ENV=prod)":    {"ENV": "prod"},
		"no keys (ENVIRONMENT)": {"ENVIRONMENT": "Production"},
		"env keyring":           {"APP_ENV": "production", EnvFieldKeys: devKeys},
		"both":                  {"APP_ENV": "production", EnvFieldKeys: devKeys, EnvKMSKeyID: "alias/chat"},
		"blank kms key":         {"APP_ENV": "production", EnvKMSKeyID: "   "},
	} {
		t.Run(name, func(t *testing.T) {
			h := &countingHandler{}
			res, err := Load(context.Background(), deps(env, h))
			if !errors.Is(err, ErrRefused) || res != nil {
				t.Fatalf("production booted: mode=%v err=%v", res, err)
			}
			if env[EnvFieldKeys] != "" && env[EnvKMSKeyID] == "" && !strings.Contains(err.Error(), "development keyring") {
				t.Fatalf("a dev keyring in production must be refused by name, got: %v", err)
			}
		})
	}
}

func TestProductionBootsOnKMS(t *testing.T) {
	h := &countingHandler{}
	store := &memStore{}
	d := deps(map[string]string{"APP_ENV": "production", EnvKMSKeyID: "alias/chat-field"}, h)
	d.Store = store
	res, err := Load(context.Background(), d)
	if err != nil || res.Mode != ModeKMS || !res.Cipher.Enabled() || res.KMSRing == nil {
		t.Fatalf("Load = %+v, %v", res, err)
	}
	if len(store.keys) != 1 || store.keys[0].Context["environment"] != "production" || store.keys[0].Context["purpose"] != KMSPurpose {
		t.Fatalf("stored key context = %+v", store.keys)
	}
	if h.warns != 0 {
		t.Fatal("a configured ring must not warn")
	}
}

func TestKMSFailureRefusesToBoot(t *testing.T) {
	h := &countingHandler{}
	d := deps(map[string]string{"APP_ENV": "staging", EnvKMSKeyID: "alias/chat"}, h)
	d.NewWrapper = func(context.Context, string) (fieldcrypt.KeyWrapper, error) {
		return fakeWrapper{fail: errors.New("AccessDeniedException")}, nil
	}
	if _, err := Load(context.Background(), d); !errors.Is(err, ErrRefused) {
		t.Fatalf("a KMS that cannot mint booted: %v", err)
	}
}

// Development without keys works, stores plaintext, and says so exactly once.
func TestDevelopmentWithoutKeysWarnsOnceAndStoresPlaintext(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"unset":   {},
		"dev":     {"APP_ENV": "development"},
		"staging": {"APP_ENV": "staging"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &countingHandler{}
			res, err := Load(context.Background(), deps(env, h))
			if err != nil || res.Mode != ModePlaintext || res.Cipher.Enabled() {
				t.Fatalf("Load = %+v, %v", res, err)
			}
			if h.warns != 1 {
				t.Fatalf("plaintext mode logged %d warnings, want exactly 1", h.warns)
			}
			got, _ := res.Cipher.SealString(context.Background(), "chat.message.text", nil, "hi")
			if got != "hi" {
				t.Fatal("plaintext mode sealed")
			}
		})
	}
}

func TestDevelopmentEnvKeyring(t *testing.T) {
	h := &countingHandler{}
	key2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	res, err := Load(context.Background(), deps(map[string]string{EnvFieldKeys: devKeys + ",v2:" + key2}, h))
	if err != nil || res.Mode != ModeEnvKeys || !res.Cipher.Enabled() {
		t.Fatalf("Load = %+v, %v", res, err)
	}
	if len(res.Versions) != 2 || res.Versions[1] != 2 {
		t.Fatalf("versions = %v", res.Versions)
	}
	sealed, _ := res.Cipher.SealString(context.Background(), "chat.message.text", fieldcrypt.Bind("r"), "hello")
	if v, _ := fieldcrypt.SealedKeyVersion(sealed); v != 2 {
		t.Fatalf("the newest key must seal; got v%d", v)
	}
	// The log never carries key material.
	if strings.Contains(h.text.String(), key2[:12]) || strings.Contains(h.text.String(), devKeys[3:15]) {
		t.Fatal("key material reached the log")
	}
	for _, bad := range []string{"v1:short", "nonsense", devKeys + "," + devKeys} {
		if _, err := Load(context.Background(), deps(map[string]string{EnvFieldKeys: bad}, h)); !errors.Is(err, ErrRefused) {
			t.Fatalf("bad keyring %.8q... accepted: %v", bad, err)
		}
	}
	if _, err := Load(context.Background(), deps(map[string]string{EnvFieldKeys: devKeys, EnvKMSKeyID: "alias/x"}, h)); !errors.Is(err, ErrRefused) {
		t.Fatal("both rings set must be refused")
	}
}

func TestIsProductionMatchesThePlatformRule(t *testing.T) {
	for env, want := range map[string]bool{"production": true, "prod": true, " PROD ": true, "staging": false, "qa": false, "": false, "dev": false} {
		if got := IsProduction(envOf(map[string]string{"APP_ENV": env})); got != want {
			t.Fatalf("APP_ENV=%q: production=%v", env, got)
		}
	}
	// The first variable that is set decides (accessauth's order).
	if IsProduction(envOf(map[string]string{"APP_ENV": "staging", "ENV": "prod"})) {
		t.Fatal("APP_ENV must win over ENV")
	}
}
