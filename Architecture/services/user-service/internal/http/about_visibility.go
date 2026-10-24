package http

import "github.com/atpost/user-service/internal/service"

// aboutRelationship is the slice of GET /v1/graph/relationship the About
// audience reads. The call names the VIEWER as user_id and the profile OWNER
// as other_id, so on the wire:
//
//	follows             the viewer follows the owner (the viewer is a follower)
//	followed_by         the owner follows the viewer
//	is_connection       a legacy connection (none can form after 24 Oct 2026)
//	blocked             the owner blocked the viewer
//	blocked_by          the viewer blocked the owner
//
// graph-service never sends an `is_friend` key. Reading one hid every
// "friends" item from everyone, and IsFollower used to come from
// followed_by — the owner following the viewer, the wrong direction.
type aboutRelationship struct {
	Follows             bool `json:"follows"`
	FollowedBy          bool `json:"followed_by"`
	IsConnection        bool `json:"is_connection"`
	Blocked             bool `json:"blocked"`
	BlockedBy           bool `json:"blocked_by"`
	ViewerBlockedTarget bool `json:"viewer_blocked_target"`
	TargetBlockedViewer bool `json:"target_blocked_viewer"`
}

// aboutAccessFromRelationship maps the relationship onto the About audiences:
//
//	followers → the viewer follows the owner, or is a friend
//	friends   → graph-service's "friends" bond (resolveComment's rule for
//	            allow_comments_from = friends): a MUTUAL follow, or a legacy
//	            connection
//
// A block in either direction admits nothing beyond public items. Graph
// removes follows when a block lands, so this is belt and braces against a
// stale cache entry or a connection row a block did not clear.
func aboutAccessFromRelationship(rel aboutRelationship) service.ViewerAccess {
	if rel.Blocked || rel.BlockedBy || rel.ViewerBlockedTarget || rel.TargetBlockedViewer {
		return service.ViewerAccess{}
	}
	return service.ViewerAccess{
		IsFollower: rel.Follows,
		IsFriend:   rel.IsConnection || (rel.Follows && rel.FollowedBy),
	}
}
