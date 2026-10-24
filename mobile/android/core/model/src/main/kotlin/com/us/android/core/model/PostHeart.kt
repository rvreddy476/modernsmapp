package com.us.android.core.model

/*
 * The heart on a post, and which posts may be reposted (founder, 24 Oct
 * 2026; post-service commit 8370981c4, the web's postHeart.ts and
 * shareOptions.ts).
 *
 * Posts of every kind — feed posts, reels and long videos — take ONE
 * reaction, the like, drawn as a heart with its count. Comments keep the six
 * reactions; posts never offer them. A viewer who reacted before with an
 * older reaction (love, haha…) reads as liked.
 *
 * Reels take no repost and no quote repost anywhere (the server refuses
 * them with REPOST_NOT_ALLOWED). Feed posts keep both, and long videos are
 * unchanged for now. A reel cross-posted into the feed (`flick_embed`) is
 * still the reel, so it follows the reel rule.
 *
 * Pure, so the tests pin it down without rendering.
 */

/** The one reaction a post takes. */
const val POST_REACTION_LIKE = "like"

/** Content types that are a reel (short) or carry one. */
val REEL_CONTENT_TYPES: Set<String> = setOf("short", "reel", "flick", "flick_embed")

/** True when a post of [contentType] is a reel or carries one. */
fun isReelContentType(contentType: String?): Boolean =
    contentType != null && contentType.trim().lowercase() in REEL_CONTENT_TYPES

/** True when a post of [contentType] may be reposted or quoted. */
fun canRepostContentType(contentType: String?): Boolean = !isReelContentType(contentType)

/**
 * True when the viewer has reacted to the post with anything at all. Go
 * sends an empty string for "none", so a blank reaction is not liked.
 */
fun isPostLiked(hasReacted: Boolean, viewerReaction: String?): Boolean =
    hasReacted || !viewerReaction.isNullOrBlank()

/** Whether [this] row may be reposted or quoted: never a reel. */
val FeedItem.allowsRepost: Boolean get() = canRepostContentType(feedContentType)
