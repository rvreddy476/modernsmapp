package service

// Upload notification routing shared by the enqueue side
// (events/subscriber_fanout.go) and the delivery side, so the two can
// never disagree about which content types notify or where a tap lands.

// UploadNotifType maps a post content type onto the subscriber-facing
// notification type. Empty means "not an upload": text, photo and poll
// posts flow through the feed and never push-spam subscribers.
func UploadNotifType(contentType string) string {
	switch contentType {
	case "video", "long_video":
		return "creator_uploaded_video"
	case "flick", "reel":
		return "creator_uploaded_flick"
	}
	return ""
}

// UploadDeepLink is the canonical in-app route for an upload: the Tube
// watch page for a long video, the reels player for a flick. The web
// zones and the Android deep-link table key on these exact paths, so a
// change here is a cross-client contract change. The former
// "/posttube/watch/{id}" route no longer exists anywhere. Empty for
// non-uploads.
func UploadDeepLink(contentType, postID string) string {
	switch UploadNotifType(contentType) {
	case "creator_uploaded_video":
		return "/tube/watch/" + postID
	case "creator_uploaded_flick":
		return "/reels/" + postID
	}
	return ""
}
