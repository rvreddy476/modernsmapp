package http

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) applyProfilePhotoPrivacy(c *gin.Context, profile *PublicProfile) *PublicProfile {
	if profile == nil || (profile.AvatarMedia == nil && profile.CoverMedia == nil) {
		return profile
	}
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	if h.photos == nil {
		h.redactProfileMedia(profile)
		return profile
	}
	allowed, err := h.photos.CanViewProfilePhoto(c.Request.Context(), viewerID, profile.UserID)
	if err != nil || !allowed {
		if err != nil {
			h.log.Error("profile-photo privacy unresolved; redacting", "err", err, "owner_id", profile.UserID)
		}
		h.redactProfileMedia(profile)
	}
	return profile
}

func (h *Handler) applyProfilePhotoPrivacyList(
	c *gin.Context,
	profiles []*PublicProfile,
) []*PublicProfile {
	if len(profiles) == 0 {
		return profiles
	}
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id"))
	checker, ok := h.photos.(profilePhotoBatchAccessChecker)
	if !ok {
		for _, profile := range profiles {
			h.redactProfileMedia(profile)
		}
		return profiles
	}
	owners := make([]uuid.UUID, 0, len(profiles))
	for _, profile := range profiles {
		if profile != nil {
			owners = append(owners, profile.UserID)
		}
	}
	allowed, err := checker.CanViewProfilePhotos(c.Request.Context(), viewerID, owners)
	if err != nil {
		h.log.Error("profile-photo batch privacy unresolved; redacting page", "err", err)
		allowed = map[uuid.UUID]bool{}
	}
	for _, profile := range profiles {
		if profile != nil && !allowed[profile.UserID] {
			h.redactProfileMedia(profile)
		}
	}
	return profiles
}

func (h *Handler) applyProfilePhotoPrivacyMap(
	c *gin.Context,
	profiles map[uuid.UUID]*PublicProfile,
) map[uuid.UUID]*PublicProfile {
	list := make([]*PublicProfile, 0, len(profiles))
	for _, profile := range profiles {
		list = append(list, profile)
	}
	h.applyProfilePhotoPrivacyList(c, list)
	return profiles
}

func (h *Handler) redactProfileMedia(profile *PublicProfile) {
	profile.AvatarMedia = nil
	profile.CoverMedia = nil
}
