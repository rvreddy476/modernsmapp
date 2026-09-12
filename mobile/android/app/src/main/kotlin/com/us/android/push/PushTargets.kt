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
 * is the fallback. A type this build does not route yields [NotificationTarget.None]
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
        else -> NotificationTarget.None
    }
}

/** The two upload types notification-service emits for a subscribed channel. */
const val TYPE_UPLOADED_VIDEO = "creator_uploaded_video"
const val TYPE_UPLOADED_FLICK = "creator_uploaded_flick"
