package store

import "fmt"

// One visibility rule for every list of questions (audit H5). A viewer sees
// a question when:
//
//   - it is not deleted (a hide is a delete);
//   - it is outside any community, or in a live community that is public,
//     or the viewer is a member of it (role member and above; "pending" is
//     not a member);
//   - it is not awaiting approval, unless the viewer wrote it or moderates
//     its community.
//
// The by-id reads apply the same rule in Go (service.questionVisibility).
// Keep the two in step.

// memberRolesSQL and moderatorRolesSQL mirror service.isCommunityMember and
// service.isCommunityModerator.
const (
	memberRolesSQL    = `('owner','admin','moderator','space_manager','expert','member')`
	moderatorRolesSQL = `('owner','admin','moderator')`
)

// questionVisibleSQL is the predicate over the questions alias q for the
// viewer in the query parameter viewerArg (e.g. "$3"); a NULL viewer is a
// signed-out caller.
func questionVisibleSQL(q, viewerArg string) string {
	v := viewerArg + "::uuid"
	return fmt.Sprintf(`(%[1]s.deleted_at IS NULL AND %[1]s.status <> 'deleted'
		AND (%[1]s.community_id IS NULL OR EXISTS (
			SELECT 1 FROM qa_communities vis_c
			WHERE vis_c.id = %[1]s.community_id AND vis_c.deleted_at IS NULL AND vis_c.status <> 'deleted'
			  AND (vis_c.community_type NOT IN ('private', 'invite') OR EXISTS (
				SELECT 1 FROM qa_community_members vis_m
				WHERE vis_m.community_id = vis_c.id AND vis_m.user_id = %[2]s AND vis_m.role IN %[3]s))))
		AND (%[1]s.status <> 'pending_approval' OR %[1]s.author_id = %[2]s OR EXISTS (
			SELECT 1 FROM qa_community_members vis_p
			WHERE vis_p.community_id = %[1]s.community_id AND vis_p.user_id = %[2]s AND vis_p.role IN %[4]s)))`,
		q, v, memberRolesSQL, moderatorRolesSQL)
}
