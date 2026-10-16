package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Admin sound library (2026-10-16): the curated library as the admin
// console manages it. admin-service is the only caller (a media:sounds.*
// service token, internal/http/admin_sounds_handler.go); every write is
// audited there with the acting admin and reason.
//
//	uploads   presigned PUT under audio-library/<uuid>.<ext>, 15 minutes,
//	          one of six audio content types, at most 20 MB
//	create    the object must exist (UPLOAD_MISSING), be audio by its bytes
//	          and by ffprobe with no video stream (NOT_AUDIO), and last at
//	          most 300 s (TOO_LONG); duration_ms is what ffprobe measured.
//	          A new row is a draft (is_active false) unless asked otherwise.
//	patch     metadata and is_active; a withdrawn row is never enabled
//	          again (WITHDRAWN) and an expired licence cannot be enabled
//	          (LICENSE_EXPIRED)
//	withdraw  new_uses: off for new posts; everywhere: also the linked
//	          audio_tracks row status 'deleted', silencing existing posts
//	preview   a short-lived presigned GET (any status)
//
// No storage key or object URL leaves the service except the presigned
// upload URL (and its key, which the console hands back on create) and the
// preview redirect.

// Limits.
const (
	MaxLibraryUploadBytes = 20 << 20
	MaxLibrarySoundMs     = 300_000
	LibraryUploadTTL      = 15 * time.Minute
	LibraryUploadPrefix   = "audio-library/"
	DefaultAdminSoundPage = 50
	MaxAdminSoundPage     = 100
)

// libraryUploadTypes maps an accepted upload content type to the key's
// extension.
var libraryUploadTypes = map[string]string{
	"audio/mpeg":  ".mp3",
	"audio/mp4":   ".m4a",
	"audio/aac":   ".aac",
	"audio/x-m4a": ".m4a",
	"audio/wav":   ".wav",
	"audio/ogg":   ".ogg",
}

// librarySniffed is what an upload's first bytes may say it is
// (processing.ValidateAudioMagicBytes; ADTS AAC sniffs as audio/mpeg).
var librarySniffed = map[string]bool{"audio/mpeg": true, "audio/mp4": true, "audio/wav": true, "audio/ogg": true}

var libraryUploadKey = regexp.MustCompile(`^audio-library/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(mp3|m4a|aac|wav|ogg)$`)

// License types.
var libraryLicenseTypes = map[string]bool{"licensed": true, "royalty_free": true, "original": true, "public_domain": true}

// Error codes.
const (
	CodeAdminSoundValidation = "VALIDATION_ERROR"
	CodeAdminSoundNotFound   = "NOT_FOUND"
	CodeUploadMissing        = "UPLOAD_MISSING"
	CodeNotAudio             = "NOT_AUDIO"
	CodeTooLong              = "TOO_LONG"
	CodeTooLarge             = "TOO_LARGE"
	CodeStorageKeyInUse      = "STORAGE_KEY_IN_USE"
	CodeSoundWithdrawn       = "WITHDRAWN"
	CodeLicenseExpired       = "LICENSE_EXPIRED"
	CodeSoundLibraryUnavail  = "SOUND_LIBRARY_UNAVAILABLE"
	CodeSoundPreviewUnavail  = "PREVIEW_UNAVAILABLE"
)

// AdminSoundError is an admin sound request refused with an HTTP status and
// a platform error code.
type AdminSoundError struct {
	Status  int
	Code    string
	Message string
	cause   error
}

func (e *AdminSoundError) Error() string {
	if e.cause != nil {
		return e.Code + ": " + e.Message + ": " + e.cause.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *AdminSoundError) Unwrap() error { return e.cause }

func invalid(msg string) error {
	return &AdminSoundError{Status: http.StatusBadRequest, Code: CodeAdminSoundValidation, Message: msg}
}

func unprocessable(code, msg string) error {
	return &AdminSoundError{Status: http.StatusUnprocessableEntity, Code: code, Message: msg}
}

func unavailable(err error) error {
	return &AdminSoundError{Status: http.StatusServiceUnavailable, Code: CodeSoundLibraryUnavail,
		Message: "The sound library is unavailable right now; retry", cause: err}
}

var errAdminSoundNotFound = &AdminSoundError{Status: http.StatusNotFound, Code: CodeAdminSoundNotFound, Message: "Sound not found"}

// AdminSoundStore is the store slice. *postgres.MediaAssetStore implements it.
type AdminSoundStore interface {
	ListAdminLibrarySounds(ctx context.Context, f postgres.AdminSoundFilter) ([]postgres.AdminLibrarySound, error)
	GetAdminLibrarySound(ctx context.Context, id uuid.UUID) (*postgres.AdminLibrarySound, error)
	AdminLibraryKeyInUse(ctx context.Context, key string) (bool, error)
	CreateAdminLibrarySound(ctx context.Context, in postgres.AdminLibrarySoundInsert) (*postgres.AdminLibrarySound, error)
	PatchAdminLibrarySound(ctx context.Context, id uuid.UUID, p postgres.AdminLibrarySoundPatch, now time.Time) (*postgres.AdminLibrarySound, error)
	WithdrawAdminLibrarySound(ctx context.Context, id uuid.UUID, scope, reason string) (*postgres.AdminLibrarySound, error)
}

// AdminSoundBlobs is the object-store slice. *blob.Store implements it.
type AdminSoundBlobs interface {
	GeneratePresignedPutURL(ctx context.Context, objectKey string, expiry time.Duration) (*url.URL, error)
	GeneratePresignedGetURL(ctx context.Context, objectKey string, expiry time.Duration) (*url.URL, error)
	StatObject(ctx context.Context, objectKey string) (blob.ObjectInfo, error)
	ReadObjectRange(ctx context.Context, objectKey string, start, end int64) ([]byte, error)
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
}

// AdminSounds is the feature. probe is ffprobe (tests replace it: the test
// machines have no ffmpeg).
type AdminSounds struct {
	store AdminSoundStore
	blobs AdminSoundBlobs
	probe func(ctx context.Context, path string) (*processing.AudioFileProbe, error)
	now   func() time.Time
	newID func() uuid.UUID
}

// NewAdminSounds wires the feature over explicit dependencies.
func NewAdminSounds(store AdminSoundStore, blobs AdminSoundBlobs) *AdminSounds {
	return &AdminSounds{store: store, blobs: blobs, probe: processing.ProbeAudioFile, now: time.Now, newID: uuid.New}
}

// WithProbe replaces ffprobe (tests).
func (a *AdminSounds) WithProbe(fn func(ctx context.Context, path string) (*processing.AudioFileProbe, error)) *AdminSounds {
	a.probe = fn
	return a
}

// WithClock replaces the clock (tests).
func (a *AdminSounds) WithClock(fn func() time.Time) *AdminSounds { a.now = fn; return a }

// AdminSounds is the feature over the service's own store and blob store.
func (s *Service) AdminSounds() *AdminSounds {
	return NewAdminSounds(s.pgStore, s.blobStore)
}

// ── list / get ──────────────────────────────────────────────────────────

// AdminSoundListQuery is GET …/library's query as sent.
type AdminSoundListQuery struct {
	Status, Q, Genre, Limit, Cursor string
}

// AdminSoundPage is one page and the cursor of the next ("" = last page).
type AdminSoundPage struct {
	Sounds     []postgres.AdminLibrarySound
	NextCursor string
}

// List answers one page, newest first.
func (a *AdminSounds) List(ctx context.Context, q AdminSoundListQuery) (*AdminSoundPage, error) {
	f := postgres.AdminSoundFilter{Status: strings.ToLower(strings.TrimSpace(q.Status)), Limit: DefaultAdminSoundPage}
	switch f.Status {
	case "", "all", postgres.LibraryStatusActive, postgres.LibraryStatusInactive,
		postgres.LibraryStatusDraft, postgres.LibraryStatusWithdrawn:
	default:
		return nil, invalid("status must be one of active, inactive, withdrawn, all")
	}
	if s := strings.TrimSpace(q.Limit); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return nil, invalid("limit must be a positive integer")
		}
		if n > MaxAdminSoundPage {
			n = MaxAdminSoundPage
		}
		f.Limit = n
	}
	if utf8.RuneCountInString(q.Q) > 200 || utf8.RuneCountInString(q.Genre) > 64 {
		return nil, invalid("q or genre is too long")
	}
	f.Q, f.Genre = q.Q, q.Genre
	if c := strings.TrimSpace(q.Cursor); c != "" {
		at, id, ok := decodeSoundCursor(c)
		if !ok {
			return nil, invalid("cursor is not valid")
		}
		f.AfterAt, f.AfterID = &at, id
	}
	want := f.Limit
	f.Limit = want + 1
	rows, err := a.store.ListAdminLibrarySounds(ctx, f)
	if err != nil {
		return nil, unavailable(err)
	}
	page := &AdminSoundPage{Sounds: rows}
	if len(rows) > want {
		page.Sounds = rows[:want]
		last := page.Sounds[want-1]
		page.NextCursor = encodeSoundCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func encodeSoundCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeSoundCursor(s string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	at, err1 := time.Parse(time.RFC3339Nano, ts)
	id, err2 := uuid.Parse(idStr)
	if err1 != nil || err2 != nil {
		return time.Time{}, uuid.Nil, false
	}
	return at, id, true
}

// Get answers one row in any status.
func (a *AdminSounds) Get(ctx context.Context, id uuid.UUID) (*postgres.AdminLibrarySound, error) {
	row, err := a.store.GetAdminLibrarySound(ctx, id)
	if err != nil {
		return nil, unavailable(err)
	}
	if row == nil {
		return nil, errAdminSoundNotFound
	}
	return row, nil
}

// ── uploads ─────────────────────────────────────────────────────────────

// LibraryUploadRequest is POST …/uploads.
type LibraryUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// LibraryUpload is the presigned PUT the console sends the file to.
type LibraryUpload struct {
	UploadURL  string            `json:"upload_url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
	StorageKey string            `json:"storage_key"`
	ExpiresAt  time.Time         `json:"expires_at"`
}

// PresignUpload issues a presigned PUT under the library prefix.
func (a *AdminSounds) PresignUpload(ctx context.Context, req LibraryUploadRequest) (*LibraryUpload, error) {
	name := strings.TrimSpace(req.Filename)
	if name == "" || utf8.RuneCountInString(name) > 255 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, invalid("filename is required (at most 255 characters)")
	}
	ct := strings.ToLower(strings.TrimSpace(req.ContentType))
	ext, ok := libraryUploadTypes[ct]
	if !ok {
		return nil, invalid("content_type must be one of audio/mpeg, audio/mp4, audio/aac, audio/x-m4a, audio/wav, audio/ogg")
	}
	if req.SizeBytes <= 0 || req.SizeBytes > MaxLibraryUploadBytes {
		return nil, invalid("size_bytes must be between 1 and 20 MB")
	}
	if a.blobs == nil {
		return nil, unavailable(errors.New("blob store not configured"))
	}
	key := LibraryUploadPrefix + a.newID().String() + ext
	u, err := a.blobs.GeneratePresignedPutURL(ctx, key, LibraryUploadTTL)
	if err != nil {
		return nil, unavailable(fmt.Errorf("presign upload: %w", err))
	}
	return &LibraryUpload{
		UploadURL:  u.String(),
		Method:     http.MethodPut,
		Headers:    map[string]string{"Content-Type": ct},
		StorageKey: key,
		ExpiresAt:  a.now().Add(LibraryUploadTTL).UTC(),
	}, nil
}

// ── create ──────────────────────────────────────────────────────────────

// LibrarySoundCreate is POST …/library.
type LibrarySoundCreate struct {
	StorageKey       string  `json:"storage_key"`
	Title            string  `json:"title"`
	Artist           string  `json:"artist"`
	Genre            string  `json:"genre"`
	Mood             *string `json:"mood"`
	LicenseType      string  `json:"license_type"`
	RightsHolder     string  `json:"rights_holder"`
	LicenseReference *string `json:"license_reference"`
	LicenseExpiresAt *string `json:"license_expires_at"`
	Attribution      *string `json:"attribution"`
	IsActive         *bool   `json:"is_active"`
}

// Create checks the upload and inserts the row as actor.
func (a *AdminSounds) Create(ctx context.Context, actor uuid.UUID, req LibrarySoundCreate) (*postgres.AdminLibrarySound, error) {
	in := postgres.AdminLibrarySoundInsert{CreatedBy: actor, IsActive: req.IsActive != nil && *req.IsActive}
	var err error
	in.StorageKey = strings.TrimSpace(req.StorageKey)
	if !libraryUploadKey.MatchString(in.StorageKey) {
		return nil, invalid("storage_key must be a key issued by the uploads route")
	}
	if in.Title, err = required("title", req.Title, 200); err != nil {
		return nil, err
	}
	if in.Artist, err = required("artist", req.Artist, 200); err != nil {
		return nil, err
	}
	if in.Genre, err = required("genre", req.Genre, 64); err != nil {
		return nil, err
	}
	in.Genre = strings.ToLower(in.Genre)
	if in.Mood, err = optional("mood", req.Mood, 64); err != nil {
		return nil, err
	}
	if in.Mood != nil {
		lower := strings.ToLower(*in.Mood)
		in.Mood = &lower
	}
	in.LicenseType = strings.TrimSpace(req.LicenseType)
	if !libraryLicenseTypes[in.LicenseType] {
		return nil, invalid("license_type must be one of licensed, royalty_free, original, public_domain")
	}
	if in.RightsHolder, err = required("rights_holder", req.RightsHolder, 200); err != nil {
		return nil, err
	}
	if in.LicenseReference, err = optional("license_reference", req.LicenseReference, 200); err != nil {
		return nil, err
	}
	if in.Attribution, err = optional("attribution", req.Attribution, 500); err != nil {
		return nil, err
	}
	if in.LicenseExpiresAt, err = optionalTime("license_expires_at", req.LicenseExpiresAt); err != nil {
		return nil, err
	}
	if in.IsActive && in.LicenseExpiresAt != nil && !in.LicenseExpiresAt.After(a.now()) {
		return nil, unprocessable(CodeLicenseExpired, "The licence has expired, so the sound cannot be enabled")
	}

	used, err := a.store.AdminLibraryKeyInUse(ctx, in.StorageKey)
	if err != nil {
		return nil, unavailable(err)
	}
	if used {
		return nil, &AdminSoundError{Status: http.StatusConflict, Code: CodeStorageKeyInUse, Message: "This upload already backs a library sound"}
	}
	durationMs, err := a.checkUpload(ctx, in.StorageKey)
	if err != nil {
		return nil, err
	}
	in.DurationMs = durationMs

	row, err := a.store.CreateAdminLibrarySound(ctx, in)
	if errors.Is(err, postgres.ErrLibraryKeyInUse) {
		return nil, &AdminSoundError{Status: http.StatusConflict, Code: CodeStorageKeyInUse, Message: "This upload already backs a library sound"}
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return row, nil
}

// checkUpload is the create gate over the stored object: it exists, is at
// most 20 MB, its bytes and ffprobe say audio with no video stream, and it
// lasts at most 300 s. Returns the measured duration.
func (a *AdminSounds) checkUpload(ctx context.Context, key string) (int, error) {
	if a.blobs == nil {
		return 0, unavailable(errors.New("blob store not configured"))
	}
	info, err := a.blobs.StatObject(ctx, key)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return 0, unprocessable(CodeUploadMissing, "Nothing was uploaded to this storage_key; upload the file first")
	}
	if err != nil {
		return 0, unavailable(fmt.Errorf("stat upload: %w", err))
	}
	if info.Size <= 0 {
		return 0, unprocessable(CodeNotAudio, "The uploaded file is empty")
	}
	if info.Size > MaxLibraryUploadBytes {
		return 0, unprocessable(CodeTooLarge, "The uploaded file is larger than 20 MB")
	}
	head, err := a.blobs.ReadObjectRange(ctx, key, 0, 63)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return 0, unprocessable(CodeUploadMissing, "Nothing was uploaded to this storage_key; upload the file first")
	}
	if err != nil {
		return 0, unavailable(fmt.Errorf("read upload header: %w", err))
	}
	if sniffed, ok := processing.ValidateAudioMagicBytes(head); !ok || !librarySniffed[sniffed] {
		return 0, unprocessable(CodeNotAudio, "The uploaded file is not MP3, AAC/M4A, WAV or Ogg audio")
	}
	data, err := a.blobs.DownloadObject(ctx, key)
	if err != nil {
		return 0, unavailable(fmt.Errorf("download upload: %w", err))
	}
	if len(data) == 0 || len(data) > MaxLibraryUploadBytes {
		return 0, unprocessable(CodeNotAudio, "The uploaded file could not be read as audio")
	}
	dir, err := os.MkdirTemp("", "library-sound-")
	if err != nil {
		return 0, unavailable(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "input"+filepath.Ext(key))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return 0, unavailable(err)
	}
	if a.probe == nil {
		return 0, unavailable(errors.New("audio probe not configured"))
	}
	p, err := a.probe(ctx, path)
	if err != nil || p == nil || !p.HasAudio || p.HasVideo || p.DurationMs <= 0 {
		return 0, unprocessable(CodeNotAudio, "The uploaded file could not be decoded as audio (or it carries video)")
	}
	if p.DurationMs > MaxLibrarySoundMs {
		return 0, unprocessable(CodeTooLong, "The sound is longer than 5 minutes (300 s)")
	}
	return p.DurationMs, nil
}

// ── patch ───────────────────────────────────────────────────────────────

// patchable are the PATCH fields; reason is accepted (admin-service audits
// it) and ignored.
var patchable = map[string]bool{
	"title": true, "artist": true, "genre": true, "mood": true, "rights_holder": true,
	"license_reference": true, "license_expires_at": true, "attribution": true, "is_active": true,
	"reason": true,
}

// Patch applies an edit given as the raw JSON object.
func (a *AdminSounds) Patch(ctx context.Context, id uuid.UUID, body []byte) (*postgres.AdminLibrarySound, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, invalid("The body must be a JSON object")
	}
	var p postgres.AdminLibrarySoundPatch
	changed := 0
	for k, raw := range fields {
		if !patchable[k] {
			return nil, invalid(k + " cannot be changed")
		}
		if k == "reason" {
			continue
		}
		changed++
		isNull := strings.TrimSpace(string(raw)) == "null"
		switch k {
		case "is_active":
			var v bool
			if isNull || json.Unmarshal(raw, &v) != nil {
				return nil, invalid("is_active must be true or false")
			}
			p.IsActive = &v
		case "license_expires_at":
			p.LicenseExpiresAt.Set = true
			if isNull {
				continue
			}
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return nil, invalid("license_expires_at must be an RFC 3339 timestamp or null")
			}
			t, err := optionalTime(k, &s)
			if err != nil {
				return nil, err
			}
			p.LicenseExpiresAt.Value = t
		default:
			var s *string
			if !isNull {
				var v string
				if json.Unmarshal(raw, &v) != nil {
					return nil, invalid(k + " must be a string")
				}
				s = &v
			}
			opt, err := patchString(k, s)
			if err != nil {
				return nil, err
			}
			switch k {
			case "title":
				p.Title = opt
			case "artist":
				p.Artist = opt
			case "genre":
				p.Genre = opt
			case "mood":
				p.Mood = opt
			case "rights_holder":
				p.RightsHolder = opt
			case "license_reference":
				p.LicenseReference = opt
			case "attribution":
				p.Attribution = opt
			}
		}
	}
	if changed == 0 {
		return nil, invalid("Nothing to change")
	}
	row, err := a.store.PatchAdminLibrarySound(ctx, id, p, a.now())
	switch {
	case errors.Is(err, postgres.ErrLibrarySoundWithdrawn):
		return nil, &AdminSoundError{Status: http.StatusConflict, Code: CodeSoundWithdrawn, Message: "A withdrawn sound cannot be enabled again"}
	case errors.Is(err, postgres.ErrLibraryLicenseExpired):
		return nil, unprocessable(CodeLicenseExpired, "The licence has expired, so the sound cannot be enabled")
	case err != nil:
		return nil, unavailable(err)
	case row == nil:
		return nil, errAdminSoundNotFound
	}
	return row, nil
}

// patchString validates one string PATCH field: required ones cannot be
// null or blank, optional ones clear on null or blank.
func patchString(field string, v *string) (postgres.OptString, error) {
	limits := map[string]int{"title": 200, "artist": 200, "genre": 64, "rights_holder": 200,
		"mood": 64, "license_reference": 200, "attribution": 500}
	max := limits[field]
	switch field {
	case "title", "artist", "genre", "rights_holder":
		if v == nil {
			return postgres.OptString{}, invalid(field + " cannot be cleared")
		}
		s, err := required(field, *v, max)
		if err != nil {
			return postgres.OptString{}, err
		}
		if field == "genre" {
			s = strings.ToLower(s)
		}
		return postgres.OptString{Set: true, Value: &s}, nil
	default:
		s, err := optional(field, v, max)
		if err != nil {
			return postgres.OptString{}, err
		}
		if s != nil && field == "mood" {
			lower := strings.ToLower(*s)
			s = &lower
		}
		return postgres.OptString{Set: true, Value: s}, nil
	}
}

// ── withdraw / preview ──────────────────────────────────────────────────

// LibrarySoundWithdraw is POST …/:id/withdraw.
type LibrarySoundWithdraw struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason"`
}

// Withdraw takes the sound out of new uses, and with scope everywhere out
// of the posts already using it.
func (a *AdminSounds) Withdraw(ctx context.Context, id uuid.UUID, req LibrarySoundWithdraw) (*postgres.AdminLibrarySound, error) {
	scope := strings.TrimSpace(req.Scope)
	if scope != postgres.WithdrawNewUses && scope != postgres.WithdrawEverywhere {
		return nil, invalid("scope must be new_uses or everywhere")
	}
	reason := strings.TrimSpace(req.Reason)
	if n := utf8.RuneCountInString(reason); n < 3 || n > 500 {
		return nil, invalid("reason is required (3 to 500 characters)")
	}
	row, err := a.store.WithdrawAdminLibrarySound(ctx, id, scope, reason)
	if err != nil {
		return nil, unavailable(err)
	}
	if row == nil {
		return nil, errAdminSoundNotFound
	}
	return row, nil
}

// PreviewURL is a short-lived presigned GET of the row's audio, for any
// status (a draft is previewed before it is enabled). A legacy row whose
// audio is an https URL answers that URL.
func (a *AdminSounds) PreviewURL(ctx context.Context, id uuid.UUID) (string, error) {
	row, err := a.Get(ctx, id)
	if err != nil {
		return "", err
	}
	src, ok := LibraryAudioSource(row.AudioURL)
	if !ok {
		return "", unprocessable(CodeSoundPreviewUnavail, "This sound has no audio to play")
	}
	u, err := signSoundAudio(ctx, a.blobs, src, true)
	if err != nil {
		return "", unavailable(err)
	}
	return u, nil
}

// ── field helpers ───────────────────────────────────────────────────────

func required(field, v string, max int) (string, error) {
	s := strings.TrimSpace(v)
	if s == "" || utf8.RuneCountInString(s) > max || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", invalid(fmt.Sprintf("%s is required (at most %d characters)", field, max))
	}
	return s, nil
}

func optional(field string, v *string, max int) (*string, error) {
	if v == nil {
		return nil, nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(s) > max || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' }) >= 0 {
		return nil, invalid(fmt.Sprintf("%s must be at most %d characters", field, max))
	}
	return &s, nil
}

func optionalTime(field string, v *string) (*time.Time, error) {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*v))
	if err != nil {
		return nil, invalid(field + " must be an RFC 3339 timestamp")
	}
	t = t.UTC()
	return &t, nil
}
