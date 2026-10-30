package http

// Topic administration on the admin-service token family (audit M3). Topics
// are created, renamed, featured and given icons only here; POST
// /v1/qa/topics answers 410. An icon must be platform media: a media id or
// this platform's /v1/media/<id>/serve path, stored as the serve path, so a
// featured topic can never make every viewer's browser fetch from an
// arbitrary host.

import (
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermTopicsManage creates and edits topics, featuring and icons included.
// NEW: not in identity's catalogue yet.
const PermTopicsManage = "qa:topics.manage"

// CodeTopicIconInvalid refuses an icon that is not platform media.
const CodeTopicIconInvalid = "QA_TOPIC_ICON_INVALID"

const (
	maxTopicNameLen        = 80
	maxTopicSlugLen        = 80
	maxTopicDescriptionLen = 1000
)

var (
	mediaServePath = regexp.MustCompile(`^/v1/media/([0-9a-fA-F-]{36})/serve(?:/[a-z0-9_-]+)?$`)
	topicSlugShape = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// platformMediaIcon reads an icon reference: "" (no icon), a bare media id,
// or /v1/media/<id>/serve[/variant]. Anything else, an https URL on any
// host included, is refused. It returns the stored form.
func platformMediaIcon(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", true
	}
	if id, err := uuid.Parse(v); err == nil && len(v) == 36 {
		if id == uuid.Nil {
			return "", false
		}
		return "/v1/media/" + id.String() + "/serve", true
	}
	m := mediaServePath.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	id, err := uuid.Parse(m[1])
	if err != nil || id == uuid.Nil {
		return "", false
	}
	return v, true
}

type adminTopicBody struct {
	Reason        string  `json:"reason"`
	Name          *string `json:"name"`
	Slug          string  `json:"slug"`
	Description   *string `json:"description"`
	IconURL       *string `json:"icon_url"`
	ParentTopicID string  `json:"parent_topic_id"`
	IsFeatured    *bool   `json:"is_featured"`
}

func bindAdminTopic(c *gin.Context) (*adminTopicBody, bool) {
	var body adminTopicBody
	ctx := c.Request.Context()
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", "body must be JSON", nil)
		return nil, false
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "REASON_REQUIRED", "reason is required", nil)
		return nil, false
	}
	bad := func(msg string) bool {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", msg, nil)
		return false
	}
	if len(body.Reason) > 2000 {
		return nil, bad("reason is too long")
	}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n == "" || utf8.RuneCountInString(n) > maxTopicNameLen {
			return nil, bad("name must be 1 to 80 characters")
		}
		body.Name = &n
	}
	if body.Slug != "" && (len(body.Slug) > maxTopicSlugLen || !topicSlugShape.MatchString(body.Slug)) {
		return nil, bad("slug must be lowercase letters, digits and single hyphens")
	}
	if body.Description != nil && utf8.RuneCountInString(*body.Description) > maxTopicDescriptionLen {
		return nil, bad("description is too long")
	}
	if body.IconURL != nil {
		icon, ok := platformMediaIcon(*body.IconURL)
		if !ok {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeTopicIconInvalid,
				"icon_url must be an uploaded image (a media id or /v1/media/<id>/serve)", nil)
			return nil, false
		}
		body.IconURL = &icon
	}
	return &body, true
}

// AdminCreateTopic — POST .../topics (qa:topics.manage).
func (h *Handler) AdminCreateTopic(c *gin.Context) {
	body, ok := bindAdminTopic(c)
	if !ok {
		return
	}
	if body.Name == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "name is required", nil)
		return
	}
	p := store.CreateTopicParams{Name: *body.Name, Slug: body.Slug}
	if body.Description != nil {
		p.Description = *body.Description
	}
	if body.IconURL != nil {
		p.IconURL = *body.IconURL
	}
	if body.IsFeatured != nil {
		p.IsFeatured = *body.IsFeatured
	}
	if strings.TrimSpace(body.ParentTopicID) != "" {
		parent, ok := parseUUIDString(c, body.ParentTopicID, "parent_topic_id")
		if !ok {
			return
		}
		p.ParentTopicID = &parent
	}
	topic, err := h.admin().AdminCreateTopic(c.Request.Context(), adminActor(c), p, body.Reason)
	respondAdminTopic(c, topic, err)
}

// AdminUpdateTopic — PATCH .../topics/:topicId (qa:topics.manage): name,
// description, icon_url, is_featured.
func (h *Handler) AdminUpdateTopic(c *gin.Context) {
	id, ok := parseUUID(c, "topicId")
	if !ok {
		return
	}
	body, ok := bindAdminTopic(c)
	if !ok {
		return
	}
	topic, err := h.admin().AdminUpdateTopic(c.Request.Context(), adminActor(c), id, store.AdminTopicUpdate{
		Name: body.Name, Description: body.Description, IconURL: body.IconURL, IsFeatured: body.IsFeatured,
	}, body.Reason)
	respondAdminTopic(c, topic, err)
}

func respondAdminTopic(c *gin.Context, topic *store.Topic, err error) {
	if err != nil {
		respondAdminWrite(c, nil, err)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, topic)
}
