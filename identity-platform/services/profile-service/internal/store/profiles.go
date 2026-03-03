package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Profile represents a user's public-facing profile.
type Profile struct {
	UserID            uuid.UUID  `json:"user_id"`
	Username          *string    `json:"username,omitempty"`
	DisplayName       string     `json:"display_name"`
	FirstName         *string    `json:"first_name,omitempty"`
	LastName          *string    `json:"last_name,omitempty"`
	PreferredName     *string    `json:"preferred_name,omitempty"`
	Pronouns          *string    `json:"pronouns,omitempty"`
	Bio               string     `json:"bio"`
	DoB               *time.Time `json:"dob,omitempty"`
	Gender            *string    `json:"gender,omitempty"`
	AvatarMediaID     *uuid.UUID `json:"avatar_media_id,omitempty"`
	CoverMediaID      *uuid.UUID `json:"cover_media_id,omitempty"`
	Category          string     `json:"category"`
	Profession        string     `json:"profession"`
	Website           string     `json:"website"`
	Location          string     `json:"location"`
	BadgeFlags        int        `json:"badge_flags"`
	IsVerified        bool       `json:"is_verified"`
	VerificationLevel string     `json:"verification_level"`
	StatusText        *string    `json:"status_text,omitempty"`
	StatusEmoji       *string    `json:"status_emoji,omitempty"`
	StatusExpiresAt   *time.Time `json:"status_expires_at,omitempty"`
	ProfileThemeColor string     `json:"profile_theme_color"`
	IntroMediaURL     *string    `json:"intro_media_url,omitempty"`
	IntroMediaType    *string    `json:"intro_media_type,omitempty"`
	CTALabel          *string    `json:"cta_label,omitempty"`
	CTAURL            *string    `json:"cta_url,omitempty"`
	MemberSinceBadge  bool       `json:"member_since_badge"`
	Timezone          *string    `json:"timezone,omitempty"`
	FollowerCount     int64      `json:"follower_count"`
	FollowingCount    int64      `json:"following_count"`
	FriendCount       int64      `json:"friend_count"`
	PostCount         int64      `json:"post_count"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// UserLink represents a social/external link on a user's profile (legacy table).
type UserLink struct {
	UserID       uuid.UUID `json:"user_id"`
	Platform     string    `json:"platform"`
	URL          string    `json:"url"`
	DisplayLabel string    `json:"display_label"`
	SortOrder    int       `json:"sort_order"`
}

// ProfileLink represents a link on a user's profile (new table with UUID PK).
type ProfileLink struct {
	ID         uuid.UUID `json:"id"`
	ProfileID  uuid.UUID `json:"profile_id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Icon       *string   `json:"icon,omitempty"`
	Category   *string   `json:"category,omitempty"`
	SortOrder  int       `json:"sort_order"`
	ClickCount int64     `json:"click_count"`
	IsPinned   bool      `json:"is_pinned"`
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"created_at"`
}

// Follow represents a follow relationship.
type Follow struct {
	ID          uuid.UUID `json:"id"`
	FollowerID  uuid.UUID `json:"follower_id"`
	FollowingID uuid.UUID `json:"following_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Friendship represents a friend request/relationship.
type Friendship struct {
	ID          uuid.UUID `json:"id"`
	RequesterID uuid.UUID `json:"requester_id"`
	AddresseeID uuid.UUID `json:"addressee_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Block represents a block relationship.
type Block struct {
	ID        uuid.UUID `json:"id"`
	BlockerID uuid.UUID `json:"blocker_id"`
	BlockedID uuid.UUID `json:"blocked_id"`
	CreatedAt time.Time `json:"created_at"`
}

// FollowerEntry represents a user in a followers/following list with basic profile info.
type FollowerEntry struct {
	UserID        uuid.UUID  `json:"user_id"`
	DisplayName   string     `json:"display_name"`
	Username      *string    `json:"username,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	FollowedAt    time.Time  `json:"followed_at"`
}

// FriendEntry represents a user in the circle list (only accepted members).
type FriendEntry struct {
	UserID        uuid.UUID  `json:"user_id"`
	DisplayName   string     `json:"display_name"`
	Username      *string    `json:"username,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	FriendSince   time.Time  `json:"friend_since"`
}

// FriendRequestEntry represents a friend request with the other user's profile info.
type FriendRequestEntry struct {
	FriendshipID  uuid.UUID  `json:"friendship_id"`
	UserID        uuid.UUID  `json:"user_id"`
	DisplayName   string     `json:"display_name"`
	Username      *string    `json:"username,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// RelationshipStatus represents the full relationship state between two users.
type RelationshipStatus struct {
	Following             bool    `json:"following"`
	FollowedBy            bool    `json:"followed_by"`
	InCircle              bool    `json:"in_circle"`
	CircleRequestSent     bool    `json:"circle_request_sent"`
	CircleRequestReceived bool    `json:"circle_request_received"`
	CircleRequestID       *string `json:"circle_request_id,omitempty"`
	Blocked               bool    `json:"blocked"`
	BlockedBy             bool    `json:"blocked_by"`
	CanDM                 bool    `json:"can_dm"`
	CanSeeOnline          bool    `json:"can_see_online"`
	CanAddToGroup         bool    `json:"can_add_to_group"`
	MutualCircleCount     int     `json:"mutual_circle_count"`
}

// AboutItem represents one item in a user's about section.
type AboutItem struct {
	UserID     uuid.UUID              `json:"user_id"`
	Section    string                 `json:"section"`
	ItemID     uuid.UUID              `json:"item_id"`
	Data       map[string]interface{} `json:"data"`
	Visibility string                 `json:"visibility"`
	SortOrder  int                    `json:"sort_order"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// allProfileCols is the column list shared across SELECT queries.
const allProfileCols = `user_id, username, display_name, first_name, last_name,
	preferred_name, pronouns, bio, dob, gender,
	avatar_media_id, cover_media_id, category, profession, website, location, badge_flags,
	is_verified, verification_level, status_text, status_emoji, status_expires_at,
	profile_theme_color, intro_media_url, intro_media_type, cta_label, cta_url,
	member_since_badge, timezone,
	follower_count, following_count, friend_count, post_count,
	created_at, updated_at`

func scanProfile(row pgx.Row) (*Profile, error) {
	var p Profile
	err := row.Scan(
		&p.UserID, &p.Username, &p.DisplayName, &p.FirstName, &p.LastName,
		&p.PreferredName, &p.Pronouns, &p.Bio, &p.DoB, &p.Gender,
		&p.AvatarMediaID, &p.CoverMediaID, &p.Category, &p.Profession, &p.Website, &p.Location, &p.BadgeFlags,
		&p.IsVerified, &p.VerificationLevel, &p.StatusText, &p.StatusEmoji, &p.StatusExpiresAt,
		&p.ProfileThemeColor, &p.IntroMediaURL, &p.IntroMediaType, &p.CTALabel, &p.CTAURL,
		&p.MemberSinceBadge, &p.Timezone,
		&p.FollowerCount, &p.FollowingCount, &p.FriendCount, &p.PostCount,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// ---------------------------------------------------------------
// Profiles CRUD
// ---------------------------------------------------------------

// CreateProfile creates a default profile (called by event consumer on UserRegistered).
func (s *Store) CreateProfile(ctx context.Context, userID uuid.UUID, displayName, firstName, lastName, dob, gender string) error {
	now := time.Now()

	var firstNamePtr, lastNamePtr, genderPtr *string
	if firstName != "" {
		firstNamePtr = &firstName
	}
	if lastName != "" {
		lastNamePtr = &lastName
	}
	if gender != "" {
		genderPtr = &gender
	}

	var dobPtr *time.Time
	if dob != "" {
		if t, err := time.Parse("2006-01-02", dob); err == nil {
			dobPtr = &t
		}
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO profile.profiles
			(user_id, display_name, first_name, last_name, bio, dob, gender, category, profession, website, location, badge_flags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '', $5, $6, 'personal', '', '', '', 0, $7, $7)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, displayName, firstNamePtr, lastNamePtr, dobPtr, genderPtr, now)
	return err
}

// GetProfile returns a user's profile by ID.
func (s *Store) GetProfile(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `SELECT `+allProfileCols+` FROM profile.profiles WHERE user_id = $1`, userID))
}

// GetProfileByUsername returns a user's profile by username.
func (s *Store) GetProfileByUsername(ctx context.Context, username string) (*Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `SELECT `+allProfileCols+` FROM profile.profiles WHERE username = $1`, username))
}

// ListProfiles returns a paginated list of all profiles ordered by creation date.
func (s *Store) ListProfiles(ctx context.Context, limit, offset int) ([]Profile, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM profile.profiles`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `SELECT `+allProfileCols+` FROM profile.profiles ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		var p Profile
		err := rows.Scan(
			&p.UserID, &p.Username, &p.DisplayName, &p.FirstName, &p.LastName,
			&p.PreferredName, &p.Pronouns, &p.Bio, &p.DoB, &p.Gender,
			&p.AvatarMediaID, &p.CoverMediaID, &p.Category, &p.Profession, &p.Website, &p.Location, &p.BadgeFlags,
			&p.IsVerified, &p.VerificationLevel, &p.StatusText, &p.StatusEmoji, &p.StatusExpiresAt,
			&p.ProfileThemeColor, &p.IntroMediaURL, &p.IntroMediaType, &p.CTALabel, &p.CTAURL,
			&p.MemberSinceBadge, &p.Timezone,
			&p.FollowerCount, &p.FollowingCount, &p.FriendCount, &p.PostCount,
			&p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		profiles = append(profiles, p)
	}
	return profiles, total, rows.Err()
}

// UpdateProfileParams groups all fields that can be updated on a profile.
type UpdateProfileParams struct {
	DisplayName       string
	Bio               string
	AvatarMediaID     *uuid.UUID
	CoverMediaID      *uuid.UUID
	FirstName         *string
	LastName          *string
	PreferredName     *string
	Pronouns          *string
	Gender            *string
	DoB               *time.Time
	Username          *string
	Category          string
	Profession        string
	Website           string
	Location          string
	StatusText        *string
	StatusEmoji       *string
	StatusExpiresAt   *time.Time
	ProfileThemeColor string
	IntroMediaURL     *string
	IntroMediaType    *string
	CTALabel          *string
	CTAURL            *string
	MemberSinceBadge  *bool
	Timezone          *string
}

// UpdateProfile updates editable profile fields.
func (s *Store) UpdateProfile(ctx context.Context, userID uuid.UUID, p UpdateProfileParams) (*Profile, error) {
	return scanProfile(s.db.QueryRow(ctx, `
		UPDATE profile.profiles
		SET display_name = $2, bio = $3, avatar_media_id = COALESCE($4, avatar_media_id), cover_media_id = COALESCE($5, cover_media_id),
			first_name = $6, last_name = $7, preferred_name = $8, pronouns = $9,
			gender = $10, dob = $11, username = $12,
			category = $13, profession = $14, website = $15, location = $16,
			status_text = $17, status_emoji = $18, status_expires_at = $19,
			profile_theme_color = $20, intro_media_url = $21, intro_media_type = $22,
			cta_label = $23, cta_url = $24, member_since_badge = COALESCE($25, member_since_badge),
			timezone = $26,
			updated_at = NOW()
		WHERE user_id = $1
		RETURNING `+allProfileCols,
		userID, p.DisplayName, p.Bio, p.AvatarMediaID, p.CoverMediaID,
		p.FirstName, p.LastName, p.PreferredName, p.Pronouns,
		p.Gender, p.DoB, p.Username,
		p.Category, p.Profession, p.Website, p.Location,
		p.StatusText, p.StatusEmoji, p.StatusExpiresAt,
		p.ProfileThemeColor, p.IntroMediaURL, p.IntroMediaType,
		p.CTALabel, p.CTAURL, p.MemberSinceBadge,
		p.Timezone,
	))
}

// ---------------------------------------------------------------
// User Links
// ---------------------------------------------------------------

// GetUserLinks returns all links for a user, ordered by sort_order.
func (s *Store) GetUserLinks(ctx context.Context, userID uuid.UUID) ([]UserLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, platform, url, display_label, sort_order
		FROM profile.user_links
		WHERE user_id = $1
		ORDER BY sort_order
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []UserLink
	for rows.Next() {
		var l UserLink
		if err := rows.Scan(&l.UserID, &l.Platform, &l.URL, &l.DisplayLabel, &l.SortOrder); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// UpsertUserLinks replaces all links for a user within a transaction.
func (s *Store) UpsertUserLinks(ctx context.Context, userID uuid.UUID, links []UserLink) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM profile.user_links WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}

	for _, l := range links {
		_, err = tx.Exec(ctx, `
			INSERT INTO profile.user_links (user_id, platform, url, display_label, sort_order)
			VALUES ($1, $2, $3, $4, $5)
		`, userID, l.Platform, l.URL, l.DisplayLabel, l.SortOrder)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ---------------------------------------------------------------
// User About
// ---------------------------------------------------------------

// GetAllAbout returns all about items for a user, grouped by section.
func (s *Store) GetAllAbout(ctx context.Context, userID uuid.UUID) ([]AboutItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
		FROM profile.user_about
		WHERE user_id = $1
		ORDER BY section, sort_order
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AboutItem
	for rows.Next() {
		var a AboutItem
		if err := rows.Scan(&a.UserID, &a.Section, &a.ItemID, &a.Data, &a.Visibility, &a.SortOrder, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// GetAboutBySection returns about items for a specific section.
func (s *Store) GetAboutBySection(ctx context.Context, userID uuid.UUID, section string) ([]AboutItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
		FROM profile.user_about
		WHERE user_id = $1 AND section = $2
		ORDER BY sort_order
	`, userID, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AboutItem
	for rows.Next() {
		var a AboutItem
		if err := rows.Scan(&a.UserID, &a.Section, &a.ItemID, &a.Data, &a.Visibility, &a.SortOrder, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// UpsertAboutItem creates or updates a single about item.
func (s *Store) UpsertAboutItem(ctx context.Context, item *AboutItem) (*AboutItem, error) {
	if item.ItemID == uuid.Nil {
		item.ItemID = uuid.New()
	}
	now := time.Now()

	var a AboutItem
	err := s.db.QueryRow(ctx, `
		INSERT INTO profile.user_about (user_id, section, item_id, data, visibility, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (user_id, section, item_id) DO UPDATE
		SET data = EXCLUDED.data, visibility = EXCLUDED.visibility, sort_order = EXCLUDED.sort_order, updated_at = NOW()
		RETURNING user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
	`, item.UserID, item.Section, item.ItemID, item.Data, item.Visibility, item.SortOrder, now).
		Scan(&a.UserID, &a.Section, &a.ItemID, &a.Data, &a.Visibility, &a.SortOrder, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------------------------------------------------------------
// Avatar / Cover
// ---------------------------------------------------------------

// UpdateAvatar sets the avatar_media_id for a user's profile.
func (s *Store) UpdateAvatar(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.profiles SET avatar_media_id = $1, updated_at = NOW() WHERE user_id = $2
	`, mediaID, userID)
	return err
}

// UpdateCover sets the cover_media_id for a user's profile.
func (s *Store) UpdateCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.profiles SET cover_media_id = $1, updated_at = NOW() WHERE user_id = $2
	`, mediaID, userID)
	return err
}

// DeleteAboutItem deletes a single about item.
func (s *Store) DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM profile.user_about WHERE user_id = $1 AND section = $2 AND item_id = $3
	`, userID, section, itemID)
	return err
}

// ---------------------------------------------------------------
// Profile Links (new table with UUID PK)
// ---------------------------------------------------------------

func (s *Store) GetProfileLinks(ctx context.Context, profileID uuid.UUID) ([]ProfileLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, profile_id, title, url, icon, category, sort_order, click_count, is_pinned, visibility, created_at
		FROM profile.profile_links
		WHERE profile_id = $1
		ORDER BY sort_order
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []ProfileLink
	for rows.Next() {
		var l ProfileLink
		if err := rows.Scan(&l.ID, &l.ProfileID, &l.Title, &l.URL, &l.Icon, &l.Category,
			&l.SortOrder, &l.ClickCount, &l.IsPinned, &l.Visibility, &l.CreatedAt); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func (s *Store) CreateProfileLink(ctx context.Context, l *ProfileLink) (*ProfileLink, error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	var out ProfileLink
	err := s.db.QueryRow(ctx, `
		INSERT INTO profile.profile_links (id, profile_id, title, url, icon, category, sort_order, is_pinned, visibility)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, profile_id, title, url, icon, category, sort_order, click_count, is_pinned, visibility, created_at
	`, l.ID, l.ProfileID, l.Title, l.URL, l.Icon, l.Category, l.SortOrder, l.IsPinned, l.Visibility).
		Scan(&out.ID, &out.ProfileID, &out.Title, &out.URL, &out.Icon, &out.Category,
			&out.SortOrder, &out.ClickCount, &out.IsPinned, &out.Visibility, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) UpdateProfileLink(ctx context.Context, linkID, profileID uuid.UUID, title, url string, icon, category *string, sortOrder int, isPinned bool, visibility string) (*ProfileLink, error) {
	var out ProfileLink
	err := s.db.QueryRow(ctx, `
		UPDATE profile.profile_links
		SET title = $3, url = $4, icon = $5, category = $6, sort_order = $7, is_pinned = $8, visibility = $9
		WHERE id = $1 AND profile_id = $2
		RETURNING id, profile_id, title, url, icon, category, sort_order, click_count, is_pinned, visibility, created_at
	`, linkID, profileID, title, url, icon, category, sortOrder, isPinned, visibility).
		Scan(&out.ID, &out.ProfileID, &out.Title, &out.URL, &out.Icon, &out.Category,
			&out.SortOrder, &out.ClickCount, &out.IsPinned, &out.Visibility, &out.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *Store) DeleteProfileLink(ctx context.Context, linkID, profileID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM profile.profile_links WHERE id = $1 AND profile_id = $2
	`, linkID, profileID)
	return err
}

func (s *Store) IncrementLinkClick(ctx context.Context, linkID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.profile_links SET click_count = click_count + 1 WHERE id = $1
	`, linkID)
	return err
}

// ---------------------------------------------------------------
// Follows
// ---------------------------------------------------------------

func (s *Store) CreateFollow(ctx context.Context, followerID, followingID uuid.UUID) (*Follow, error) {
	var f Follow
	err := s.db.QueryRow(ctx, `
		INSERT INTO profile.follows (id, follower_id, following_id, status)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT (follower_id, following_id) DO UPDATE SET status = 'active'
		RETURNING id, follower_id, following_id, status, created_at
	`, uuid.New(), followerID, followingID).
		Scan(&f.ID, &f.FollowerID, &f.FollowingID, &f.Status, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) DeleteFollow(ctx context.Context, followerID, followingID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM profile.follows WHERE follower_id = $1 AND following_id = $2
	`, followerID, followingID)
	return err
}

func (s *Store) GetFollowStatus(ctx context.Context, followerID, followingID uuid.UUID) (*Follow, error) {
	var f Follow
	err := s.db.QueryRow(ctx, `
		SELECT id, follower_id, following_id, status, created_at
		FROM profile.follows
		WHERE follower_id = $1 AND following_id = $2
	`, followerID, followingID).Scan(&f.ID, &f.FollowerID, &f.FollowingID, &f.Status, &f.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (s *Store) IncrementFollowerCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET follower_count = follower_count + 1 WHERE user_id = $1`, userID)
	return err
}

func (s *Store) DecrementFollowerCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET follower_count = GREATEST(follower_count - 1, 0) WHERE user_id = $1`, userID)
	return err
}

func (s *Store) IncrementFollowingCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET following_count = following_count + 1 WHERE user_id = $1`, userID)
	return err
}

func (s *Store) DecrementFollowingCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET following_count = GREATEST(following_count - 1, 0) WHERE user_id = $1`, userID)
	return err
}

// ---------------------------------------------------------------
// Friendships
// ---------------------------------------------------------------

func (s *Store) CreateFriendRequest(ctx context.Context, requesterID, addresseeID uuid.UUID) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		INSERT INTO profile.friendships (id, requester_id, addressee_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', NOW(), NOW())
		RETURNING id, requester_id, addressee_id, status, created_at, updated_at
	`, uuid.New(), requesterID, addresseeID).
		Scan(&f.ID, &f.RequesterID, &f.AddresseeID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) UpdateFriendshipStatus(ctx context.Context, friendshipID uuid.UUID, status string) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		UPDATE profile.friendships
		SET status = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id, requester_id, addressee_id, status, created_at, updated_at
	`, friendshipID, status).
		Scan(&f.ID, &f.RequesterID, &f.AddresseeID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (s *Store) GetFriendship(ctx context.Context, friendshipID uuid.UUID) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		SELECT id, requester_id, addressee_id, status, created_at, updated_at
		FROM profile.friendships
		WHERE id = $1
	`, friendshipID).Scan(&f.ID, &f.RequesterID, &f.AddresseeID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// GetFriendshipBetween returns any friendship row between two users (in either direction).
func (s *Store) GetFriendshipBetween(ctx context.Context, userA, userB uuid.UUID) (*Friendship, error) {
	var f Friendship
	err := s.db.QueryRow(ctx, `
		SELECT id, requester_id, addressee_id, status, created_at, updated_at
		FROM profile.friendships
		WHERE (requester_id = $1 AND addressee_id = $2)
		   OR (requester_id = $2 AND addressee_id = $1)
		ORDER BY created_at DESC
		LIMIT 1
	`, userA, userB).Scan(&f.ID, &f.RequesterID, &f.AddresseeID, &f.Status, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// DeleteFriendshipByID removes a friendship row by its ID.
func (s *Store) DeleteFriendshipByID(ctx context.Context, friendshipID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM profile.friendships WHERE id = $1`, friendshipID)
	return err
}

func (s *Store) IncrementFriendCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET friend_count = friend_count + 1 WHERE user_id = $1`, userID)
	return err
}

func (s *Store) DecrementFriendCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE profile.profiles SET friend_count = GREATEST(friend_count - 1, 0) WHERE user_id = $1`, userID)
	return err
}

// ---------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------

func (s *Store) CreateBlock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO profile.blocks (id, blocker_id, blocked_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (blocker_id, blocked_id) DO NOTHING
	`, uuid.New(), blockerID, blockedID)
	return err
}

func (s *Store) DeleteBlock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM profile.blocks WHERE blocker_id = $1 AND blocked_id = $2
	`, blockerID, blockedID)
	return err
}

func (s *Store) IsBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM profile.blocks WHERE blocker_id = $1 AND blocked_id = $2)
	`, blockerID, blockedID).Scan(&exists)
	return exists, err
}

// GetBlockBidirectional checks if either user has blocked the other.
func (s *Store) GetBlockBidirectional(ctx context.Context, userAID, userBID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM profile.blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		)
	`, userAID, userBID).Scan(&exists)
	return exists, err
}

// ListBlocks returns users blocked by the given user with pagination.
func (s *Store) ListBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Block, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM profile.blocks WHERE blocker_id = $1
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, blocker_id, blocked_id, created_at
		FROM profile.blocks
		WHERE blocker_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var blocks []Block
	for rows.Next() {
		var b Block
		if err := rows.Scan(&b.ID, &b.BlockerID, &b.BlockedID, &b.CreatedAt); err != nil {
			return nil, 0, err
		}
		blocks = append(blocks, b)
	}
	return blocks, total, rows.Err()
}

// ---------------------------------------------------------------
// Social List Queries
// ---------------------------------------------------------------

// ListFollowers returns users who follow the given userID, with basic profile info.
func (s *Store) ListFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]FollowerEntry, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM profile.follows WHERE following_id = $1 AND status = 'active'
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, p.display_name, p.username, p.avatar_media_id, f.created_at
		FROM profile.follows f
		JOIN profile.profiles p ON p.user_id = f.follower_id
		WHERE f.following_id = $1 AND f.status = 'active'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []FollowerEntry
	for rows.Next() {
		var e FollowerEntry
		if err := rows.Scan(&e.UserID, &e.DisplayName, &e.Username, &e.AvatarMediaID, &e.FollowedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ListFollowing returns users that the given userID follows, with basic profile info.
func (s *Store) ListFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]FollowerEntry, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM profile.follows WHERE follower_id = $1 AND status = 'active'
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, p.display_name, p.username, p.avatar_media_id, f.created_at
		FROM profile.follows f
		JOIN profile.profiles p ON p.user_id = f.following_id
		WHERE f.follower_id = $1 AND f.status = 'active'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []FollowerEntry
	for rows.Next() {
		var e FollowerEntry
		if err := rows.Scan(&e.UserID, &e.DisplayName, &e.Username, &e.AvatarMediaID, &e.FollowedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ListFriends returns only accepted circle members for the given userID.
// Only friendships with status='accepted' are returned — pending/rejected are excluded.
func (s *Store) ListFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]FriendEntry, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM profile.friendships
		WHERE status = 'accepted'
		  AND (requester_id = $1 OR addressee_id = $1)
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, p.display_name, p.username, p.avatar_media_id, fr.updated_at
		FROM profile.friendships fr
		JOIN profile.profiles p ON p.user_id = CASE
			WHEN fr.requester_id = $1 THEN fr.addressee_id
			ELSE fr.requester_id
		END
		WHERE fr.status = 'accepted'
		  AND (fr.requester_id = $1 OR fr.addressee_id = $1)
		ORDER BY fr.updated_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []FriendEntry
	for rows.Next() {
		var e FriendEntry
		if err := rows.Scan(&e.UserID, &e.DisplayName, &e.Username, &e.AvatarMediaID, &e.FriendSince); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ListFriendRequests returns pending incoming friend requests for the given userID, enriched with requester profile info.
func (s *Store) ListFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]FriendRequestEntry, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM profile.friendships
		WHERE addressee_id = $1 AND status = 'pending'
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.requester_id, p.display_name, p.username, p.avatar_media_id, f.created_at
		FROM profile.friendships f
		JOIN profile.profiles p ON p.user_id = f.requester_id
		WHERE f.addressee_id = $1 AND f.status = 'pending'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []FriendRequestEntry
	for rows.Next() {
		var e FriendRequestEntry
		if err := rows.Scan(&e.FriendshipID, &e.UserID, &e.DisplayName, &e.Username, &e.AvatarMediaID, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// ListSentFriendRequests returns pending outgoing friend requests sent by the given userID, enriched with addressee profile info.
func (s *Store) ListSentFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]FriendRequestEntry, int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM profile.friendships
		WHERE requester_id = $1 AND status = 'pending'
	`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT f.id, f.addressee_id, p.display_name, p.username, p.avatar_media_id, f.created_at
		FROM profile.friendships f
		JOIN profile.profiles p ON p.user_id = f.addressee_id
		WHERE f.requester_id = $1 AND f.status = 'pending'
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []FriendRequestEntry
	for rows.Next() {
		var e FriendRequestEntry
		if err := rows.Scan(&e.FriendshipID, &e.UserID, &e.DisplayName, &e.Username, &e.AvatarMediaID, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// CancelFriendRequest deletes a pending friend request sent by the given user.
func (s *Store) CancelFriendRequest(ctx context.Context, requesterID, friendshipID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM profile.friendships
		WHERE id = $1 AND requester_id = $2 AND status = 'pending'
	`, friendshipID, requesterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("friend request not found or already responded")
	}
	return nil
}

// DeleteFriendship removes an accepted friendship between two users and decrements friend counts.
func (s *Store) DeleteFriendship(ctx context.Context, userID, friendID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM profile.friendships
		WHERE status = 'accepted'
		  AND ((requester_id = $1 AND addressee_id = $2)
		    OR (requester_id = $2 AND addressee_id = $1))
	`, userID, friendID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("friendship not found")
	}

	// Decrement friend counts for both users
	if err := s.DecrementFriendCount(ctx, userID); err != nil {
		return err
	}
	return s.DecrementFriendCount(ctx, friendID)
}

// DeleteFollowsBetween deletes follows in both directions between two users.
func (s *Store) DeleteFollowsBetween(ctx context.Context, userAID, userBID uuid.UUID) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM profile.follows
		WHERE (follower_id = $1 AND following_id = $2)
		   OR (follower_id = $2 AND following_id = $1)
	`, userAID, userBID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RejectFriendRequestsBetween rejects any pending friend requests in both directions.
func (s *Store) RejectFriendRequestsBetween(ctx context.Context, userAID, userBID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.friendships
		SET status = 'rejected', updated_at = NOW()
		WHERE status = 'pending'
		  AND ((requester_id = $1 AND addressee_id = $2)
		    OR (requester_id = $2 AND addressee_id = $1))
	`, userAID, userBID)
	return err
}

// CountFollowsBetween returns the number of active follows between two users (0, 1, or 2).
func (s *Store) CountFollowsBetween(ctx context.Context, userAID, userBID uuid.UUID) (aFollowsB, bFollowsA bool, err error) {
	rows, err := s.db.Query(ctx, `
		SELECT follower_id, following_id
		FROM profile.follows
		WHERE status = 'active'
		  AND ((follower_id = $1 AND following_id = $2)
		    OR (follower_id = $2 AND following_id = $1))
	`, userAID, userBID)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var followerID, followingID uuid.UUID
		if err := rows.Scan(&followerID, &followingID); err != nil {
			return false, false, err
		}
		if followerID == userAID {
			aFollowsB = true
		} else {
			bFollowsA = true
		}
	}
	return aFollowsB, bFollowsA, rows.Err()
}

// GetRelationship returns the full relationship state between viewerID and targetID.
func (s *Store) GetRelationship(ctx context.Context, viewerID, targetID uuid.UUID) (*RelationshipStatus, error) {
	rel := &RelationshipStatus{}

	// Check follows in both directions
	viewerFollows, targetFollows, err := s.CountFollowsBetween(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	rel.Following = viewerFollows
	rel.FollowedBy = targetFollows

	// Check circle (accepted friendship = in circle)
	friendship, err := s.GetFriendshipBetween(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	if friendship != nil {
		switch friendship.Status {
		case "accepted":
			rel.InCircle = true
		case "pending":
			reqID := friendship.ID.String()
			rel.CircleRequestID = &reqID
			if friendship.RequesterID == viewerID {
				rel.CircleRequestSent = true
			} else {
				rel.CircleRequestReceived = true
			}
		}
	}

	// Check blocks
	viewerBlocked, err := s.IsBlocked(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	rel.Blocked = viewerBlocked
	// Never reveal if target blocked viewer (spec: blocked_by always false)
	rel.BlockedBy = false

	// Derived permissions based on circle membership
	rel.CanDM = rel.InCircle
	rel.CanSeeOnline = rel.InCircle
	rel.CanAddToGroup = rel.InCircle

	// Mutual circle count: count friends they have in common
	rel.MutualCircleCount, err = s.CountMutualFriends(ctx, viewerID, targetID)
	if err != nil {
		// Non-fatal: log and continue with 0
		rel.MutualCircleCount = 0
	}

	return rel, nil
}

// GetProfilesByIDs returns profiles for the given list of user IDs.
func (s *Store) GetProfilesByIDs(ctx context.Context, ids []uuid.UUID) ([]Profile, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+allProfileCols+` FROM profile.profiles WHERE user_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		var p Profile
		err := rows.Scan(
			&p.UserID, &p.Username, &p.DisplayName, &p.FirstName, &p.LastName,
			&p.PreferredName, &p.Pronouns, &p.Bio, &p.DoB, &p.Gender,
			&p.AvatarMediaID, &p.CoverMediaID, &p.Category, &p.Profession, &p.Website, &p.Location, &p.BadgeFlags,
			&p.IsVerified, &p.VerificationLevel, &p.StatusText, &p.StatusEmoji, &p.StatusExpiresAt,
			&p.ProfileThemeColor, &p.IntroMediaURL, &p.IntroMediaType, &p.CTALabel, &p.CTAURL,
			&p.MemberSinceBadge, &p.Timezone,
			&p.FollowerCount, &p.FollowingCount, &p.FriendCount, &p.PostCount,
			&p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// CountMutualFriends counts how many accepted friends viewerID and targetID have in common.
func (s *Store) CountMutualFriends(ctx context.Context, userA, userB uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT CASE WHEN requester_id = $1 THEN addressee_id ELSE requester_id END AS friend_id
			FROM profile.friendships
			WHERE status = 'accepted' AND (requester_id = $1 OR addressee_id = $1)
		) a
		INNER JOIN (
			SELECT CASE WHEN requester_id = $2 THEN addressee_id ELSE requester_id END AS friend_id
			FROM profile.friendships
			WHERE status = 'accepted' AND (requester_id = $2 OR addressee_id = $2)
		) b ON a.friend_id = b.friend_id
	`, userA, userB).Scan(&count)
	return count, err
}
