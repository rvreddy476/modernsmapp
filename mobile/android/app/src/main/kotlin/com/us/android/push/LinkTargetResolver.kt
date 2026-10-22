package com.us.android.push

import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.profile.data.UsernameLookupRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Turns the handle a web link carries into the user id the screen takes
 * (2026-10-22): `cleestudio.com/u/<username>` names a person by handle but
 * the profile screen is `ProfileRoute(userId)`, and
 * `/posttube/channel/<handle>` names a channel by handle but the channel
 * screen is `TubeChannelRoute(userId)` (its videos and subscription edge
 * are read by user id). The nav host asks here, once, before it navigates;
 * a null answer means "no such handle, or the read failed", and the host
 * falls back to the search screen with the handle typed in.
 */
@Singleton
class LinkTargetResolver @Inject constructor(
    private val usernames: UsernameLookupRepository,
    private val channels: ChannelRepository,
) {

    /** The user id for a [PushDestinations.HANDLE_LINK_TYPES] destination, or null. Other types answer null too. */
    suspend fun userIdFor(destination: PushDestination): String? {
        val handle = destination.entityId.trim().removePrefix("@").takeIf { it.isNotEmpty() } ?: return null
        return when (destination.type) {
            PushDestinations.TYPE_PROFILE_LINK -> usernames.userIdOf(handle)
            PushDestinations.TYPE_CHANNEL_LINK ->
                (channels.channel(handle) as? AppResult.Success)?.data?.userId?.takeIf { it.isNotBlank() }
            else -> null
        }
    }
}
