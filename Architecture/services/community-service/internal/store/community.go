package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Community struct {
	ID               uuid.UUID       `json:"id"`
	OwnerID          uuid.UUID       `json:"owner_id"`
	Handle           string          `json:"handle"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	AvatarMediaID    *uuid.UUID      `json:"avatar_media_id,omitempty"`
	BannerMediaID    *uuid.UUID      `json:"banner_media_id,omitempty"`
	CommunityType    string          `json:"community_type"`
	Category         string          `json:"category"`
	Language         string          `json:"language"`
	JoinMode         string          `json:"join_mode"`
	EmailDomainGate  *string         `json:"email_domain_gate,omitempty"`
	JoinQuestions    json.RawMessage `json:"join_questions"`
	MemberDirectory  bool            `json:"member_directory"`
	CrossSpaceBans   bool            `json:"cross_space_bans"`
	MaxSubSpaces     int             `json:"max_sub_spaces"`
	Latitude         *float64        `json:"latitude,omitempty"`
	Longitude        *float64        `json:"longitude,omitempty"`
	LocationName     string          `json:"location_name"`
	Rules            []string        `json:"rules"`
	TopicTags        []string        `json:"topic_tags"`
	MemberCount      int64           `json:"member_count"`
	SpaceCount       int             `json:"space_count"`
	IsVerified       bool            `json:"is_verified"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	DeletedAt        *time.Time      `json:"deleted_at,omitempty"`
}

type CommunityMember struct {
	CommunityID uuid.UUID  `json:"community_id"`
	UserID      uuid.UUID  `json:"user_id"`
	Role        string     `json:"role"`
	JoinedAt    time.Time  `json:"joined_at"`
	BannedAt    *time.Time `json:"banned_at,omitempty"`
	BannedBy    *uuid.UUID `json:"banned_by,omitempty"`
	BanReason   *string    `json:"ban_reason,omitempty"`
}

type CommunitySpace struct {
	ID              uuid.UUID  `json:"id"`
	CommunityID     uuid.UUID  `json:"community_id"`
	SpaceType       string     `json:"space_type"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id,omitempty"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id,omitempty"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	SortOrder       int        `json:"sort_order"`
	IsQuarantined   bool       `json:"is_quarantined"`
	CreatedBy       uuid.UUID  `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
}

type CommunityJoinRequest struct {
	ID          uuid.UUID       `json:"id"`
	CommunityID uuid.UUID      `json:"community_id"`
	UserID      uuid.UUID       `json:"user_id"`
	Answers     json.RawMessage `json:"answers,omitempty"`
	Status      string          `json:"status"`
	ReviewedBy  *uuid.UUID      `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time      `json:"reviewed_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CommunityModlogEntry struct {
	ID          uuid.UUID       `json:"id"`
	CommunityID uuid.UUID      `json:"community_id"`
	ActorID     uuid.UUID       `json:"actor_id"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    uuid.UUID       `json:"target_id"`
	Reason      *string         `json:"reason,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CommunityAnnouncement struct {
	ID          uuid.UUID `json:"id"`
	CommunityID uuid.UUID `json:"community_id"`
	AuthorID    uuid.UUID `json:"author_id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	IsPinned    bool      `json:"is_pinned"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CommunityEvent struct {
	ID           uuid.UUID  `json:"id"`
	CommunityID  uuid.UUID  `json:"community_id"`
	SpaceID      *uuid.UUID `json:"space_id,omitempty"`
	CreatorID    uuid.UUID  `json:"creator_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Location     *string    `json:"location,omitempty"`
	StartsAt     time.Time  `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	MaxAttendees int        `json:"max_attendees"`
	RSVPCount    int        `json:"rsvp_count"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Community CRUD ---

func (s *Store) CreateCommunity(ctx context.Context, c *Community) error {
	query := `
		INSERT INTO communities (
			id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
			community_type, category, language, join_mode, email_domain_gate,
			join_questions, member_directory, cross_space_bans, max_sub_spaces,
			latitude, longitude, location_name, rules, topic_tags,
			member_count, space_count, is_verified, status
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23, $24, $25
		) RETURNING created_at, updated_at`
	return s.db.QueryRow(ctx, query,
		c.ID, c.OwnerID, c.Handle, c.Name, c.Description, c.AvatarMediaID, c.BannerMediaID,
		c.CommunityType, c.Category, c.Language, c.JoinMode, c.EmailDomainGate,
		c.JoinQuestions, c.MemberDirectory, c.CrossSpaceBans, c.MaxSubSpaces,
		c.Latitude, c.Longitude, c.LocationName, c.Rules, c.TopicTags,
		c.MemberCount, c.SpaceCount, c.IsVerified, c.Status,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
}

func (s *Store) GetCommunityByID(ctx context.Context, id uuid.UUID) (*Community, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		community_type, category, language, join_mode, email_domain_gate,
		join_questions, member_directory, cross_space_bans, max_sub_spaces,
		latitude, longitude, location_name, rules, topic_tags,
		member_count, space_count, is_verified, status, created_at, updated_at, deleted_at
		FROM communities WHERE id = $1 AND status != 'deleted'`
	c := &Community{}
	err := s.db.QueryRow(ctx, query, id).Scan(
		&c.ID, &c.OwnerID, &c.Handle, &c.Name, &c.Description, &c.AvatarMediaID, &c.BannerMediaID,
		&c.CommunityType, &c.Category, &c.Language, &c.JoinMode, &c.EmailDomainGate,
		&c.JoinQuestions, &c.MemberDirectory, &c.CrossSpaceBans, &c.MaxSubSpaces,
		&c.Latitude, &c.Longitude, &c.LocationName, &c.Rules, &c.TopicTags,
		&c.MemberCount, &c.SpaceCount, &c.IsVerified, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("community not found")
	}
	return c, err
}

func (s *Store) GetCommunityByHandle(ctx context.Context, handle string) (*Community, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		community_type, category, language, join_mode, email_domain_gate,
		join_questions, member_directory, cross_space_bans, max_sub_spaces,
		latitude, longitude, location_name, rules, topic_tags,
		member_count, space_count, is_verified, status, created_at, updated_at, deleted_at
		FROM communities WHERE handle = $1 AND status != 'deleted'`
	c := &Community{}
	err := s.db.QueryRow(ctx, query, handle).Scan(
		&c.ID, &c.OwnerID, &c.Handle, &c.Name, &c.Description, &c.AvatarMediaID, &c.BannerMediaID,
		&c.CommunityType, &c.Category, &c.Language, &c.JoinMode, &c.EmailDomainGate,
		&c.JoinQuestions, &c.MemberDirectory, &c.CrossSpaceBans, &c.MaxSubSpaces,
		&c.Latitude, &c.Longitude, &c.LocationName, &c.Rules, &c.TopicTags,
		&c.MemberCount, &c.SpaceCount, &c.IsVerified, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("community not found")
	}
	return c, err
}

func (s *Store) UpdateCommunity(ctx context.Context, c *Community) error {
	query := `UPDATE communities SET
		name = $2, description = $3, avatar_media_id = $4, banner_media_id = $5,
		community_type = $6, category = $7, language = $8, join_mode = $9,
		email_domain_gate = $10, join_questions = $11, member_directory = $12,
		cross_space_bans = $13, max_sub_spaces = $14,
		latitude = $15, longitude = $16, location_name = $17,
		rules = $18, topic_tags = $19, updated_at = NOW()
		WHERE id = $1 AND status != 'deleted'
		RETURNING updated_at`
	return s.db.QueryRow(ctx, query,
		c.ID, c.Name, c.Description, c.AvatarMediaID, c.BannerMediaID,
		c.CommunityType, c.Category, c.Language, c.JoinMode,
		c.EmailDomainGate, c.JoinQuestions, c.MemberDirectory,
		c.CrossSpaceBans, c.MaxSubSpaces,
		c.Latitude, c.Longitude, c.LocationName,
		c.Rules, c.TopicTags,
	).Scan(&c.UpdatedAt)
}

func (s *Store) DeleteCommunity(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE communities SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

func (s *Store) GetMyCommunities(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Community, error) {
	query := `SELECT c.id, c.owner_id, c.handle, c.name, c.description, c.avatar_media_id, c.banner_media_id,
		c.community_type, c.category, c.language, c.join_mode, c.email_domain_gate,
		c.join_questions, c.member_directory, c.cross_space_bans, c.max_sub_spaces,
		c.latitude, c.longitude, c.location_name, c.rules, c.topic_tags,
		c.member_count, c.space_count, c.is_verified, c.status, c.created_at, c.updated_at, c.deleted_at
		FROM communities c
		JOIN community_members cm ON cm.community_id = c.id
		WHERE cm.user_id = $1 AND c.status != 'deleted' AND cm.role NOT IN ('banned','pending')
		ORDER BY cm.joined_at DESC LIMIT $2 OFFSET $3`
	return s.scanCommunities(ctx, query, userID, limit, offset)
}

func (s *Store) DiscoverCommunities(ctx context.Context, limit, offset int) ([]Community, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		community_type, category, language, join_mode, email_domain_gate,
		join_questions, member_directory, cross_space_bans, max_sub_spaces,
		latitude, longitude, location_name, rules, topic_tags,
		member_count, space_count, is_verified, status, created_at, updated_at, deleted_at
		FROM communities
		WHERE status = 'active' AND community_type IN ('public','education','local','professional','fan','brand')
		ORDER BY member_count DESC, created_at DESC
		LIMIT $1 OFFSET $2`
	return s.scanCommunities(ctx, query, limit, offset)
}

func (s *Store) scanCommunities(ctx context.Context, query string, args ...any) ([]Community, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var communities []Community
	for rows.Next() {
		var c Community
		if err := rows.Scan(
			&c.ID, &c.OwnerID, &c.Handle, &c.Name, &c.Description, &c.AvatarMediaID, &c.BannerMediaID,
			&c.CommunityType, &c.Category, &c.Language, &c.JoinMode, &c.EmailDomainGate,
			&c.JoinQuestions, &c.MemberDirectory, &c.CrossSpaceBans, &c.MaxSubSpaces,
			&c.Latitude, &c.Longitude, &c.LocationName, &c.Rules, &c.TopicTags,
			&c.MemberCount, &c.SpaceCount, &c.IsVerified, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
		); err != nil {
			return nil, err
		}
		communities = append(communities, c)
	}
	return communities, rows.Err()
}

// --- Member operations ---

func (s *Store) AddMember(ctx context.Context, m *CommunityMember) error {
	query := `INSERT INTO community_members (community_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (community_id, user_id) DO UPDATE SET role = $3`
	_, err := s.db.Exec(ctx, query, m.CommunityID, m.UserID, m.Role)
	return err
}

func (s *Store) GetMember(ctx context.Context, communityID, userID uuid.UUID) (*CommunityMember, error) {
	query := `SELECT community_id, user_id, role, joined_at, banned_at, banned_by, ban_reason
		FROM community_members WHERE community_id = $1 AND user_id = $2`
	m := &CommunityMember{}
	err := s.db.QueryRow(ctx, query, communityID, userID).Scan(
		&m.CommunityID, &m.UserID, &m.Role, &m.JoinedAt, &m.BannedAt, &m.BannedBy, &m.BanReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (s *Store) UpdateMemberRole(ctx context.Context, communityID, userID uuid.UUID, role string) error {
	query := `UPDATE community_members SET role = $3 WHERE community_id = $1 AND user_id = $2`
	tag, err := s.db.Exec(ctx, query, communityID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("member not found")
	}
	return nil
}

func (s *Store) RemoveMember(ctx context.Context, communityID, userID uuid.UUID) error {
	query := `DELETE FROM community_members WHERE community_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, query, communityID, userID)
	return err
}

func (s *Store) BanMember(ctx context.Context, communityID, userID, bannedBy uuid.UUID, reason string) error {
	query := `UPDATE community_members SET role = 'banned', banned_at = NOW(), banned_by = $3, ban_reason = $4
		WHERE community_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, query, communityID, userID, bannedBy, reason)
	return err
}

func (s *Store) UnbanMember(ctx context.Context, communityID, userID uuid.UUID) error {
	query := `UPDATE community_members SET role = 'member', banned_at = NULL, banned_by = NULL, ban_reason = NULL
		WHERE community_id = $1 AND user_id = $2 AND role = 'banned'`
	tag, err := s.db.Exec(ctx, query, communityID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("member not found or not banned")
	}
	return nil
}

func (s *Store) ListMembers(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]CommunityMember, error) {
	query := `SELECT community_id, user_id, role, joined_at, banned_at, banned_by, ban_reason
		FROM community_members WHERE community_id = $1 AND role NOT IN ('banned','pending')
		ORDER BY joined_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, query, communityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []CommunityMember
	for rows.Next() {
		var m CommunityMember
		if err := rows.Scan(&m.CommunityID, &m.UserID, &m.Role, &m.JoinedAt, &m.BannedAt, &m.BannedBy, &m.BanReason); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) CountMembers(ctx context.Context, communityID uuid.UUID) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM community_members WHERE community_id = $1 AND role NOT IN ('banned','pending')`, communityID).Scan(&count)
	return count, err
}

func (s *Store) IncrementMemberCount(ctx context.Context, communityID uuid.UUID, delta int) error {
	query := `UPDATE communities SET member_count = member_count + $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, communityID, delta)
	return err
}

func (s *Store) IncrementSpaceCount(ctx context.Context, communityID uuid.UUID, delta int) error {
	query := `UPDATE communities SET space_count = space_count + $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, communityID, delta)
	return err
}

// --- Space operations ---

func (s *Store) CreateSpace(ctx context.Context, sp *CommunitySpace) error {
	query := `INSERT INTO community_spaces (
		id, community_id, space_type, linked_group_id, linked_channel_id,
		name, description, sort_order, is_quarantined, created_by
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	RETURNING created_at`
	return s.db.QueryRow(ctx, query,
		sp.ID, sp.CommunityID, sp.SpaceType, sp.LinkedGroupID, sp.LinkedChannelID,
		sp.Name, sp.Description, sp.SortOrder, sp.IsQuarantined, sp.CreatedBy,
	).Scan(&sp.CreatedAt)
}

func (s *Store) GetSpace(ctx context.Context, id uuid.UUID) (*CommunitySpace, error) {
	query := `SELECT id, community_id, space_type, linked_group_id, linked_channel_id,
		name, description, sort_order, is_quarantined, created_by, created_at
		FROM community_spaces WHERE id = $1`
	sp := &CommunitySpace{}
	err := s.db.QueryRow(ctx, query, id).Scan(
		&sp.ID, &sp.CommunityID, &sp.SpaceType, &sp.LinkedGroupID, &sp.LinkedChannelID,
		&sp.Name, &sp.Description, &sp.SortOrder, &sp.IsQuarantined, &sp.CreatedBy, &sp.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("space not found")
	}
	return sp, err
}

func (s *Store) UpdateSpace(ctx context.Context, sp *CommunitySpace) error {
	query := `UPDATE community_spaces SET
		name = $2, description = $3, sort_order = $4,
		linked_group_id = $5, linked_channel_id = $6
		WHERE id = $1
		RETURNING created_at`
	return s.db.QueryRow(ctx, query,
		sp.ID, sp.Name, sp.Description, sp.SortOrder,
		sp.LinkedGroupID, sp.LinkedChannelID,
	).Scan(&sp.CreatedAt)
}

func (s *Store) DeleteSpace(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM community_spaces WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

func (s *Store) ListSpaces(ctx context.Context, communityID uuid.UUID) ([]CommunitySpace, error) {
	query := `SELECT id, community_id, space_type, linked_group_id, linked_channel_id,
		name, description, sort_order, is_quarantined, created_by, created_at
		FROM community_spaces WHERE community_id = $1
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := s.db.Query(ctx, query, communityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var spaces []CommunitySpace
	for rows.Next() {
		var sp CommunitySpace
		if err := rows.Scan(
			&sp.ID, &sp.CommunityID, &sp.SpaceType, &sp.LinkedGroupID, &sp.LinkedChannelID,
			&sp.Name, &sp.Description, &sp.SortOrder, &sp.IsQuarantined, &sp.CreatedBy, &sp.CreatedAt,
		); err != nil {
			return nil, err
		}
		spaces = append(spaces, sp)
	}
	return spaces, rows.Err()
}

func (s *Store) QuarantineSpace(ctx context.Context, id uuid.UUID, quarantined bool) error {
	query := `UPDATE community_spaces SET is_quarantined = $2 WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id, quarantined)
	return err
}

func (s *Store) CountSpaces(ctx context.Context, communityID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM community_spaces WHERE community_id = $1`, communityID).Scan(&count)
	return count, err
}

// --- Join Request operations ---

func (s *Store) CreateJoinRequest(ctx context.Context, jr *CommunityJoinRequest) error {
	query := `INSERT INTO community_join_requests (id, community_id, user_id, answers, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`
	return s.db.QueryRow(ctx, query, jr.ID, jr.CommunityID, jr.UserID, jr.Answers, jr.Status).Scan(&jr.CreatedAt)
}

func (s *Store) GetJoinRequest(ctx context.Context, id uuid.UUID) (*CommunityJoinRequest, error) {
	query := `SELECT id, community_id, user_id, answers, status, reviewed_by, reviewed_at, created_at
		FROM community_join_requests WHERE id = $1`
	jr := &CommunityJoinRequest{}
	err := s.db.QueryRow(ctx, query, id).Scan(
		&jr.ID, &jr.CommunityID, &jr.UserID, &jr.Answers, &jr.Status, &jr.ReviewedBy, &jr.ReviewedAt, &jr.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("join request not found")
	}
	return jr, err
}

func (s *Store) ListJoinRequests(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]CommunityJoinRequest, error) {
	query := `SELECT id, community_id, user_id, answers, status, reviewed_by, reviewed_at, created_at
		FROM community_join_requests WHERE community_id = $1 AND status = 'pending'
		ORDER BY created_at ASC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, query, communityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []CommunityJoinRequest
	for rows.Next() {
		var jr CommunityJoinRequest
		if err := rows.Scan(&jr.ID, &jr.CommunityID, &jr.UserID, &jr.Answers, &jr.Status, &jr.ReviewedBy, &jr.ReviewedAt, &jr.CreatedAt); err != nil {
			return nil, err
		}
		requests = append(requests, jr)
	}
	return requests, rows.Err()
}

func (s *Store) ApproveRequest(ctx context.Context, id, reviewerID uuid.UUID) error {
	query := `UPDATE community_join_requests SET status = 'approved', reviewed_by = $2, reviewed_at = NOW()
		WHERE id = $1 AND status = 'pending'`
	tag, err := s.db.Exec(ctx, query, id, reviewerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("join request not found or already reviewed")
	}
	return nil
}

func (s *Store) RejectRequest(ctx context.Context, id, reviewerID uuid.UUID) error {
	query := `UPDATE community_join_requests SET status = 'rejected', reviewed_by = $2, reviewed_at = NOW()
		WHERE id = $1 AND status = 'pending'`
	tag, err := s.db.Exec(ctx, query, id, reviewerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("join request not found or already reviewed")
	}
	return nil
}

// --- Modlog operations ---

func (s *Store) AddModlogEntry(ctx context.Context, entry *CommunityModlogEntry) error {
	query := `INSERT INTO community_modlog (id, community_id, actor_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`
	return s.db.QueryRow(ctx, query,
		entry.ID, entry.CommunityID, entry.ActorID, entry.Action,
		entry.TargetType, entry.TargetID, entry.Reason, entry.Metadata,
	).Scan(&entry.CreatedAt)
}

func (s *Store) ListModlog(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]CommunityModlogEntry, error) {
	query := `SELECT id, community_id, actor_id, action, target_type, target_id, reason, metadata, created_at
		FROM community_modlog WHERE community_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, query, communityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []CommunityModlogEntry
	for rows.Next() {
		var e CommunityModlogEntry
		if err := rows.Scan(&e.ID, &e.CommunityID, &e.ActorID, &e.Action, &e.TargetType, &e.TargetID, &e.Reason, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- Space Follow operations ---

// AutoFollowDefaultSpaces auto-follows a user to default/announcements/welcome spaces
// when they join a community.
func (s *Store) AutoFollowDefaultSpaces(ctx context.Context, communityID, userID uuid.UUID) error {
	// Find spaces that are announcements, welcome, or marked as default
	rows, err := s.db.Query(ctx, `
		SELECT id FROM community_spaces
		WHERE community_id = $1
		  AND (space_type IN ('announcements', 'welcome') OR is_default = TRUE)
	`, communityID)
	if err != nil {
		return fmt.Errorf("failed to query default spaces: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var spaceID uuid.UUID
		if err := rows.Scan(&spaceID); err != nil {
			return fmt.Errorf("failed to scan space: %w", err)
		}
		_, err := s.db.Exec(ctx, `
			INSERT INTO community_space_follows (community_id, space_id, user_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (community_id, space_id, user_id) DO NOTHING
		`, communityID, spaceID, userID.String())
		if err != nil {
			return fmt.Errorf("failed to auto-follow space %s: %w", spaceID, err)
		}
	}
	return rows.Err()
}

// FollowSpace allows a user to follow a specific space.
func (s *Store) FollowSpace(ctx context.Context, communityID, spaceID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO community_space_follows (community_id, space_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (community_id, space_id, user_id) DO NOTHING
	`, communityID, spaceID, userID.String())
	return err
}

// UnfollowSpace removes a user's follow on a specific space.
func (s *Store) UnfollowSpace(ctx context.Context, communityID, spaceID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM community_space_follows
		WHERE community_id = $1 AND space_id = $2 AND user_id = $3
	`, communityID, spaceID, userID.String())
	return err
}

// GetFollowedSpaces returns space IDs a user follows in a community.
func (s *Store) GetFollowedSpaces(ctx context.Context, communityID, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT space_id FROM community_space_follows
		WHERE community_id = $1 AND user_id = $2
	`, communityID, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var spaceIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		spaceIDs = append(spaceIDs, id)
	}
	return spaceIDs, rows.Err()
}

// --- GDPR helpers ---

func (s *Store) RemoveUserFromAll(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM community_members WHERE user_id = $1`
	_, err := s.db.Exec(ctx, query, userID)
	return err
}

func (s *Store) ListCommunitiesWhereUserIsOnlyOwner(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT cm.community_id FROM community_members cm
		WHERE cm.user_id = $1 AND cm.role = 'owner'
		AND NOT EXISTS (
			SELECT 1 FROM community_members cm2
			WHERE cm2.community_id = cm.community_id AND cm2.role = 'owner' AND cm2.user_id != $1
		)`
	rows, err := s.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ArchiveCommunity(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE communities SET status = 'archived', updated_at = NOW() WHERE id = $1`
	_, err := s.db.Exec(ctx, query, id)
	return err
}

func (s *Store) CountCommunitiesByOwner(ctx context.Context, ownerID uuid.UUID, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM communities WHERE owner_id = $1 AND created_at >= $2 AND status != 'deleted'`, ownerID, since).Scan(&count)
	return count, err
}
