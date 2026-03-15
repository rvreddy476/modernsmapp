package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-profile-service/internal/store"
)

type stubProfileService struct{}

func (s *stubProfileService) ListProfiles(ctx context.Context, limit, offset int) ([]store.Profile, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	return nil, nil
}
func (s *stubProfileService) GetProfileByUsername(ctx context.Context, username string) (*store.Profile, error) {
	return nil, nil
}
func (s *stubProfileService) UpdateProfile(ctx context.Context, userID uuid.UUID, params store.UpdateProfileParams) (*store.Profile, error) {
	return nil, nil
}
func (s *stubProfileService) GetUserLinks(ctx context.Context, userID uuid.UUID) ([]store.UserLink, error) {
	return nil, nil
}
func (s *stubProfileService) UpsertUserLinks(ctx context.Context, userID uuid.UUID, links []store.UserLink) error {
	return nil
}
func (s *stubProfileService) GetProfileLinks(ctx context.Context, profileID uuid.UUID) ([]store.ProfileLink, error) {
	return nil, nil
}
func (s *stubProfileService) CreateProfileLink(ctx context.Context, link *store.ProfileLink) (*store.ProfileLink, error) {
	return nil, nil
}
func (s *stubProfileService) UpdateProfileLink(ctx context.Context, linkID, profileID uuid.UUID, title, url string, icon, category *string, sortOrder int, isPinned bool, visibility string) (*store.ProfileLink, error) {
	return nil, nil
}
func (s *stubProfileService) DeleteProfileLink(ctx context.Context, linkID, profileID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) IncrementLinkClick(ctx context.Context, linkID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) GetAllAbout(ctx context.Context, userID uuid.UUID) ([]store.AboutItem, error) {
	return nil, nil
}
func (s *stubProfileService) GetAboutBySection(ctx context.Context, userID uuid.UUID, section string) ([]store.AboutItem, error) {
	return nil, nil
}
func (s *stubProfileService) UpsertAboutItem(ctx context.Context, item *store.AboutItem) (*store.AboutItem, error) {
	return nil, nil
}
func (s *stubProfileService) DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) UpdateAvatar(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) UpdateCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) FollowUser(ctx context.Context, followerID, followingID uuid.UUID) (*store.Follow, error) {
	return nil, nil
}
func (s *stubProfileService) UnfollowUser(ctx context.Context, followerID, followingID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) SendFriendRequest(ctx context.Context, requesterID, addresseeID uuid.UUID) (*store.Friendship, error) {
	return nil, nil
}
func (s *stubProfileService) RespondToFriendRequest(ctx context.Context, userID, friendshipID uuid.UUID, accept bool) (*store.Friendship, error) {
	return nil, nil
}
func (s *stubProfileService) ListFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) ListFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) ListFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendEntry, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) ListFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendRequestEntry, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) ListSentFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendRequestEntry, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) ListBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Block, int64, error) {
	return nil, 0, nil
}
func (s *stubProfileService) CancelFriendRequest(ctx context.Context, requesterID, friendshipID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) RemoveFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) BlockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) UnblockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	return nil
}
func (s *stubProfileService) GetRelationship(ctx context.Context, viewerID, targetID uuid.UUID) (*store.RelationshipStatus, error) {
	return nil, nil
}
func (s *stubProfileService) GetProfilesBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*store.Profile, error) {
	return nil, nil
}
func (s *stubProfileService) GetModuleProfile(ctx context.Context, userID uuid.UUID, module string) (*store.ModuleProfile, error) {
	return nil, nil
}
func (s *stubProfileService) GetModuleProfiles(ctx context.Context, userID uuid.UUID) ([]store.ModuleProfile, error) {
	return nil, nil
}
func (s *stubProfileService) UpsertModuleProfile(ctx context.Context, userID uuid.UUID, module string, params store.UpsertModuleProfileParams) (*store.ModuleProfile, error) {
	return nil, nil
}
func (s *stubProfileService) DeleteModuleProfile(ctx context.Context, userID uuid.UUID, module string) error {
	return nil
}
func (s *stubProfileService) ChangeHandle(ctx context.Context, userID uuid.UUID, newUsername string) (*store.Profile, error) {
	return nil, nil
}
func (s *stubProfileService) ResolveHandle(ctx context.Context, oldUsername string) (*uuid.UUID, *string, error) {
	return nil, nil, nil
}
func (s *stubProfileService) GetHandleHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.HandleHistoryEntry, error) {
	return nil, nil
}

func TestGetProfileInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/not-a-uuid", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}

func TestUpdateMeInvalidHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodPut, "/v1/profiles/me", bytes.NewBufferString(`{"display_name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
}

func TestChangeHandleMissingHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	body, _ := json.Marshal(map[string]string{"username": "newname"})
	req := httptest.NewRequest(http.MethodPut, "/v1/profiles/me/handle", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
}

func TestResolveHandleNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/resolve-handle/olduser", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, resp.Code)
	}
}
