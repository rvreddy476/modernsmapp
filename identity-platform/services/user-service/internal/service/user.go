package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/identity-user-service/internal/config"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrInvalidPrivacySetting is returned by UpdateSettings when a privacy enum
// field carries a value outside its allowed set (spec §5.2). The HTTP layer
// maps this to 400.
var ErrInvalidPrivacySetting = errors.New("invalid privacy setting value")

// allowedPrivacyValues lists the accepted enum values per spec §5.2 for each
// privacy field. account_visibility and allow_comments_from are now validated
// here too (Module 3 — private accounts are a real, enforced feature); the
// one remaining legacy column, allow_messages_from, is intentionally not
// validated (WhoCanMessage is the authoritative messaging field).
var allowedPrivacyValues = map[string]map[string]bool{
	// Module 3: "private" is accepted and enforced. graph-service consumes
	// the settings-changed event (which carries the new value) to gate
	// follows and auto-accept pending requests on private→public.
	"account_visibility": {
		"public": true, "private": true,
	},
	"allow_comments_from": {
		"everyone": true, "friends": true,
	},
	// DM consent model (founder-approved 2026-10-20): exactly four values.
	// "Recipient follows sender" is the consent signal graph-service applies.
	// The pre-consent values are still ACCEPTED on write (old app versions)
	// but are mapped by normaliseLegacySettings before this check runs, so
	// they never reach the table or a read.
	"who_can_message": {
		"everyone_message_requests": true, "followers_message_requests": true,
		"people_i_follow": true, "no_one": true,
	},
	// who_can_send_connection_request is gone (24 Oct 2026): connection
	// requests are retired, so the key is ignored on PUT and never returned.
	// connections_only is legacy: accepting a message request no longer
	// creates a graph connection, so it is mapped to accepted_chats_only on
	// write (normaliseLegacySettings) and never stored.
	"who_can_call": {
		"no_one": true, "accepted_chats_only": true,
	},
	"who_can_add_to_groups": {
		"no_one": true, "connections_only": true, "connections_and_contacts": true,
		"everyone_with_approval": true,
		// Production chat pass: friends-of-friends may INVITE (consent
		// required); direct add stays connections-only.
		"friends_of_friends_invites": true,
	},
	// Chat availability is a master switch, not a visibility value.
	"chat_availability": {
		"enabled": true, "paused": true,
	},
	// Mentions and tags (2026-10-24): who_can_mention and who_can_tag share
	// one set. "following" = the accounts this user follows (graph-service
	// resolves it as "target follows actor").
	"mention_tag": {
		"everyone": true, "following": true, "no_one": true,
	},
	// The four visibility fields share one enum set.
	"visibility": {
		"everyone": true, "connections_only": true, "no_one": true,
	},
}

// allowedChatBubbleStyles is the chat_bubble_style vocabulary (CONTRACT §7).
// Clients map each name to a design-token palette; the server stores only the
// name. Keep in step with the CHECK in database/setup.sql.
var allowedChatBubbleStyles = map[string]bool{
	"default": true, "ocean": true, "ember": true, "forest": true, "violet": true, "mono": true,
}

// legacyWhoCanMessage maps the pre-consent-model who_can_message values to
// the four the DM consent contract allows. The SAME map is applied to stored
// rows by the boot-time UPDATE in database/setup.sql; keep the two in step.
var legacyWhoCanMessage = map[string]string{
	"connections_only":                 "people_i_follow",
	"connections_and_mutual_followers": "people_i_follow",
	"friends_of_friends_requests":      "followers_message_requests",
}

// legacyWhoCanCall maps the retired who_can_call value (also in setup.sql).
var legacyWhoCanCall = map[string]string{
	"connections_only": "accepted_chats_only",
}

// Values a minor (or Strict Privacy Mode) may never hold for who_can_message:
// both let a stranger reach the account with a request.
var openWhoCanMessage = map[string]bool{
	"everyone_message_requests":  true,
	"followers_message_requests": true,
}

// normaliseLegacySettings rewrites legacy who_can_message / who_can_call
// values to their contract equivalents. Applied before validation on every
// write and to every read, so a row or cache entry written by an older build
// never surfaces a legacy value.
func normaliseLegacySettings(s *store.UserSettings) {
	if v, ok := legacyWhoCanMessage[s.WhoCanMessage]; ok {
		s.WhoCanMessage = v
	}
	if v, ok := legacyWhoCanCall[s.WhoCanCall]; ok {
		s.WhoCanCall = v
	}
}

// clampMinorMessaging keeps a minor (or a Strict Privacy Mode account) out of
// the two request-open values: they become people_i_follow. no_one and
// people_i_follow are left alone, and an unknown value is left for
// validation to refuse rather than silently rewritten.
func clampMinorMessaging(s *store.UserSettings) {
	if !s.StrictPrivacyMode && !s.Under18Mode {
		return
	}
	if openWhoCanMessage[s.WhoCanMessage] {
		s.WhoCanMessage = "people_i_follow"
	}
}

// normaliseForRead is what every settings read passes through: legacy values
// mapped and the minor clamp re-applied, so the settings API and the internal
// route graph-service reads only ever return the contract's value set.
func normaliseForRead(s *store.UserSettings) {
	normaliseLegacySettings(s)
	clampMinorMessaging(s)
}

// Filtered-keyword limits (CONTRACT §7).
const (
	maxFilteredKeywords   = 100
	maxFilteredKeywordLen = 30 // characters (runes), after trimming
)

// Field-level error codes carried in the 400 response's error.details.
const (
	SettingCodeInvalidValue    = "INVALID_VALUE"
	SettingCodeTooManyKeywords = "TOO_MANY_KEYWORDS"
	SettingCodeKeywordLength   = "KEYWORD_LENGTH"
	SettingCodeKeywordInvalid  = "KEYWORD_INVALID"
)

// SettingValidationError names the offending field and a machine code. It
// unwraps to ErrInvalidPrivacySetting, so existing errors.Is checks (and the
// handler's 400 mapping) keep working; Error() keeps the historical message
// prefix.
type SettingValidationError struct {
	Field string
	Code  string
	Value string
}

func (e *SettingValidationError) Error() string {
	return fmt.Sprintf("%s: %s %q", ErrInvalidPrivacySetting.Error(), e.Field, e.Value)
}

func (e *SettingValidationError) Unwrap() error { return ErrInvalidPrivacySetting }

// validatePrivacySettings rejects any enum field with an out-of-range value.
func validatePrivacySettings(s *store.UserSettings) error {
	checks := []struct{ vocab, field, value string }{
		{"account_visibility", "account_visibility", s.AccountVisibility},
		{"allow_comments_from", "allow_comments_from", s.AllowCommentsFrom},
		{"who_can_message", "who_can_message", s.WhoCanMessage},
		{"who_can_call", "who_can_call", s.WhoCanCall},
		{"who_can_add_to_groups", "who_can_add_to_groups", s.WhoCanAddToGroups},
		{"chat_availability", "chat_availability", s.ChatAvailability},
		{"visibility", "who_can_see_online_status", s.WhoCanSeeOnlineStatus},
		{"visibility", "who_can_see_read_receipts", s.WhoCanSeeReadReceipts},
		{"visibility", "who_can_see_last_seen", s.WhoCanSeeLastSeen},
		{"visibility", "who_can_see_profile_photo", s.WhoCanSeeProfilePhoto},
		{"mention_tag", "who_can_mention", s.WhoCanMention},
		{"mention_tag", "who_can_tag", s.WhoCanTag},
	}
	for _, c := range checks {
		if !allowedPrivacyValues[c.vocab][c.value] {
			return &SettingValidationError{Field: c.field, Code: SettingCodeInvalidValue, Value: c.value}
		}
	}
	if !allowedChatBubbleStyles[s.ChatBubbleStyle] {
		return &SettingValidationError{Field: "chat_bubble_style", Code: SettingCodeInvalidValue, Value: s.ChatBubbleStyle}
	}
	return nil
}

// NormaliseFilteredKeywords validates and normalises dm_filtered_keywords:
// at most 100 entries; each is trimmed, inner whitespace runs collapse to one
// space, and it is lower-cased; each must then be 1..30 characters with no
// control characters; duplicates (case-insensitive) are dropped, keeping the
// first occurrence's position. The result is what gets stored, so matching
// downstream is a plain lower-cased comparison.
//
// The count is checked on the RAW input, before dedupe, so the work done on
// an oversized payload stays bounded.
func NormaliseFilteredKeywords(in []string) ([]string, error) {
	if len(in) > maxFilteredKeywords {
		return nil, &SettingValidationError{
			Field: "dm_filtered_keywords", Code: SettingCodeTooManyKeywords,
			Value: fmt.Sprintf("%d entries", len(in)),
		}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		if !utf8.ValidString(raw) {
			return nil, &SettingValidationError{Field: "dm_filtered_keywords", Code: SettingCodeKeywordInvalid, Value: raw}
		}
		k := strings.ToLower(strings.Join(strings.Fields(raw), " "))
		if n := utf8.RuneCountInString(k); n < 1 || n > maxFilteredKeywordLen {
			return nil, &SettingValidationError{Field: "dm_filtered_keywords", Code: SettingCodeKeywordLength, Value: raw}
		}
		for _, r := range k {
			if unicode.IsControl(r) {
				return nil, &SettingValidationError{Field: "dm_filtered_keywords", Code: SettingCodeKeywordInvalid, Value: raw}
			}
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out, nil
}

// applyStrictMode forces the strictest values across the matrix when the user
// is in Strict Privacy Mode or is a minor (spec §5.4 — under-18 is server-
// enforced and cannot be loosened by the user).
func applyStrictMode(s *store.UserSettings) {
	if !s.StrictPrivacyMode && !s.Under18Mode {
		return
	}
	// DM consent model: only people this account follows may message it
	// (or no one, if that was chosen).
	clampMinorMessaging(s)
	s.WhoCanCall = "no_one"
	s.WhoCanAddToGroups = "no_one"
	s.WhoCanSeeOnlineStatus = "connections_only"
	s.WhoCanSeeReadReceipts = "connections_only"
	s.WhoCanSeeLastSeen = "connections_only"
	s.WhoCanSeeProfilePhoto = "connections_only"
	// Strangers may not @mention or tag a minor / strict-mode account:
	// "everyone" narrows to the people it follows; following and no_one
	// stay as chosen.
	if s.WhoCanMention == "everyone" {
		s.WhoCanMention = "following"
	}
	if s.WhoCanTag == "everyone" {
		s.WhoCanTag = "following"
	}
	s.BlockUnknownCalls = true
	s.AutoFilterAbusiveContent = true
	s.AllowPhoneDiscovery = false
	s.DiscoverableByPhoneToContacts = false
	// Chat parity: risky-sender requests stay filtered and sensitive media
	// stays blurred for minors and in Strict Privacy Mode.
	s.DmSafeMode = true
	s.DmSensitiveMediaFilter = true
}

// SettingsChangedPublisher announces committed writes to downstream
// permission caches. Implemented by events.Producer; nil-safe by contract.
//
// PublishSettingsChanged carries accountVisibility (the NEW value) so
// graph-service can act on a private→public flip — auto-accepting pending
// follow requests — without a synchronous read-back.
type SettingsChangedPublisher interface {
	PublishSettingsChanged(ctx context.Context, userID uuid.UUID, privacyVersion int, accountVisibility string)
	PublishModulesChanged(ctx context.Context, userID uuid.UUID, modules []string, homeModule string)
}

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	cfg      *config.Config
	log      *slog.Logger
	producer SettingsChangedPublisher
}

func New(s *store.Store, rdb *redis.Client, cfg *config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: s, rdb: rdb, cfg: cfg, log: logger}
}

// WithProducer wires the settings-changed publisher. Optional — without it,
// downstream caches rely on their TTLs alone. Call once from cmd/server.
func (s *Service) WithProducer(p SettingsChangedPublisher) *Service {
	s.producer = p
	return s
}

// CreateUser handles user creation from a UserRegistered event. under18 is
// derived from the registrant's date of birth and pins the account into the
// minor-safety profile (spec §5.4, §11.1).
//
// The settings row usually exists already: auth-service inserts it, with
// adult column defaults, in the registration transaction, so the store's
// INSERT is a no-op. For a minor the full Strict Privacy Mode profile
// (including who_can_message = people_i_follow) is therefore applied here, at
// creation, rather than waiting for the account's first settings save.
// Idempotent: a redelivered event finds nothing to change and writes nothing.
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID, under18 bool) error {
	if err := s.store.CreateUser(ctx, id, under18); err != nil {
		return err
	}
	if !under18 {
		return nil
	}
	cur, err := s.store.GetSettings(ctx, id)
	if err != nil {
		return err
	}
	if cur == nil {
		return fmt.Errorf("settings row missing after create for user %s", id)
	}
	next, changed, err := minorProfile(cur)
	if err != nil || !changed {
		return err
	}
	_, err = s.UpdateSettings(ctx, next)
	return err
}

// minorProfile returns cur pinned into the minor-safety profile: under-18
// mode on and the strict clamp applied. changed is false when the row already
// matches, so creation never writes (or bumps privacy_version) needlessly.
func minorProfile(cur *store.UserSettings) (*store.UserSettings, bool, error) {
	next := *cur
	next.DmFilteredKeywords = append([]string(nil), cur.DmFilteredKeywords...)
	next.Under18Mode = true
	if err := prepareSettings(&next); err != nil {
		return nil, false, err
	}
	before := *cur
	before.DmFilteredKeywords = keywordsOrEmpty(before.DmFilteredKeywords)
	return &next, !reflect.DeepEqual(before, next), nil
}

func keywordsOrEmpty(k []string) []string {
	if k == nil {
		return []string{}
	}
	return k
}

// GetUser returns core user record.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*store.User, error) {
	cacheKey := fmt.Sprintf("user:core:%s", id)
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var u store.User
		if jsonErr := json.Unmarshal([]byte(cached), &u); jsonErr == nil {
			return &u, nil
		} else {
			s.log.Warn("failed to unmarshal user cache", "err", jsonErr, "cache_key", cacheKey)
		}
	} else if err != nil && err != redis.Nil {
		s.log.Warn("failed to read user cache", "err", err, "cache_key", cacheKey)
	}

	u, err := s.store.GetUser(ctx, id)
	if err != nil || u == nil {
		return u, err
	}

	go func() {
		data, err := json.Marshal(u)
		if err != nil {
			s.log.Warn("failed to marshal user cache", "err", err, "cache_key", cacheKey)
			return
		}
		if err := s.rdb.Set(context.Background(), cacheKey, data, s.cfg.CacheTTL).Err(); err != nil {
			s.log.Warn("failed to set user cache", "err", err, "cache_key", cacheKey)
		}
	}()

	return u, nil
}

// ListUsers returns all active users with pagination.
func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]store.User, int, error) {
	return s.store.ListUsers(ctx, limit, offset)
}

// settingsCacheKey is the Redis key for a user's cached settings snapshot.
//
// Versioned (v3, mentions and tags 2026-10-24: a v2 entry would unmarshal
// who_can_mention / who_can_tag as "", which validation refuses, so the PUT
// read-modify-write would 400. v2, chat parity 2026-10-18): an entry written before the dm_* /
// chat_bubble_style fields existed would unmarshal with dm_safe_mode=false and
// chat_bubble_style="" — the wrong default, and a value the PUT
// read-modify-write would then refuse. Bump the version whenever a field with
// a non-zero default is added.
func settingsCacheKey(id uuid.UUID) string {
	return fmt.Sprintf("user:settings:v3:%s", id)
}

// legacySettingsCacheKey is the pre-v2 key. Still deleted on every write so a
// pod running the previous build during a rolling deploy never serves a stale
// snapshot after a change made through a new pod.
func legacySettingsCacheKey(id uuid.UUID) string {
	return fmt.Sprintf("user:settings:%s", id)
}

// v2SettingsCacheKey is the chat-parity key the previous build reads; deleted
// on every write for the same rolling-deploy reason.
func v2SettingsCacheKey(id uuid.UUID) string {
	return fmt.Sprintf("user:settings:v2:%s", id)
}

// invalidateSettingsCache drops the current and both older entries.
func (s *Service) invalidateSettingsCache(ctx context.Context, id uuid.UUID) {
	keys := []string{settingsCacheKey(id), v2SettingsCacheKey(id), legacySettingsCacheKey(id)}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		s.log.Warn("failed to delete settings cache", "err", err, "cache_keys", keys)
	}
}

// GetSettings returns user privacy settings.
func (s *Service) GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error) {
	cacheKey := settingsCacheKey(id)
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var us store.UserSettings
		if jsonErr := json.Unmarshal([]byte(cached), &us); jsonErr == nil {
			normaliseForRead(&us)
			return &us, nil
		} else {
			s.log.Warn("failed to unmarshal settings cache", "err", jsonErr, "cache_key", cacheKey)
		}
	} else if err != nil && err != redis.Nil {
		s.log.Warn("failed to read settings cache", "err", err, "cache_key", cacheKey)
	}

	us, err := s.store.GetSettings(ctx, id)
	if err != nil || us == nil {
		return us, err
	}
	normaliseForRead(us)

	go func() {
		data, err := json.Marshal(us)
		if err != nil {
			s.log.Warn("failed to marshal settings cache", "err", err, "cache_key", cacheKey)
			return
		}
		if err := s.rdb.Set(context.Background(), cacheKey, data, s.cfg.CacheTTL).Err(); err != nil {
			s.log.Warn("failed to set settings cache", "err", err, "cache_key", cacheKey)
		}
	}()

	return us, nil
}

// prepareSettings is the pure pre-write step: map legacy values, clamp for
// strict mode/minors, normalise the keyword list, then validate every enum.
// Split out so the rules are table-testable without Postgres or Redis.
func prepareSettings(settings *store.UserSettings) error {
	normaliseLegacySettings(settings)
	applyStrictMode(settings)
	keywords, err := NormaliseFilteredKeywords(settings.DmFilteredKeywords)
	if err != nil {
		return err
	}
	settings.DmFilteredKeywords = keywords
	return validatePrivacySettings(settings)
}

// UpdateSettings validates and persists privacy settings. Strict Privacy Mode
// and under-18 status clamp the matrix to strict values before the write.
func (s *Service) UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error) {
	if err := prepareSettings(settings); err != nil {
		return nil, err
	}

	us, err := s.store.UpdateSettings(ctx, settings)
	if err != nil {
		return nil, err
	}

	s.invalidateSettingsCache(ctx, settings.UserID)

	// Invalidation signal for downstream permission caches (graph privacy
	// cache, chat policy projection). Best-effort AFTER the committed write;
	// consumers re-fetch the snapshot, so a duplicate or late event is
	// harmless and a lost one is bounded by their TTL fallbacks.
	if s.producer != nil && us != nil {
		s.producer.PublishSettingsChanged(ctx, us.UserID, us.PrivacyVersion, us.AccountVisibility)
	}

	return us, nil
}
