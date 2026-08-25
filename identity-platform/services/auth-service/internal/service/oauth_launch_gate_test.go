package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

type oauthExistingStore struct {
	*fakeAnomalyStore
	providerUser *store.User
	emailUser    *store.User
	links        int
}

func (s *oauthExistingStore) GetUserByLoginProvider(context.Context, string, string) (*store.User, error) {
	return s.providerUser, nil
}

func (s *oauthExistingStore) GetUserByEmail(context.Context, string) (*store.User, error) {
	return s.emailUser, nil
}

func (s *oauthExistingStore) LinkOAuthProvider(context.Context, uuid.UUID, string) error {
	s.links++
	return nil
}

func TestOAuthNewAccountGateStopsBothCreationBranchesBeforeSideEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		emailVerified bool
	}{
		{name: "provider_verified_email", emailVerified: true},
		{name: "provider_unverified_email", emailVerified: false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := &fakeAnomalyStore{}
			svc := New(st, nil, &config.Config{OAuthNewAccountEnabled: false}, nil, nil, nil)

			result, err := svc.loginOrRegisterOAuth(context.Background(), "google", &OAuthUserInfo{
				Email:         "new-user@example.com",
				Name:          "New User",
				Sub:           "provider-subject",
				EmailVerified: tc.emailVerified,
			})
			if !errors.Is(err, ErrOAuthNewAccountDisabled) {
				t.Fatalf("error = %v, want ErrOAuthNewAccountDisabled", err)
			}
			if result != nil {
				t.Fatalf("result = %#v, want nil", result)
			}
			if len(st.sessions) != 0 {
				t.Fatalf("created %d sessions while new-account OAuth was disabled", len(st.sessions))
			}
		})
	}
}

func TestOAuthCompletionEndpointsFailBeforeRedisOrDatabaseWhenDisabled(t *testing.T) {
	t.Parallel()

	svc := New(&fakeAnomalyStore{}, nil,
		&config.Config{OAuthNewAccountEnabled: false}, nil, nil, nil)

	if err := svc.CompleteOAuthSignup(context.Background(), "pending", "+919999999999"); !errors.Is(err, ErrOAuthNewAccountDisabled) {
		t.Fatalf("CompleteOAuthSignup error = %v, want ErrOAuthNewAccountDisabled", err)
	}
	if result, err := svc.VerifyOAuthSignup(context.Background(), "pending", "123456", "device", "android", "127.0.0.1", "test"); !errors.Is(err, ErrOAuthNewAccountDisabled) || result != nil {
		t.Fatalf("VerifyOAuthSignup = (%#v, %v), want (nil, ErrOAuthNewAccountDisabled)", result, err)
	}
}

func TestOAuthGateDoesNotBlockExistingLinkedAccountLogin(t *testing.T) {
	baseService, baseStore, _ := newTestService(t, "shadow")
	baseService.cfg.OAuthNewAccountEnabled = false
	linked := &store.User{ID: uuid.New(), AccountStatus: "active"}
	wrapper := &oauthExistingStore{fakeAnomalyStore: baseStore, providerUser: linked}
	baseService.store = wrapper

	result, err := baseService.loginOrRegisterOAuth(context.Background(), "google", &OAuthUserInfo{
		Email: "linked@example.com", Sub: "linked-subject", EmailVerified: true,
	})
	if err != nil || result == nil || result.Auth == nil || result.Auth.User.ID != linked.ID {
		t.Fatalf("existing linked OAuth login = (%#v, %v), want authenticated user", result, err)
	}
	if len(baseStore.sessions) != 1 {
		t.Fatalf("existing login created %d sessions, want 1", len(baseStore.sessions))
	}
}

func TestOAuthGateDoesNotBlockVerifiedExistingAccountLink(t *testing.T) {
	baseService, baseStore, _ := newTestService(t, "shadow")
	baseService.cfg.OAuthNewAccountEnabled = false
	existing := &store.User{ID: uuid.New(), AccountStatus: "active", EmailVerified: true}
	wrapper := &oauthExistingStore{fakeAnomalyStore: baseStore, emailUser: existing}
	baseService.store = wrapper

	result, err := baseService.loginOrRegisterOAuth(context.Background(), "google", &OAuthUserInfo{
		Email: "existing@example.com", Sub: "new-provider-subject", EmailVerified: true,
	})
	if err != nil || result == nil || result.Auth == nil || result.Auth.User.ID != existing.ID {
		t.Fatalf("existing account OAuth link = (%#v, %v), want authenticated user", result, err)
	}
	if wrapper.links != 1 || len(baseStore.sessions) != 1 {
		t.Fatalf("link/session counts = %d/%d, want 1/1", wrapper.links, len(baseStore.sessions))
	}
}
