package com.us.android.push

import com.us.android.core.model.NotificationTarget

/**
 * What a tapped upload push should open (Tube subscriptions, 2026-09-12).
 *
 * The push data carries `{type, entity_id, deep_link}`. The deep link is
 * the authority, parsed by the same [NotificationTarget.parse] the inbox
 * uses so a tap in the shade and a tap on the inbox row can never
 * disagree. When the link is missing or does not parse (a producer that
 * sent only the id, or a legacy row with an unknown shape), the entity id
 * still names the post and the type says which surface plays it, so that
 * is the fallback. A "went live" push (2026-10-02) falls back the same way
 * to the live viewer, and only when its entity id is a UUID
 * ([NotificationTarget.liveOf]). A type this build does not route yields [NotificationTarget.None]
 * and the nav host leaves the existing per-type routing to handle it.
 */
fun pushTargetOf(destination: PushDestination): NotificationTarget {
    val linked = NotificationTarget.parse(destination.deepLink)
    if (linked != NotificationTarget.None) return linked
    val postId = destination.entityId.trim()
    if (postId.isBlank()) return NotificationTarget.None
    return when (destination.type) {
        TYPE_UPLOADED_VIDEO -> NotificationTarget.Video(postId)
        TYPE_UPLOADED_FLICK -> NotificationTarget.Reel(postId)
        TYPE_WENT_LIVE -> NotificationTarget.liveOf(postId)
        else -> NotificationTarget.None
    }
}

/** The two upload types notification-service emits for a subscribed channel. */
const val TYPE_UPLOADED_VIDEO = "creator_uploaded_video"
const val TYPE_UPLOADED_FLICK = "creator_uploaded_flick"

/** A followed creator started a live stream; `entity_id` is the stream id. */
const val TYPE_WENT_LIVE = "creator_went_live"

/** What a tap on a live notification does to the back stack. */
enum class LiveOpen { STAY, REPLACE, PUSH }

/**
 * How the live viewer opens from a notification (2026-10-02), given the
 * stream the viewer is on right now ([watching], null when the current
 * screen is not the live viewer).
 *
 * The viewer's connection belongs to its ViewModel and ends only when its
 * entry leaves the back stack. So a tap for the stream already on screen
 * does nothing (a second join of the same room from one account is at best
 * a reconnect), a tap for a different stream REPLACES the viewer (stacking
 * would leave the first stream connected, and audible, underneath), and
 * from anywhere else the viewer is pushed like any other notification target.
 */
fun liveOpenOf(watching: String?, streamId: String): LiveOpen = when {
    watching == null -> LiveOpen.PUSH
    watching.equals(streamId, ignoreCase = true) -> LiveOpen.STAY
    else -> LiveOpen.REPLACE
}
