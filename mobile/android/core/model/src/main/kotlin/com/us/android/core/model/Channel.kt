package com.us.android.core.model

/**
 * A creator's channel (Tube, 2026-09-05): the identity a long video is
 * published under. One per user, keyed by the user's id; the handle is
 * the public address (`@handle`) and is unique across the platform.
 *
 * [videoCount] is the server's count of the channel's long videos, carried
 * so a channel page and the You page can say it without a second call.
 */
data class Channel(
    val userId: String,
    val name: String,
    val handle: String,
    val about: String = "",
    val avatarMediaId: String? = null,
    val avatarUrl: String? = null,
    val videoCount: Int = 0,
    val createdAt: String = "",
    val updatedAt: String = "",
) {
    /** `@handle`, as it is shown everywhere. */
    val handleForDisplay: String get() = "@$handle"
}

/**
 * The channel a long video was posted under, as the feed embeds it beside
 * the author (Tube, 2026-09-05). Present only on `long_video` rows from a
 * server that has channels; a card that finds it shows the channel's name
 * and `@handle` in place of the author's display name.
 */
data class FeedChannel(
    val userId: String,
    val name: String,
    val handle: String,
    val avatarUrl: String? = null,
) {
    val handleForDisplay: String get() = "@$handle"
}
