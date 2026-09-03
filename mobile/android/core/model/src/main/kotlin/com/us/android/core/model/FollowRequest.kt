package com.us.android.core.model

/**
 * One entry in `GET /v1/graph/follow-requests/incoming` — someone who asked
 * to follow the signed-in user's private account.
 *
 * The endpoint carries only the requester's id and when they asked; the
 * requester's name and avatar are resolved separately through the profile
 * endpoint, the same way a notification row resolves [Notification.actorName]
 * rather than the graph event carrying it.
 */
data class FollowRequest(
    val requesterId: String,
    val createdAt: String,
)
