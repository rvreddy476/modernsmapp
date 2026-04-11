package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Service) IssueMiniAppSession(ctx context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*MiniAppSessionResponse, error) {
	_ = ctx
	if s.miniAppSessionSigner == nil {
		return nil, fmt.Errorf("MINI_APP_SESSION_UNAVAILABLE")
	}
	return s.miniAppSessionSigner.Issue(appID, userID, grantedPermissions)
}

func (s *Service) MiniAppJWKS(ctx context.Context) (*JSONWebKeySet, error) {
	_ = ctx
	if s.miniAppSessionSigner == nil {
		return nil, fmt.Errorf("MINI_APP_SESSION_UNAVAILABLE")
	}
	return s.miniAppSessionSigner.JWKS(), nil
}
