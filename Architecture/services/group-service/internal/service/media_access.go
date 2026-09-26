package service

import (
	"context"

	"github.com/google/uuid"
)

/*
	The group content authority for protected media delivery.

	media-service asks POST /v1/internal/groups/media-access (and /batch)
	"may viewer V receive asset M?". The rule is the same one every group
	read already applies, restated once for media:

	  - the asset must be referenced by a published post of a group, or be a
	    group's avatar or cover;
	  - the group must not be deleted;
	  - a private group's content is for its active members only — the same
	    rule as checkGroupAccess; a public or restricted group's content is
	    readable by any signed-in viewer;
	  - a viewer banned from the group is refused whatever the privacy.

	Any one qualifying reference is enough (the same photo may sit in two
	groups). An asset nothing in groups references is denied HERE — another
	authority (post, chat, profile, commerce) may still say yes, and
	media-service takes the first yes.

	This is deliberately a decision about the viewer and the group, never
	about the uploader: an anonymous post's photo must be readable by exactly
	the people who can read the post, and must not become readable by
	somebody who merely knows who uploaded it.
*/

type MediaAccessDecision string

const (
	MediaAllowed MediaAccessDecision = "allowed"
	MediaDenied  MediaAccessDecision = "denied"
)

type MediaAccessResult struct {
	Allowed  bool
	Decision MediaAccessDecision
	Reason   string
}

func denied(reason string) MediaAccessResult {
	return MediaAccessResult{Allowed: false, Decision: MediaDenied, Reason: reason}
}

// ViewerMayAccessMedia answers for one asset.
func (s *Service) ViewerMayAccessMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (MediaAccessResult, error) {
	res, err := s.ViewerMayAccessMediaBatch(ctx, viewerID, []uuid.UUID{mediaID})
	if err != nil {
		return MediaAccessResult{}, err
	}
	return res[mediaID], nil
}

// ViewerMayAccessMediaBatch answers for a page of assets with one reference
// query and at most one membership/ban read per group touched.
func (s *Service) ViewerMayAccessMediaBatch(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]MediaAccessResult, error) {
	out := make(map[uuid.UUID]MediaAccessResult, len(mediaIDs))
	for _, id := range mediaIDs {
		out[id] = denied("no_group_reference")
	}
	if viewerID == uuid.Nil || len(mediaIDs) == 0 {
		return out, nil
	}
	refs, err := s.store.FindMediaReferences(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}

	type standing struct {
		member, banned bool
	}
	byGroup := map[uuid.UUID]standing{}
	lookup := func(groupID uuid.UUID) (standing, error) {
		if st, ok := byGroup[groupID]; ok {
			return st, nil
		}
		member, err := s.store.CheckMembership(ctx, groupID, viewerID)
		if err != nil {
			return standing{}, err
		}
		banned, err := s.store.CheckBanned(ctx, groupID, viewerID)
		if err != nil {
			return standing{}, err
		}
		st := standing{member: member, banned: banned}
		byGroup[groupID] = st
		return st, nil
	}

	for _, ref := range refs {
		if out[ref.MediaID].Allowed {
			continue // one yes is enough
		}
		if ref.GroupStatus == "deleted" {
			out[ref.MediaID] = denied("group_deleted")
			continue
		}
		st, err := lookup(ref.GroupID)
		if err != nil {
			return nil, err
		}
		if st.banned {
			out[ref.MediaID] = denied("banned")
			continue
		}
		private := ref.PrivacyLevel == "private" || ref.Visibility == "private"
		if private && !st.member {
			out[ref.MediaID] = denied("not_a_member")
			continue
		}
		out[ref.MediaID] = MediaAccessResult{Allowed: true, Decision: MediaAllowed, Reason: "group_" + ref.Kind}
	}
	return out, nil
}
