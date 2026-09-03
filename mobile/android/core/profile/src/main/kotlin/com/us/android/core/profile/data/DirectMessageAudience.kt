package com.us.android.core.profile.data

/**
 * The TikTok-style "who can message you" picker, mapped onto the six raw
 * `who_can_message` values the server actually understands.
 *
 * The picker shows three independent rows — Everyone, Followers, Friends —
 * each with two states (the audience-appropriate "on" state, or "Don't
 * receive"). The server has one enum, not three booleans, so [fromRaw] and
 * [toRaw] are the two directions of a lossy-on-the-fringes mapping:
 *
 *  - `everyone_message_requests` -> Everyone=on, Followers=on, Friends=on
 *  - `followers_message_requests` -> Everyone=off, Followers=on, Friends=on
 *  - `connections_only` -> Everyone=off, Followers=off, Friends=on
 *  - `no_one` -> Everyone=off, Followers=off, Friends=off
 *
 * `connections_and_mutual_followers` and `friends_of_friends_requests` have
 * no row of their own in this simplified picker. Both are "narrower than
 * everyone, wider than no one" policies, so [fromRaw] shows them the same way
 * it shows [RAW_CONNECTIONS_ONLY]: Friends=on, everything else off. Selecting
 * ANY row afterwards replaces the raw value outright — there is no way to
 * express those two values from this screen, matching the product decision
 * to retire them from the UI while the server keeps accepting them for
 * existing accounts.
 */
data class DirectMessageAudience(
    val everyoneRequests: Boolean,
    val followersRequests: Boolean,
    val friendsDirect: Boolean,
) {
    /**
     * The raw value this state maps to, in priority order: the widest
     * audience with its row "on" wins. This is what makes a row toggle
     * deterministic — flipping one row picks the nearest value rather than
     * requiring the other two to be reconciled by hand.
     */
    fun toRaw(): String = when {
        everyoneRequests -> RAW_EVERYONE
        followersRequests -> RAW_FOLLOWERS
        friendsDirect -> RAW_CONNECTIONS_ONLY
        else -> RAW_NO_ONE
    }

    companion object {
        const val RAW_NO_ONE = "no_one"
        const val RAW_CONNECTIONS_ONLY = "connections_only"
        const val RAW_FOLLOWERS = "followers_message_requests"
        const val RAW_EVERYONE = "everyone_message_requests"

        fun fromRaw(value: String): DirectMessageAudience = when (value) {
            RAW_EVERYONE -> DirectMessageAudience(true, true, true)
            RAW_FOLLOWERS -> DirectMessageAudience(false, true, true)
            RAW_NO_ONE -> DirectMessageAudience(false, false, false)
            // RAW_CONNECTIONS_ONLY and the two retired values all land here.
            else -> DirectMessageAudience(false, false, true)
        }
    }
}
