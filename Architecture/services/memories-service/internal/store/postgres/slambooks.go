package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Slambook struct {
	ID                   uuid.UUID  `json:"id"`
	OwnerUserID          uuid.UUID  `json:"owner_user_id"`
	ContextType          string     `json:"context_type"`
	ContextID            *uuid.UUID `json:"context_id,omitempty"`
	Title                string     `json:"title"`
	Subtitle             *string    `json:"subtitle,omitempty"`
	Description          *string    `json:"description,omitempty"`
	Category             string     `json:"category"`
	ThemeKey             string     `json:"theme_key"`
	CoverMediaID         *uuid.UUID `json:"cover_media_id,omitempty"`
	Visibility           string     `json:"visibility"`
	ResponseIdentityMode string     `json:"response_identity_mode"`
	ApprovalRequired     bool       `json:"approval_required"`
	AllowCustomCards     bool       `json:"allow_custom_cards"`
	AllowReactions       bool       `json:"allow_reactions"`
	AllowComments        bool       `json:"allow_comments"`
	AllowShareLink       bool       `json:"allow_share_link"`
	MaxResponsesPerUser  int        `json:"max_responses_per_user"`
	OpensAt              time.Time  `json:"opens_at"`
	ClosesAt             *time.Time `json:"closes_at,omitempty"`
	Status               string     `json:"status"`
	InvitedCount         int        `json:"invited_count"`
	ResponseCount        int        `json:"response_count"`
	ApprovedCount        int        `json:"approved_count"`
	PinnedCount          int        `json:"pinned_count"`
	LastActivityAt       time.Time  `json:"last_activity_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty"`
	ViewerResponseStatus *string    `json:"viewer_response_status,omitempty"`
	ViewerSessionID      *uuid.UUID `json:"viewer_session_id,omitempty"`
	ViewerCanRespond     bool       `json:"viewer_can_respond"`
	ViewerCanModerate    bool       `json:"viewer_can_moderate"`
	ShareToken           *uuid.UUID `json:"share_token,omitempty"`
}

type SlambookTemplatePack struct {
	ID          uuid.UUID          `json:"id"`
	Key         string             `json:"key"`
	Title       string             `json:"title"`
	Description *string            `json:"description,omitempty"`
	Category    string             `json:"category"`
	Templates   []SlambookTemplate `json:"templates"`
}

type SlambookTemplate struct {
	ID              uuid.UUID      `json:"id"`
	PackID          uuid.UUID      `json:"pack_id"`
	Title           string         `json:"title"`
	Prompt          string         `json:"prompt"`
	ResponseType    string         `json:"response_type"`
	PlaceholderText *string        `json:"placeholder_text,omitempty"`
	HelpText        *string        `json:"help_text,omitempty"`
	Config          map[string]any `json:"config"`
	OrderIndex      int            `json:"order_index"`
}

type SlambookCard struct {
	ID                  uuid.UUID            `json:"id"`
	SlambookID          uuid.UUID            `json:"slambook_id"`
	SourceType          string               `json:"source_type"`
	TemplateID          *uuid.UUID           `json:"template_id,omitempty"`
	Title               string               `json:"title"`
	Prompt              string               `json:"prompt"`
	ResponseType        string               `json:"response_type"`
	PlaceholderText     *string              `json:"placeholder_text,omitempty"`
	HelpText            *string              `json:"help_text,omitempty"`
	Config              map[string]any       `json:"config"`
	IsRequired          bool                 `json:"is_required"`
	IsActive            bool                 `json:"is_active"`
	LockedAfterResponse bool                 `json:"locked_after_response"`
	OrderIndex          int                  `json:"order_index"`
	VersionNo           int                  `json:"version_no"`
	CreatedByUserID     uuid.UUID            `json:"created_by_user_id"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
	DeletedAt           *time.Time           `json:"deleted_at,omitempty"`
	Options             []SlambookCardOption `json:"options"`
}

type SlambookCardOption struct {
	ID         uuid.UUID `json:"id"`
	CardID     uuid.UUID `json:"card_id"`
	Label      string    `json:"label"`
	Value      string    `json:"value"`
	OrderIndex int       `json:"order_index"`
}

type SlambookInvite struct {
	ID            uuid.UUID  `json:"id"`
	SlambookID    uuid.UUID  `json:"slambook_id"`
	InviterUserID uuid.UUID  `json:"inviter_user_id"`
	InviteType    string     `json:"invite_type"`
	TargetUserID  *uuid.UUID `json:"target_user_id,omitempty"`
	TargetEmail   *string    `json:"target_email,omitempty"`
	TargetRefID   *uuid.UUID `json:"target_ref_id,omitempty"`
	ShareToken    *uuid.UUID `json:"share_token,omitempty"`
	Message       *string    `json:"message,omitempty"`
	Status        string     `json:"status"`
	OpenedAt      *time.Time `json:"opened_at,omitempty"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	DeclinedAt    *time.Time `json:"declined_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type SlambookResponseSession struct {
	ID                uuid.UUID              `json:"id"`
	SlambookID        uuid.UUID              `json:"slambook_id"`
	InviteID          *uuid.UUID             `json:"invite_id,omitempty"`
	ResponderUserID   *uuid.UUID             `json:"responder_user_id,omitempty"`
	DisplayName       *string                `json:"display_name,omitempty"`
	IdentityMode      string                 `json:"identity_mode"`
	Status            string                 `json:"status"`
	StartedAt         time.Time              `json:"started_at"`
	DraftLastSavedAt  *time.Time             `json:"draft_last_saved_at,omitempty"`
	SubmittedAt       *time.Time             `json:"submitted_at,omitempty"`
	ModeratedAt       *time.Time             `json:"moderated_at,omitempty"`
	ModeratedByUserID *uuid.UUID             `json:"moderated_by_user_id,omitempty"`
	ModerationReason  *string                `json:"moderation_reason,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	Items             []SlambookResponseItem `json:"items"`
}

type SlambookResponseItem struct {
	ID           uuid.UUID      `json:"id"`
	SessionID    uuid.UUID      `json:"session_id"`
	SlambookID   uuid.UUID      `json:"slambook_id"`
	CardID       uuid.UUID      `json:"card_id"`
	ResponseType string         `json:"response_type"`
	AnswerText   *string        `json:"answer_text,omitempty"`
	AnswerJSON   map[string]any `json:"answer_json"`
	MediaAssetID *uuid.UUID     `json:"media_asset_id,omitempty"`
	CardTitle    *string        `json:"card_title,omitempty"`
	CardPrompt   *string        `json:"card_prompt,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type SlambookSubmittedItem struct {
	CardID       uuid.UUID
	ResponseType string
	AnswerText   *string
	AnswerJSON   map[string]any
}

type OpinionSpaceItem struct {
	ID                   uuid.UUID      `json:"id"`
	SlambookID           uuid.UUID      `json:"slambook_id"`
	SessionID            uuid.UUID      `json:"session_id"`
	ResponseItemID       uuid.UUID      `json:"response_item_id"`
	Status               string         `json:"status"`
	IsPinned             bool           `json:"is_pinned"`
	BoardSection         *string        `json:"board_section,omitempty"`
	BoardOrder           float64        `json:"board_order"`
	ZIndex               int            `json:"z_index"`
	FeaturedBadge        *string        `json:"featured_badge,omitempty"`
	OwnerNote            *string        `json:"owner_note,omitempty"`
	ApprovedAt           *time.Time     `json:"approved_at,omitempty"`
	HiddenReason         *string        `json:"hidden_reason,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	ResponderDisplayName *string        `json:"responder_display_name,omitempty"`
	Anonymous            bool           `json:"anonymous"`
	CardTitle            string         `json:"card_title"`
	CardPrompt           string         `json:"card_prompt"`
	ResponseType         string         `json:"response_type"`
	AnswerText           *string        `json:"answer_text,omitempty"`
	AnswerJSON           map[string]any `json:"answer_json"`
}

const slambookBaseSelect = `
	SELECT s.id, s.owner_user_id, s.context_type, s.context_id, s.title, s.subtitle, s.description,
	       s.category, s.theme_key, s.cover_media_id, s.visibility, s.response_identity_mode,
	       s.approval_required, s.allow_custom_cards, s.allow_reactions, s.allow_comments,
	       s.allow_share_link, s.max_responses_per_user, s.opens_at, s.closes_at, s.status,
	       s.invited_count, s.response_count, s.approved_count, s.pinned_count,
	       s.last_activity_at, s.created_at, s.updated_at, s.deleted_at
	FROM memories.slambooks s
`

func (s *Store) ListSlambookTemplatePacks(ctx context.Context) ([]SlambookTemplatePack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, key, title, description, category
		FROM memories.slambook_template_packs
		WHERE is_active = TRUE
		ORDER BY category, title
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	packs := make([]SlambookTemplatePack, 0)
	indexByID := make(map[uuid.UUID]int)
	for rows.Next() {
		var pack SlambookTemplatePack
		if err := rows.Scan(&pack.ID, &pack.Key, &pack.Title, &pack.Description, &pack.Category); err != nil {
			return nil, err
		}
		pack.Templates = []SlambookTemplate{}
		indexByID[pack.ID] = len(packs)
		packs = append(packs, pack)
	}

	templateRows, err := s.db.Query(ctx, `
		SELECT id, pack_id, title, prompt, response_type, placeholder_text, help_text, config, order_index
		FROM memories.slambook_templates
		WHERE is_active = TRUE
		ORDER BY pack_id, order_index, created_at
	`)
	if err != nil {
		return nil, err
	}
	defer templateRows.Close()

	for templateRows.Next() {
		var tpl SlambookTemplate
		var rawConfig []byte
		if err := templateRows.Scan(
			&tpl.ID, &tpl.PackID, &tpl.Title, &tpl.Prompt, &tpl.ResponseType,
			&tpl.PlaceholderText, &tpl.HelpText, &rawConfig, &tpl.OrderIndex,
		); err != nil {
			return nil, err
		}
		tpl.Config = decodeJSONMap(rawConfig)
		if idx, ok := indexByID[tpl.PackID]; ok {
			packs[idx].Templates = append(packs[idx].Templates, tpl)
		}
	}
	return packs, nil
}

func (s *Store) CreateSlambook(ctx context.Context, slambook *Slambook, templatePackKey string, customCards []SlambookCard) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackQuietly(ctx, tx)

	_, err = tx.Exec(ctx, `
		INSERT INTO memories.slambooks (
			id, owner_user_id, context_type, context_id, title, subtitle, description,
			category, theme_key, cover_media_id, visibility, response_identity_mode,
			approval_required, allow_custom_cards, allow_reactions, allow_comments,
			allow_share_link, max_responses_per_user, opens_at, closes_at, status,
			last_activity_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19, $20, $21,
			$22, $23, $24
		)
	`,
		slambook.ID, slambook.OwnerUserID, slambook.ContextType, slambook.ContextID,
		slambook.Title, slambook.Subtitle, slambook.Description, slambook.Category,
		slambook.ThemeKey, slambook.CoverMediaID, slambook.Visibility, slambook.ResponseIdentityMode,
		slambook.ApprovalRequired, slambook.AllowCustomCards, slambook.AllowReactions,
		slambook.AllowComments, slambook.AllowShareLink, slambook.MaxResponsesPerUser,
		slambook.OpensAt, slambook.ClosesAt, slambook.Status, slambook.LastActivityAt,
		slambook.CreatedAt, slambook.UpdatedAt,
	)
	if err != nil {
		return err
	}

	orderIndex := 0
	templateCardsInserted := 0
	if templatePackKey != "" {
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.title, t.prompt, t.response_type, t.placeholder_text, t.help_text, t.config
			FROM memories.slambook_templates t
			INNER JOIN memories.slambook_template_packs p ON p.id = t.pack_id
			WHERE p.key = $1 AND p.is_active = TRUE AND t.is_active = TRUE
			ORDER BY t.order_index, t.created_at
		`, templatePackKey)
		if err != nil {
			return err
		}
		for rows.Next() {
			var templateID uuid.UUID
			var title, prompt, responseType string
			var placeholder, helpText *string
			var config []byte
			if err := rows.Scan(&templateID, &title, &prompt, &responseType, &placeholder, &helpText, &config); err != nil {
				rows.Close()
				return err
			}
			orderIndex++
			templateCardsInserted++
			if _, err := tx.Exec(ctx, `
				INSERT INTO memories.slambook_cards (
					id, slambook_id, source_type, template_id, title, prompt, response_type,
					placeholder_text, help_text, config, is_required, is_active, locked_after_response,
					order_index, version_no, created_by_user_id, created_at, updated_at
				)
				VALUES (
					$1, $2, 'template', $3, $4, $5, $6,
					$7, $8, $9, FALSE, TRUE, FALSE,
					$10, 1, $11, NOW(), NOW()
				)
			`, uuid.New(), slambook.ID, templateID, title, prompt, responseType, placeholder, helpText, config, orderIndex, slambook.OwnerUserID); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if templateCardsInserted == 0 {
			return fmt.Errorf("template pack %q was not found or has no active templates", templatePackKey)
		}
	}

	for _, card := range customCards {
		orderIndex++
		config, err := json.Marshal(card.Config)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO memories.slambook_cards (
				id, slambook_id, source_type, template_id, title, prompt, response_type,
				placeholder_text, help_text, config, is_required, is_active, locked_after_response,
				order_index, version_no, created_by_user_id, created_at, updated_at
			)
			VALUES (
				$1, $2, 'custom', NULL, $3, $4, $5,
				$6, $7, $8, $9, TRUE, FALSE,
				$10, 1, $11, NOW(), NOW()
			)
		`, uuid.New(), slambook.ID, card.Title, card.Prompt, card.ResponseType, card.PlaceholderText, card.HelpText, config, card.IsRequired, orderIndex, slambook.OwnerUserID); err != nil {
			return err
		}
	}
	if orderIndex == 0 {
		return fmt.Errorf("slambook must contain at least one card")
	}

	return tx.Commit(ctx)
}

func (s *Store) ListOwnedSlambooks(ctx context.Context, ownerUserID uuid.UUID) ([]Slambook, error) {
	rows, err := s.db.Query(ctx, slambookBaseSelect+`
		WHERE s.owner_user_id = $1 AND s.deleted_at IS NULL
		ORDER BY s.updated_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlambooks(rows)
}

func (s *Store) ListPublicSlambooksByOwner(ctx context.Context, ownerUserID uuid.UUID) ([]Slambook, error) {
	rows, err := s.db.Query(ctx, slambookBaseSelect+`
		WHERE s.owner_user_id = $1 AND s.deleted_at IS NULL
		  AND s.visibility = 'public'
		  AND s.status IN ('active', 'closed', 'archived')
		ORDER BY s.updated_at DESC
	`, ownerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlambooks(rows)
}

func (s *Store) GetSlambook(ctx context.Context, id uuid.UUID) (*Slambook, error) {
	row := s.db.QueryRow(ctx, slambookBaseSelect+`
		WHERE s.id = $1 AND s.deleted_at IS NULL
	`, id)
	var slambook Slambook
	if err := scanSlambook(row, &slambook); err != nil {
		return nil, err
	}
	return &slambook, nil
}

func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func rollbackQuietly(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

type slambookScanner interface {
	Scan(dest ...any) error
}

func scanSlambooks(rows pgx.Rows) ([]Slambook, error) {
	slambooks := make([]Slambook, 0)
	for rows.Next() {
		var slambook Slambook
		if err := scanSlambook(rows, &slambook); err != nil {
			return nil, err
		}
		slambooks = append(slambooks, slambook)
	}
	return slambooks, nil
}

func scanSlambook(scanner slambookScanner, slambook *Slambook) error {
	return scanner.Scan(
		&slambook.ID, &slambook.OwnerUserID, &slambook.ContextType, &slambook.ContextID,
		&slambook.Title, &slambook.Subtitle, &slambook.Description, &slambook.Category,
		&slambook.ThemeKey, &slambook.CoverMediaID, &slambook.Visibility, &slambook.ResponseIdentityMode,
		&slambook.ApprovalRequired, &slambook.AllowCustomCards, &slambook.AllowReactions, &slambook.AllowComments,
		&slambook.AllowShareLink, &slambook.MaxResponsesPerUser, &slambook.OpensAt, &slambook.ClosesAt,
		&slambook.Status, &slambook.InvitedCount, &slambook.ResponseCount, &slambook.ApprovedCount,
		&slambook.PinnedCount, &slambook.LastActivityAt, &slambook.CreatedAt, &slambook.UpdatedAt, &slambook.DeletedAt,
	)
}

func (s *Store) GetSlambookCards(ctx context.Context, slambookID uuid.UUID) ([]SlambookCard, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, slambook_id, source_type, template_id, title, prompt, response_type,
		       placeholder_text, help_text, config, is_required, is_active,
		       locked_after_response, order_index, version_no, created_by_user_id,
		       created_at, updated_at, deleted_at
		FROM memories.slambook_cards
		WHERE slambook_id = $1 AND deleted_at IS NULL
		ORDER BY order_index, created_at
	`, slambookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cards := make([]SlambookCard, 0)
	for rows.Next() {
		var card SlambookCard
		var rawConfig []byte
		if err := rows.Scan(
			&card.ID, &card.SlambookID, &card.SourceType, &card.TemplateID, &card.Title,
			&card.Prompt, &card.ResponseType, &card.PlaceholderText, &card.HelpText, &rawConfig,
			&card.IsRequired, &card.IsActive, &card.LockedAfterResponse, &card.OrderIndex,
			&card.VersionNo, &card.CreatedByUserID, &card.CreatedAt, &card.UpdatedAt, &card.DeletedAt,
		); err != nil {
			return nil, err
		}
		card.Config = decodeJSONMap(rawConfig)
		card.Options = []SlambookCardOption{}
		cards = append(cards, card)
	}
	return cards, nil
}

func (s *Store) GetViewerResponseSession(ctx context.Context, slambookID, viewerID uuid.UUID) (*SlambookResponseSession, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, slambook_id, invite_id, responder_user_id, display_name_snapshot, identity_mode,
		       status, started_at, draft_last_saved_at, submitted_at, moderated_at,
		       moderated_by_user_id, moderation_reason, created_at, updated_at
		FROM memories.slambook_response_sessions
		WHERE slambook_id = $1 AND responder_user_id = $2 AND deleted_at IS NULL
	`, slambookID, viewerID)
	var session SlambookResponseSession
	if err := row.Scan(
		&session.ID, &session.SlambookID, &session.InviteID, &session.ResponderUserID, &session.DisplayName,
		&session.IdentityMode, &session.Status, &session.StartedAt, &session.DraftLastSavedAt,
		&session.SubmittedAt, &session.ModeratedAt, &session.ModeratedByUserID, &session.ModerationReason,
		&session.CreatedAt, &session.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	items, err := s.getSessionItems(ctx, []uuid.UUID{session.ID})
	if err != nil {
		return nil, err
	}
	session.Items = items[session.ID]
	return &session, nil
}

func (s *Store) EnsureShareLinkInvite(ctx context.Context, slambookID, inviterUserID uuid.UUID) (*SlambookInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, slambook_id, inviter_user_id, invite_type, target_user_id, target_email, target_ref_id,
		       share_token, message, status, opened_at, accepted_at, declined_at, expires_at, created_at, updated_at
		FROM memories.slambook_invites
		WHERE slambook_id = $1 AND invite_type = 'link'
		ORDER BY created_at DESC
		LIMIT 1
	`, slambookID)
	var invite SlambookInvite
	if err := row.Scan(
		&invite.ID, &invite.SlambookID, &invite.InviterUserID, &invite.InviteType, &invite.TargetUserID,
		&invite.TargetEmail, &invite.TargetRefID, &invite.ShareToken, &invite.Message, &invite.Status,
		&invite.OpenedAt, &invite.AcceptedAt, &invite.DeclinedAt, &invite.ExpiresAt, &invite.CreatedAt, &invite.UpdatedAt,
	); err == nil {
		return &invite, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	invite = SlambookInvite{
		ID:            uuid.New(),
		SlambookID:    slambookID,
		InviterUserID: inviterUserID,
		InviteType:    "link",
		Status:        "sent",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO memories.slambook_invites (
			id, slambook_id, inviter_user_id, invite_type, status, created_at, updated_at
		)
		VALUES ($1, $2, $3, 'link', $4, $5, $6)
	`, invite.ID, invite.SlambookID, invite.InviterUserID, invite.Status, invite.CreatedAt, invite.UpdatedAt); err != nil {
		return nil, err
	}
	return s.GetInviteByID(ctx, invite.ID)
}

func (s *Store) CreateUserInvites(ctx context.Context, slambookID, inviterUserID uuid.UUID, targetUserIDs []uuid.UUID, message *string) ([]SlambookInvite, error) {
	now := time.Now()
	invites := make([]SlambookInvite, 0, len(targetUserIDs))
	for _, targetUserID := range targetUserIDs {
		invite := SlambookInvite{
			ID:            uuid.New(),
			SlambookID:    slambookID,
			InviterUserID: inviterUserID,
			InviteType:    "user",
			TargetUserID:  &targetUserID,
			Message:       message,
			Status:        "sent",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO memories.slambook_invites (
				id, slambook_id, inviter_user_id, invite_type, target_user_id, message, status, created_at, updated_at
			)
			VALUES ($1, $2, $3, 'user', $4, $5, $6, $7, $8)
		`, invite.ID, invite.SlambookID, invite.InviterUserID, targetUserID, invite.Message, invite.Status, invite.CreatedAt, invite.UpdatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, invite)
	}
	if len(invites) > 0 {
		_, _ = s.db.Exec(ctx, `
			UPDATE memories.slambooks
			SET invited_count = (
				SELECT COUNT(*) FROM memories.slambook_invites WHERE slambook_id = $1
			),
			last_activity_at = NOW(),
			updated_at = NOW()
			WHERE id = $1
		`, slambookID)
	}
	return invites, nil
}

func (s *Store) GetInviteByID(ctx context.Context, id uuid.UUID) (*SlambookInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, slambook_id, inviter_user_id, invite_type, target_user_id, target_email, target_ref_id,
		       share_token, message, status, opened_at, accepted_at, declined_at, expires_at, created_at, updated_at
		FROM memories.slambook_invites
		WHERE id = $1
	`, id)
	var invite SlambookInvite
	if err := row.Scan(
		&invite.ID, &invite.SlambookID, &invite.InviterUserID, &invite.InviteType, &invite.TargetUserID,
		&invite.TargetEmail, &invite.TargetRefID, &invite.ShareToken, &invite.Message, &invite.Status,
		&invite.OpenedAt, &invite.AcceptedAt, &invite.DeclinedAt, &invite.ExpiresAt, &invite.CreatedAt, &invite.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &invite, nil
}

func (s *Store) GetInviteByToken(ctx context.Context, token uuid.UUID) (*SlambookInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, slambook_id, inviter_user_id, invite_type, target_user_id, target_email, target_ref_id,
		       share_token, message, status, opened_at, accepted_at, declined_at, expires_at, created_at, updated_at
		FROM memories.slambook_invites
		WHERE share_token = $1
		  AND status IN ('pending', 'sent', 'opened', 'accepted')
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, token)
	var invite SlambookInvite
	if err := row.Scan(
		&invite.ID, &invite.SlambookID, &invite.InviterUserID, &invite.InviteType, &invite.TargetUserID,
		&invite.TargetEmail, &invite.TargetRefID, &invite.ShareToken, &invite.Message, &invite.Status,
		&invite.OpenedAt, &invite.AcceptedAt, &invite.DeclinedAt, &invite.ExpiresAt, &invite.CreatedAt, &invite.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &invite, nil
}

func (s *Store) GetInviteByTargetUser(ctx context.Context, slambookID, targetUserID uuid.UUID) (*SlambookInvite, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, slambook_id, inviter_user_id, invite_type, target_user_id, target_email, target_ref_id,
		       share_token, message, status, opened_at, accepted_at, declined_at, expires_at, created_at, updated_at
		FROM memories.slambook_invites
		WHERE slambook_id = $1
		  AND target_user_id = $2
		  AND status IN ('pending', 'sent', 'opened', 'accepted')
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
		LIMIT 1
	`, slambookID, targetUserID)
	var invite SlambookInvite
	if err := row.Scan(
		&invite.ID, &invite.SlambookID, &invite.InviterUserID, &invite.InviteType, &invite.TargetUserID,
		&invite.TargetEmail, &invite.TargetRefID, &invite.ShareToken, &invite.Message, &invite.Status,
		&invite.OpenedAt, &invite.AcceptedAt, &invite.DeclinedAt, &invite.ExpiresAt, &invite.CreatedAt, &invite.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &invite, nil
}

func (s *Store) SaveResponseSession(
	ctx context.Context,
	slambookID uuid.UUID,
	responderID uuid.UUID,
	inviteID *uuid.UUID,
	displayName string,
	identityMode string,
	answers []SlambookSubmittedItem,
	submit bool,
	approvalRequired bool,
) (*SlambookResponseSession, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackQuietly(ctx, tx)

	var session SlambookResponseSession
	row := tx.QueryRow(ctx, `
		SELECT id, slambook_id, invite_id, responder_user_id, display_name_snapshot, identity_mode,
		       status, started_at, draft_last_saved_at, submitted_at, moderated_at,
		       moderated_by_user_id, moderation_reason, created_at, updated_at
		FROM memories.slambook_response_sessions
		WHERE slambook_id = $1 AND responder_user_id = $2 AND deleted_at IS NULL
	`, slambookID, responderID)
	err = row.Scan(
		&session.ID, &session.SlambookID, &session.InviteID, &session.ResponderUserID, &session.DisplayName,
		&session.IdentityMode, &session.Status, &session.StartedAt, &session.DraftLastSavedAt,
		&session.SubmittedAt, &session.ModeratedAt, &session.ModeratedByUserID, &session.ModerationReason,
		&session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	now := time.Now()
	newStatus := "draft"
	if submit {
		if approvalRequired {
			newStatus = "pending"
		} else {
			newStatus = "approved"
		}
	}

	if errors.Is(err, pgx.ErrNoRows) {
		session = SlambookResponseSession{
			ID:              uuid.New(),
			SlambookID:      slambookID,
			InviteID:        inviteID,
			ResponderUserID: &responderID,
			IdentityMode:    identityMode,
			Status:          newStatus,
			StartedAt:       now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if displayName != "" {
			session.DisplayName = &displayName
		}
		if submit {
			session.SubmittedAt = &now
		} else {
			session.DraftLastSavedAt = &now
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO memories.slambook_response_sessions (
				id, slambook_id, invite_id, responder_user_id, display_name_snapshot,
				identity_mode, status, started_at, draft_last_saved_at, submitted_at,
				created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		`,
			session.ID, session.SlambookID, session.InviteID, session.ResponderUserID, session.DisplayName,
			session.IdentityMode, session.Status, session.StartedAt, session.DraftLastSavedAt, session.SubmittedAt,
			session.CreatedAt, session.UpdatedAt,
		); err != nil {
			return nil, err
		}
	} else {
		session.Status = newStatus
		session.IdentityMode = identityMode
		session.InviteID = inviteID
		session.UpdatedAt = now
		if displayName != "" {
			session.DisplayName = &displayName
		}
		if submit {
			session.SubmittedAt = &now
			session.DraftLastSavedAt = nil
		} else {
			session.DraftLastSavedAt = &now
		}
		if _, err := tx.Exec(ctx, `
			UPDATE memories.slambook_response_sessions
			SET invite_id = $2,
			    display_name_snapshot = $3,
			    identity_mode = $4,
			    status = $5,
			    draft_last_saved_at = $6,
			    submitted_at = $7,
			    updated_at = $8
			WHERE id = $1
		`, session.ID, session.InviteID, session.DisplayName, session.IdentityMode, session.Status, session.DraftLastSavedAt, session.SubmittedAt, session.UpdatedAt); err != nil {
			return nil, err
		}
	}

	cardIDs := make([]uuid.UUID, 0, len(answers))
	for _, answer := range answers {
		cardIDs = append(cardIDs, answer.CardID)
	}
	if len(cardIDs) == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM memories.slambook_response_items WHERE session_id = $1`, session.ID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			DELETE FROM memories.slambook_response_items
			WHERE session_id = $1 AND NOT (card_id = ANY($2))
		`, session.ID, cardIDs); err != nil {
			return nil, err
		}
	}

	session.Items = make([]SlambookResponseItem, 0, len(answers))
	boardStatus := "pending"
	if submit && !approvalRequired {
		boardStatus = "visible"
	}
	for index, answer := range answers {
		rawJSON, err := json.Marshal(answer.AnswerJSON)
		if err != nil {
			return nil, err
		}
		var item SlambookResponseItem
		if err := tx.QueryRow(ctx, `
			INSERT INTO memories.slambook_response_items (
				id, session_id, slambook_id, card_id, response_type, answer_text, answer_json, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
			ON CONFLICT (session_id, card_id) DO UPDATE
			SET response_type = EXCLUDED.response_type,
			    answer_text = EXCLUDED.answer_text,
			    answer_json = EXCLUDED.answer_json,
			    updated_at = NOW(),
			    deleted_at = NULL
			RETURNING id, session_id, slambook_id, card_id, response_type, answer_text, answer_json, media_asset_id, created_at, updated_at
		`,
			uuid.New(), session.ID, slambookID, answer.CardID, answer.ResponseType, answer.AnswerText, rawJSON,
		).Scan(
			&item.ID, &item.SessionID, &item.SlambookID, &item.CardID, &item.ResponseType,
			&item.AnswerText, &rawJSON, &item.MediaAssetID, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.AnswerJSON = decodeJSONMap(rawJSON)
		session.Items = append(session.Items, item)

		if submit {
			if _, err := tx.Exec(ctx, `
				INSERT INTO memories.opinion_space_items (
					id, slambook_id, session_id, response_item_id, status, is_pinned, board_order, created_at, updated_at
				)
				VALUES ($1, $2, $3, $4, $5, FALSE, $6, NOW(), NOW())
				ON CONFLICT (response_item_id) DO UPDATE
				SET status = EXCLUDED.status,
				    updated_at = NOW()
			`, uuid.New(), slambookID, session.ID, item.ID, boardStatus, float64(index+1)); err != nil {
				return nil, err
			}
		}
	}

	if err := refreshSlambookCounters(ctx, tx, slambookID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Store) ListPendingSlambookSessions(ctx context.Context, slambookID uuid.UUID) ([]SlambookResponseSession, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, slambook_id, invite_id, responder_user_id, display_name_snapshot, identity_mode,
		       status, started_at, draft_last_saved_at, submitted_at, moderated_at,
		       moderated_by_user_id, moderation_reason, created_at, updated_at
		FROM memories.slambook_response_sessions
		WHERE slambook_id = $1 AND status = 'pending' AND deleted_at IS NULL
		ORDER BY submitted_at DESC NULLS LAST, created_at DESC
	`, slambookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]SlambookResponseSession, 0)
	sessionIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var session SlambookResponseSession
		if err := rows.Scan(
			&session.ID, &session.SlambookID, &session.InviteID, &session.ResponderUserID, &session.DisplayName,
			&session.IdentityMode, &session.Status, &session.StartedAt, &session.DraftLastSavedAt,
			&session.SubmittedAt, &session.ModeratedAt, &session.ModeratedByUserID, &session.ModerationReason,
			&session.CreatedAt, &session.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
		sessionIDs = append(sessionIDs, session.ID)
	}
	itemsBySession, err := s.getSessionItems(ctx, sessionIDs)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i].Items = itemsBySession[sessions[i].ID]
	}
	return sessions, nil
}

func (s *Store) ModerateSlambookSession(ctx context.Context, slambookID, sessionID, actorUserID uuid.UUID, action, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackQuietly(ctx, tx)

	sessionStatus := "pending"
	boardStatus := "pending"
	switch action {
	case "approve":
		sessionStatus = "approved"
		boardStatus = "visible"
	case "reject":
		sessionStatus = "rejected"
		boardStatus = "rejected"
	case "hide":
		sessionStatus = "hidden"
		boardStatus = "hidden"
	default:
		return fmt.Errorf("unsupported moderation action: %s", action)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE memories.slambook_response_sessions
		SET status = $3,
		    moderated_at = NOW(),
		    moderated_by_user_id = $4,
		    moderation_reason = $5,
		    updated_at = NOW()
		WHERE id = $1 AND slambook_id = $2
	`, sessionID, slambookID, sessionStatus, actorUserID, reason); err != nil {
		return err
	}

	if boardStatus == "visible" {
		if _, err := tx.Exec(ctx, `
			UPDATE memories.opinion_space_items
			SET status = 'visible',
			    approved_by_user_id = $3,
			    approved_at = NOW(),
			    updated_at = NOW()
			WHERE slambook_id = $1 AND session_id = $2
		`, slambookID, sessionID, actorUserID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE memories.opinion_space_items
			SET status = $3,
			    hidden_reason = $4,
			    updated_at = NOW()
			WHERE slambook_id = $1 AND session_id = $2
		`, slambookID, sessionID, boardStatus, reason); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO memories.slambook_moderation_log (
			id, slambook_id, session_id, actor_user_id, action, reason, metadata, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, '{}'::jsonb, NOW())
	`, uuid.New(), slambookID, sessionID, actorUserID, action, reason); err != nil {
		return err
	}

	if err := refreshSlambookCounters(ctx, tx, slambookID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListVisibleOpinionSpaceItems(ctx context.Context, slambookID uuid.UUID) ([]OpinionSpaceItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT oi.id, oi.slambook_id, oi.session_id, oi.response_item_id, oi.status, oi.is_pinned,
		       oi.board_section, oi.board_order, oi.z_index, oi.featured_badge, oi.owner_note,
		       oi.approved_at, oi.hidden_reason, oi.created_at, oi.updated_at,
		       rs.display_name_snapshot, rs.identity_mode,
		       c.title, c.prompt, ri.response_type, ri.answer_text, ri.answer_json
		FROM memories.opinion_space_items oi
		INNER JOIN memories.slambook_response_sessions rs ON rs.id = oi.session_id
		INNER JOIN memories.slambook_response_items ri ON ri.id = oi.response_item_id
		INNER JOIN memories.slambook_cards c ON c.id = ri.card_id
		WHERE oi.slambook_id = $1 AND oi.status = 'visible'
		ORDER BY oi.is_pinned DESC, oi.board_order ASC, oi.created_at ASC
	`, slambookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]OpinionSpaceItem, 0)
	for rows.Next() {
		var (
			item         OpinionSpaceItem
			displayName  *string
			identityMode string
			rawAnswer    []byte
		)
		if err := rows.Scan(
			&item.ID, &item.SlambookID, &item.SessionID, &item.ResponseItemID, &item.Status, &item.IsPinned,
			&item.BoardSection, &item.BoardOrder, &item.ZIndex, &item.FeaturedBadge, &item.OwnerNote,
			&item.ApprovedAt, &item.HiddenReason, &item.CreatedAt, &item.UpdatedAt,
			&displayName, &identityMode, &item.CardTitle, &item.CardPrompt, &item.ResponseType,
			&item.AnswerText, &rawAnswer,
		); err != nil {
			return nil, err
		}
		item.AnswerJSON = decodeJSONMap(rawAnswer)
		item.Anonymous = identityMode != "named"
		if !item.Anonymous {
			item.ResponderDisplayName = displayName
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) SetOpinionSpacePinned(ctx context.Context, slambookID, itemID uuid.UUID, pinned bool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackQuietly(ctx, tx)
	if _, err := tx.Exec(ctx, `
		UPDATE memories.opinion_space_items
		SET is_pinned = $3, updated_at = NOW()
		WHERE id = $1 AND slambook_id = $2
	`, itemID, slambookID, pinned); err != nil {
		return err
	}
	if err := refreshSlambookCounters(ctx, tx, slambookID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReorderOpinionSpaceItems(ctx context.Context, slambookID uuid.UUID, itemIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackQuietly(ctx, tx)
	for index, itemID := range itemIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE memories.opinion_space_items
			SET board_order = $3, updated_at = NOW()
			WHERE id = $1 AND slambook_id = $2
		`, itemID, slambookID, float64(index+1)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ArchiveSlambook(ctx context.Context, slambookID, ownerUserID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE memories.slambooks
		SET status = 'archived', updated_at = NOW(), last_activity_at = NOW()
		WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NULL
	`, slambookID, ownerUserID)
	return err
}

func (s *Store) getSessionItems(ctx context.Context, sessionIDs []uuid.UUID) (map[uuid.UUID][]SlambookResponseItem, error) {
	result := make(map[uuid.UUID][]SlambookResponseItem)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT ri.id, ri.session_id, ri.slambook_id, ri.card_id, ri.response_type, ri.answer_text, ri.answer_json,
		       ri.media_asset_id, c.title, c.prompt, ri.created_at, ri.updated_at
		FROM memories.slambook_response_items ri
		INNER JOIN memories.slambook_cards c ON c.id = ri.card_id
		WHERE ri.session_id = ANY($1) AND ri.deleted_at IS NULL
		ORDER BY ri.created_at ASC
	`, sessionIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			item      SlambookResponseItem
			rawAnswer []byte
		)
		if err := rows.Scan(
			&item.ID, &item.SessionID, &item.SlambookID, &item.CardID, &item.ResponseType,
			&item.AnswerText, &rawAnswer, &item.MediaAssetID, &item.CardTitle, &item.CardPrompt,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.AnswerJSON = decodeJSONMap(rawAnswer)
		result[item.SessionID] = append(result[item.SessionID], item)
	}
	return result, nil
}

func refreshSlambookCounters(ctx context.Context, tx pgx.Tx, slambookID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE memories.slambooks s
		SET invited_count = COALESCE(invite_stats.invited_count, 0),
		    response_count = COALESCE(session_stats.response_count, 0),
		    approved_count = COALESCE(session_stats.approved_count, 0),
		    pinned_count = COALESCE(board_stats.pinned_count, 0),
		    last_activity_at = NOW(),
		    updated_at = NOW()
		FROM (
			SELECT COUNT(*)::int AS invited_count
			FROM memories.slambook_invites
			WHERE slambook_id = $1 AND status <> 'revoked'
		) invite_stats,
		(
			SELECT
				COUNT(*) FILTER (WHERE status IN ('pending', 'approved'))::int AS response_count,
				COUNT(*) FILTER (WHERE status = 'approved')::int AS approved_count
			FROM memories.slambook_response_sessions
			WHERE slambook_id = $1 AND deleted_at IS NULL
		) session_stats,
		(
			SELECT COUNT(*) FILTER (WHERE is_pinned = TRUE AND status = 'visible')::int AS pinned_count
			FROM memories.opinion_space_items
			WHERE slambook_id = $1
		) board_stats
		WHERE s.id = $1
	`, slambookID)
	return err
}
