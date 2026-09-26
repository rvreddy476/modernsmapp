package store

import (
	"context"

	"github.com/google/uuid"
)

/*
	Which group content references a media asset.

	media-service owns assets but not audiences: before it hands out the bytes
	of a protected asset it asks each content authority "may this viewer have
	this?". For a group post's photo the answer lives here — the post's group,
	its privacy, and whether the viewer is inside it. Until this existed no
	authority claimed group media, so every member who was not the uploader
	got "Media not found" for every attachment in every group.

	A reference is a published, non-deleted group post whose attachments
	(a JSONB array of media-id strings) contain the asset, or a group whose
	avatar or cover it is. The rows carry the group's privacy and status so
	the service can decide without a second read per reference.
*/

// MediaReference is one place a media asset is used inside groups.
type MediaReference struct {
	MediaID      uuid.UUID
	GroupID      uuid.UUID
	Kind         string // "post" | "avatar" | "cover"
	GroupStatus  string
	PrivacyLevel string
	Visibility   string
	PostAuthorID string // "" for avatar/cover
}

// FindMediaReferences returns every group reference of each asset in one
// query. An asset nothing references is simply absent from the result.
func (s *Store) FindMediaReferences(ctx context.Context, mediaIDs []uuid.UUID) ([]MediaReference, error) {
	if len(mediaIDs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(mediaIDs))
	for i, id := range mediaIDs {
		ids[i] = id.String()
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.media_id::uuid, g.id, 'post', g.status, g.privacy_level, g.visibility, p.author_id
		FROM group_posts p
		JOIN LATERAL jsonb_array_elements_text(p.attachments) AS a(media_id) ON a.media_id = ANY($1)
		JOIN groups g ON g.id = p.group_id
		WHERE p.status = 'published'
		  AND p.attachments IS NOT NULL AND jsonb_typeof(p.attachments) = 'array'
		UNION ALL
		SELECT g.avatar_media_id, g.id, 'avatar', g.status, g.privacy_level, g.visibility, ''
		FROM groups g WHERE g.avatar_media_id::text = ANY($1)
		UNION ALL
		SELECT g.cover_media_id, g.id, 'cover', g.status, g.privacy_level, g.visibility, ''
		FROM groups g WHERE g.cover_media_id::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaReference
	for rows.Next() {
		var r MediaReference
		if err := rows.Scan(&r.MediaID, &r.GroupID, &r.Kind, &r.GroupStatus, &r.PrivacyLevel, &r.Visibility, &r.PostAuthorID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
