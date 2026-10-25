package service

import (
	"errors"
	"net/http"
)

// PostEditErrorStatus is the one error -> (status, code) table for the owner
// edit routes: PATCH /v1/posts/:postId, the bulk routes (whose per-id
// outcomes carry the code), the private-share routes and the Creator Hub
// reads' refusals. http/post_edit.go writes it; the bulk routes put the code
// in each failed outcome, so a post refused inside a batch reads exactly
// like the same post refused by the single PATCH.
func PostEditErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, ErrPostNotFound), errors.Is(err, ErrPostNotVisible):
		return http.StatusNotFound, "NOT_FOUND"
	// Members-only gate (members_tiers.go): 400 / 503 on every route that
	// sets it, create and PATCH alike.
	case errors.Is(err, ErrInvalidTier):
		return http.StatusBadRequest, "INVALID_TIER"
	case errors.Is(err, ErrTierLookupUnavailable):
		return http.StatusServiceUnavailable, "MEMBERSHIP_TIERS_UNAVAILABLE"
	// Video credits (credits.go): 400 on every route, create and PATCH
	// alike, as the credits contract pins.
	case errors.Is(err, ErrCreditsNotAllowed):
		return http.StatusBadRequest, "CREDITS_NOT_ALLOWED"
	case errors.Is(err, ErrTooManyCredits):
		return http.StatusBadRequest, "TOO_MANY_CREDITS"
	case errors.Is(err, ErrInvalidCredit):
		return http.StatusBadRequest, "INVALID_CREDIT"
	case errors.Is(err, ErrInvalidGenre):
		return http.StatusBadRequest, "INVALID_GENRE"
	case errors.Is(err, ErrInvalidReleaseYear):
		return http.StatusBadRequest, "INVALID_RELEASE_YEAR"
	case errors.Is(err, ErrInvalidProductionHouse):
		return http.StatusBadRequest, "INVALID_PRODUCTION_HOUSE"
	case errors.Is(err, ErrNotPostAuthor), errors.Is(err, ErrPostForbidden):
		return http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, ErrMediaNotOwned):
		return http.StatusForbidden, "MEDIA_NOT_OWNED"
	case errors.Is(err, ErrAgeSignIn):
		return http.StatusUnauthorized, "AGE_RESTRICTED_SIGN_IN"
	case errors.Is(err, ErrAgeRestricted):
		return http.StatusForbidden, "AGE_RESTRICTED"
	case errors.Is(err, ErrAgeUnverified):
		return http.StatusForbidden, "AGE_UNVERIFIED"
	case errors.Is(err, ErrInvalidCategory):
		return http.StatusUnprocessableEntity, "INVALID_CATEGORY"
	case errors.Is(err, ErrInvalidVisibility):
		return http.StatusUnprocessableEntity, "INVALID_VISIBILITY"
	case errors.Is(err, ErrTitleTooLong):
		return http.StatusUnprocessableEntity, "TITLE_TOO_LONG"
	case errors.Is(err, ErrTitleRequired):
		return http.StatusUnprocessableEntity, "TITLE_REQUIRED"
	case errors.Is(err, ErrTextTooLong):
		return http.StatusUnprocessableEntity, "TEXT_TOO_LONG"
	case errors.Is(err, ErrEmptyPost):
		return http.StatusUnprocessableEntity, "EMPTY_POST"
	case errors.Is(err, ErrInvalidHashtag):
		return http.StatusUnprocessableEntity, "INVALID_HASHTAG"
	case errors.Is(err, ErrTooManyHashtags):
		return http.StatusUnprocessableEntity, "TOO_MANY_HASHTAGS"
	case errors.Is(err, ErrInvalidMention):
		return http.StatusUnprocessableEntity, "INVALID_MENTION"
	case errors.Is(err, ErrTooManyMentions):
		return http.StatusUnprocessableEntity, "TOO_MANY_MENTIONS"
	case errors.Is(err, ErrTooManyTaggedUsers), errors.Is(err, ErrInvalidTaggedUser):
		return http.StatusUnprocessableEntity, "INVALID_TAGGED_USERS"
	case errors.Is(err, ErrInvalidLanguage):
		return http.StatusUnprocessableEntity, "INVALID_LANGUAGE"
	case errors.Is(err, ErrTooManyTags), errors.Is(err, ErrTagTooLong):
		return http.StatusUnprocessableEntity, "INVALID_TAGS"
	case errors.Is(err, ErrMediaNotFound):
		return http.StatusUnprocessableEntity, "MEDIA_NOT_FOUND"
	case errors.Is(err, ErrMediaNotReady):
		return http.StatusUnprocessableEntity, "MEDIA_NOT_READY"
	case errors.Is(err, ErrMediaTypeMismatch):
		return http.StatusUnprocessableEntity, "MEDIA_TYPE_MISMATCH"
	case errors.Is(err, ErrInvalidLicense):
		return http.StatusUnprocessableEntity, "INVALID_LICENSE"
	case errors.Is(err, ErrInvalidRecordingDate):
		return http.StatusUnprocessableEntity, "INVALID_RECORDING_DATE"
	case errors.Is(err, ErrInvalidRecordingLocation):
		return http.StatusUnprocessableEntity, "INVALID_RECORDING_LOCATION"
	case errors.Is(err, ErrInvalidLinkURL):
		return http.StatusUnprocessableEntity, "INVALID_LINK_URL"
	case errors.Is(err, ErrInvalidRemixSetting):
		return http.StatusUnprocessableEntity, "INVALID_REMIX_SETTING"
	case errors.Is(err, ErrInvalidCommentModeration):
		return http.StatusUnprocessableEntity, "INVALID_COMMENT_MODERATION"
	case errors.Is(err, ErrInvalidCommentAccess):
		return http.StatusUnprocessableEntity, "INVALID_COMMENT_ACCESS"
	case errors.Is(err, ErrInvalidCommentSort):
		return http.StatusUnprocessableEntity, "INVALID_COMMENT_SORT"
	case errors.Is(err, ErrRelatedNotFound):
		return http.StatusUnprocessableEntity, "RELATED_NOT_FOUND"
	case errors.Is(err, ErrRelatedSelf):
		return http.StatusUnprocessableEntity, "RELATED_SELF"
	case errors.Is(err, ErrTooManyShares):
		return http.StatusUnprocessableEntity, "TOO_MANY_SHARES"
	case errors.Is(err, ErrInvalidShareUser):
		return http.StatusUnprocessableEntity, "INVALID_USER"
	case errors.Is(err, ErrBulkNothing), errors.Is(err, ErrBulkTooMany),
		errors.Is(err, ErrBulkEmptyPatch), errors.Is(err, ErrBulkTagsMode):
		return http.StatusUnprocessableEntity, "INVALID_REQUEST"
	// Media claims (media_claims.go): media-service could not record the
	// reference, so nothing was written. A retry can succeed.
	case errors.Is(err, ErrMediaClaimsUnavailable):
		return http.StatusServiceUnavailable, "MEDIA_CLAIMS_UNAVAILABLE"
	case errors.Is(err, ErrAuthoringStoreUnavailable):
		return http.StatusServiceUnavailable, "STORE_UNAVAILABLE"
	case errors.Is(err, ErrShareUsersUnknown):
		return http.StatusServiceUnavailable, "PROFILES_UNAVAILABLE"
	default:
		return http.StatusInternalServerError, "INTERNAL_ERROR"
	}
}
