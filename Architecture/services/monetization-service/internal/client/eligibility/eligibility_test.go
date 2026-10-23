package eligibility

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func serve(t *testing.T, fn http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBirthDateOnlyCountsADateIdentityStandsBehind(t *testing.T) {
	user := uuid.New()
	for _, tc := range []struct {
		name, body string
		status     int
		wantDoB    bool
		wantFound  bool
		wantErr    bool
	}{
		{"registration", `{"data":{"user_id":"` + user.String() + `","dob":"1990-05-01","dob_source":"registration"}}`, 200, true, true, false},
		{"profile", `{"data":{"user_id":"` + user.String() + `","dob":"1990-05-01","dob_source":"profile"}}`, 200, true, true, false},
		{"unverified source", `{"data":{"user_id":"` + user.String() + `","dob":"1990-05-01","dob_source":"import"}}`, 200, false, true, false},
		{"no dob", `{"data":{"user_id":"` + user.String() + `","dob":null}}`, 200, false, true, false},
		{"another user", `{"data":{"user_id":"` + uuid.NewString() + `","dob":"1990-05-01","dob_source":"profile"}}`, 200, false, false, true},
		{"not found", ``, 404, false, false, false},
		{"5xx", ``, 503, false, false, true},
		{"malformed", `{`, 200, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Internal-Service-Key") != "k" || r.Header.Get("X-User-Id") != "" ||
					r.URL.Path != "/internal/v1/profiles/users/"+user.String()+"/identity" {
					http.Error(w, "bad request shape", 400)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			dob, found, err := NewBirthDates(url, "k").BirthDate(context.Background(), user)
			if (err != nil) != tc.wantErr || (dob != nil) != tc.wantDoB || found != tc.wantFound {
				t.Fatalf("dob=%v found=%v err=%v", dob, found, err)
			}
			if err != nil && strings.Contains(err.Error(), "1990") {
				t.Fatal("the date leaked into an error")
			}
		})
	}
	if NewBirthDates("", "k") != nil {
		t.Fatal("an empty URL must mean no source")
	}
}

func TestFollowerCount(t *testing.T) {
	user := uuid.New()
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + user.String() + `","follower_count":512}}`))
	})
	if n, err := NewFollowers(url, "k").FollowerCount(context.Background(), user); err != nil || n != 512 {
		t.Fatalf("%d %v", n, err)
	}
	missing := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"user_id":"` + user.String() + `"}}`))
	})
	if _, err := NewFollowers(missing, "k").FollowerCount(context.Background(), user); err == nil {
		t.Fatal("a body without follower_count is not zero followers")
	}
}

func TestStandingSendsAServiceTokenAndNeedsTheStrikeList(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(StandingAudience)
	if err := v.RegisterBase64(callerService, "m1", pub, []string{OpStandingRead}, nil); err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	body := `{"data":{"standing":"ok","policy_version":"standing-v1","suspended_until":null,"active_strikes":[]}}`
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := v.Verify(strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer "), OpStandingRead, ""); err != nil {
			http.Error(w, "refused", 403)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	s, err := NewStanding(url, "k", "m1", priv)
	if err != nil || s == nil {
		t.Fatalf("%v", err)
	}
	if a, err := s.Standing(context.Background(), user); err != nil || a.Standing != "ok" || a.ActiveStrikes != 0 {
		t.Fatalf("%+v %v", a, err)
	}
	body = `{"data":{"standing":"restricted","active_strikes":[{"id":"x"}]}}`
	if a, err := s.Standing(context.Background(), user); err != nil || a.ActiveStrikes != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	body = `{"data":{"standing":"ok"}}`
	if _, err := s.Standing(context.Background(), user); err == nil {
		t.Fatal("a body without active_strikes is not 'no strikes'")
	}
	// Not admitted (today's trust-safety: monetization-service is not on the
	// standing allowlist) is an error, never ok.
	_, other, _ := servicetoken.GenerateKeypair()
	s2, _ := NewStanding(url, "k", "m1", other)
	if _, err := s2.Standing(context.Background(), user); err == nil {
		t.Fatal("a refused token must be unknown")
	}
	if s3, err := NewStanding(url, "k", "", ""); s3 != nil || err != nil {
		t.Fatal("no key: no source, no error")
	}
}
