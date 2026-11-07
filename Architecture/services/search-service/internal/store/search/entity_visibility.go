package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// Who may find a community or a broadcast channel (audit 2026-11-07, S-M2).
//
// Search used to index communities of every type and channels of every
// status, and filtered communities not at all, so a private, invite-only or
// suspended community's name, handle, description and owner came back to
// signed-out callers. The founder rule is "hidden when closed".
//
// The rule is applied twice, because the two halves fail differently:
//
//   - at index time, in IndexCommunity / IndexChannel, which every writer
//     (consumer and backfill) goes through: a document that is not
//     searchable is deleted instead of written;
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

// IndexCommunity writes a community document, or deletes it when it is not
// searchable (CommunitySearchable).
func (s *Store) IndexCommunity(ctx context.Context, doc CommunityDoc) error {
	if !CommunitySearchable(doc) {
		return s.DeleteCommunity(ctx, doc.CommunityID)
	}
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityCommunities, engagementCounters{Members: doc.MemberCount})
	}
	return s.indexDoc(ctx, IndexCommunities, doc.CommunityID, doc)
}

// IndexChannel writes a broadcast channel document, or deletes it when it
// is not searchable (ChannelSearchable).
func (s *Store) IndexChannel(ctx context.Context, doc ChannelDoc) error {
	if !ChannelSearchable(doc) {
		return s.DeleteChannel(ctx, doc.ChannelID)
	}
	if doc.EngagementScore == 0 {
		doc.EngagementScore = computeEngagementScore(EntityChannels, engagementCounters{Subscribers: doc.SubscriberCount})
	}
	return s.indexDoc(ctx, IndexChannels, doc.ChannelID, doc)
}

// UpdateCommunityFields applies a partial update to one community document
// (S-M4). community.updated carries only the name and type, and a full
// replace dropped owner_id (so blocks on the owner stopped applying), the
// handle, the counts and the status. A missing document stays missing: a
// partial update never creates one.
func (s *Store) UpdateCommunityFields(ctx context.Context, communityID string, fields map[string]any) error {
	if communityID == "" || len(fields) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]any{"doc": fields})
	if err != nil {
		return err
	}
	retries := bulkRetries
	req := opensearchapi.UpdateRequest{
		Index:           IndexCommunities,
		DocumentID:      communityID,
		Body:            bytes.NewReader(body),
		RetryOnConflict: &retries,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("update community %s: %s", communityID, res.String())
	}
	return nil
}
