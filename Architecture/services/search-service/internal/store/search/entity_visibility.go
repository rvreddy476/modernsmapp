package search

import "context"

// Who may find a community or a broadcast channel (audit 2026-11-07, S-M2).
//
// Search used to index communities of every type and channels of every
// status, and filtered communities not at all, so a private, invite-only or
// suspended community's name, handle, description and owner came back to
// signed-out callers. The founder rule is "hidden when closed".
//
// The rule is applied twice, because the two halves fail differently:
//
//   - at index time, in ApplyCommunityState / ApplyChannelState, which every
//     writer (consumer and backfill) goes through: a document that is not
//     searchable becomes a tombstone (entity_state.go) instead;
//   - at query time, in every query over the two indices: type in the
//     public set AND status = active. A document without a status matches
//     nothing (fail closed), so documents written before this field existed
//     stay out of search until the backfill rewrites them.

// StatusActive is the only community / channel status search shows.
// community-service and channel-service share the vocabulary
// active | suspended | archived | deleted.
const StatusActive = "active"

// publicCommunityTypes is community-service's public set (AuthorizeViewer
// treats exactly these as public; audit K-L2). private and invite are not
// discoverable.
var publicCommunityTypes = []string{"public", "education", "professional"}

func inSet(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// CommunitySearchable reports whether a community document may be in the
// index at all.
func CommunitySearchable(d CommunityDoc) bool {
	return d.Status == StatusActive && inSet(d.CommunityType, publicCommunityTypes)
}

// ChannelSearchable reports whether a broadcast channel document may be in
// the index at all.
func ChannelSearchable(d ChannelDoc) bool {
	return d.Status == StatusActive && inSet(d.ChannelType, publicChannelTypes)
}

// IsPublicCommunityType reports whether a community type is discoverable.
func IsPublicCommunityType(t string) bool { return inSet(t, publicCommunityTypes) }

// activeStatusFilter is the query-time status half of the rule.
func activeStatusFilter() map[string]any {
	return map[string]any{"term": map[string]any{"status": StatusActive}}
}

// publicCommunityFilterAny is the query-time filter for communities_v1.
func publicCommunityFilterAny() []map[string]any {
	return []map[string]any{
		{"terms": map[string]any{"community_type": publicCommunityTypes}},
		activeStatusFilter(),
	}
}

// ApplyCommunityState writes a community's full state — community-service's
// CommunitySnapshot on community.created / community.updated, the backfill's
// row, or an older producer's four created fields — in snapshot order
// (entity_state.go): a write older than the stored one is dropped.
//
//   - Deleted (status deleted): a terminal tombstone (DeleteCommunity).
//   - Not searchable (archived, suspended, a type outside the public set):
//     a tombstone at the snapshot's version (HideCommunity).
//   - Searchable: the snapshot's fields are merged into a live document,
//     leaving engagement_score alone (the member events keep it); a missing
//     document, or a tombstone the snapshot is newer than, becomes the whole
//     document, so a community made public again comes back into search
//     without waiting on the backfill.
//
// Every field is written even when empty: the snapshot is the row, so an
// empty description means the description was cleared. doc.SourceVersion is
// the snapshot's version (SourceVersion of its updated_at); zero for an
// older event, which then never overwrites a versioned document.
func (s *Store) ApplyCommunityState(ctx context.Context, doc CommunityDoc) error {
	if doc.CommunityID == "" {
		return nil
	}
	if doc.Status == StatusDeleted {
		return s.DeleteCommunity(ctx, doc.CommunityID)
	}
	if !CommunitySearchable(doc) {
		return s.HideCommunity(ctx, doc.CommunityID, doc.SourceVersion)
	}
	tags := doc.TopicTags
	if tags == nil {
		tags = []string{}
	}
	fields := map[string]any{
		"community_id":   doc.CommunityID,
		"owner_id":       doc.OwnerID,
		"handle":         doc.Handle,
		"name":           doc.Name,
		"description":    doc.Description,
		"community_type": doc.CommunityType,
		"category":       doc.Category,
		"topic_tags":     tags,
		"member_count":   doc.MemberCount,
		"is_verified":    doc.IsVerified,
		"status":         doc.Status,
	}
	if !doc.CreatedAt.IsZero() {
		fields["created_at"] = doc.CreatedAt
	}
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityCommunities, engagementCounters{Members: doc.MemberCount})
	}
	return s.writeEntity(ctx, communityTarget, entityWrite{
		op: entityUpsert, id: doc.CommunityID, version: doc.SourceVersion,
		fields: fields, doc: doc, engagement: doc.EngagementScore,
	})
}

// HideCommunity takes a community out of search at the given version
// (zero: unversioned) without forgetting it: a tombstone, so a stale
// snapshot that still says active cannot bring it back (entity_state.go).
func (s *Store) HideCommunity(ctx context.Context, communityID string, version int64) error {
	return s.writeEntity(ctx, communityTarget, entityWrite{op: entityRemove, id: communityID, version: version})
}

// DeleteCommunity removes a deleted community for good: a terminal
// tombstone that no later snapshot, however new, writes over.
func (s *Store) DeleteCommunity(ctx context.Context, communityID string) error {
	return s.writeEntity(ctx, communityTarget, entityWrite{op: entityDelete, id: communityID})
}

// UpdateCommunityFields applies a partial update to one community document
// (S-M4), for community.updated from producers older than the snapshot,
// which carry only the name, the type and the status. A full replace
// dropped owner_id (so blocks on the owner stopped applying), the handle,
// the counts and the status.
//
// It lands only on a live, unversioned document (entity_state.go, patch): a
// missing document stays missing (there is no owner to write, and a
// document without owner_id escapes the block filter), a tombstone stays a
// tombstone (fields on it could make it match the public filter), and a
// versioned document is newer than any event without a version.
func (s *Store) UpdateCommunityFields(ctx context.Context, communityID string, fields map[string]any) error {
	if communityID == "" || len(fields) == 0 {
		return nil
	}
	return s.writeEntity(ctx, communityTarget, entityWrite{op: entityPatch, id: communityID, fields: fields})
}

// ApplyChannelState is ApplyCommunityState for broadcast channels:
// channel-service's ChannelSnapshot on channel.created / channel.updated
// (since 2026-11-07), the backfill's row, or an older producer's created
// fields, applied in snapshot order. Deleted is a terminal tombstone; not
// searchable (suspended, archived, a type outside the public set) is a
// tombstone at the snapshot's version; searchable merges the fields into a
// live document and leaves engagement_score to the subscribe / unsubscribe
// events, or creates the whole document.
func (s *Store) ApplyChannelState(ctx context.Context, doc ChannelDoc) error {
	if doc.ChannelID == "" {
		return nil
	}
	if doc.Status == StatusDeleted {
		return s.DeleteChannel(ctx, doc.ChannelID)
	}
	if !ChannelSearchable(doc) {
		return s.writeEntity(ctx, channelTarget, entityWrite{op: entityRemove, id: doc.ChannelID, version: doc.SourceVersion})
	}
	fields := map[string]any{
		"channel_id":       doc.ChannelID,
		"owner_id":         doc.OwnerID,
		"handle":           doc.Handle,
		"name":             doc.Name,
		"description":      doc.Description,
		"channel_type":     doc.ChannelType,
		"category":         doc.Category,
		"subscriber_count": doc.SubscriberCount,
		"is_verified":      doc.IsVerified,
		"status":           doc.Status,
	}
	if !doc.CreatedAt.IsZero() {
		fields["created_at"] = doc.CreatedAt
	}
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityChannels, engagementCounters{Subscribers: doc.SubscriberCount})
	}
	return s.writeEntity(ctx, channelTarget, entityWrite{
		op: entityUpsert, id: doc.ChannelID, version: doc.SourceVersion,
		fields: fields, doc: doc, engagement: doc.EngagementScore,
	})
}

// DeleteChannel removes a deleted channel for good: a terminal tombstone.
func (s *Store) DeleteChannel(ctx context.Context, channelID string) error {
	return s.writeEntity(ctx, channelTarget, entityWrite{op: entityDelete, id: channelID})
}
