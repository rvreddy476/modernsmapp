package com.us.android.core.model

/**
 * A user profile as the product understands it.
 *
 * TWO DTOs, ONE DOMAIN MODEL — AND THE DIFFERENCE MATTERS
 *
 * The backend serves two genuinely different payloads and the client must not
 * flatten them into one. Verified by live capture on 2026-08-16
 * (prompt/android-api-contracts.md §5):
 *
 *  - `GET /v1/profiles/:userId` — public. Omits first name, last name, `dob`,
 *    `gender`, `timezone`, `intro_media_url` and `cta_url`.
 *  - `GET /v1/profiles/me` — private. Carries all of them.
 *
 * Modelling the public response with the private shape would produce a
 * `Profile` whose `dateOfBirth` is silently null for every other user, which
 * reads exactly like "this person did not set a birthday". The distinction is
 * carried by [personal] being null rather than by absent fields, so a screen
 * cannot accidentally render someone else's private data as empty state.
 *
 * No avatar field appears in either captured payload. `avatar_media_id` exists
 * in the database but is not serialized, so there is nothing to load and no
 * image dependency is justified yet. When the backend starts returning it,
 * add it here and recapture — do not construct a media URL from a storage key,
 * which the capture explicitly warns against (§3).
 */
data class Profile(
    val userId: String,
    val username: String = "",
    val displayName: String,
    val pronouns: String = "",
    val avatarMediaId: String? = null,
    val coverMediaId: String? = null,
    val bio: String,
    val category: String,
    val profession: String,
    val website: String,
    val location: String,
    val badgeFlags: Int,
    val isVerified: Boolean,
    val verificationLevel: String,
    val statusText: String,
    val statusEmoji: String,
    val statusExpiresAt: String? = null,
    val profileThemeColor: String,
    val memberSinceBadge: Boolean,
    val introMediaUrl: String = "",
    val introMediaType: String = "",
    val ctaLabel: String = "",
    val ctaUrl: String = "",
    val counts: ProfileCounts,
    val createdAt: String,
    /**
     * Present only for the signed-in user's own profile. Null for everyone
     * else — and null means "not disclosed to this viewer", never "empty".
     */
    val personal: PersonalProfile? = null,
) {
    /** True when this instance came from `/me` and may show private fields. */
    val isOwnProfile: Boolean get() = personal != null

    /**
     * What the UI should show as a name. The captured `PUT /v1/profiles/me`
     * behaviour makes this necessary rather than defensive: sending `{}` is a
     * full replacement that clears `display_name` to the empty string, so a
     * blank name is a state real accounts can reach.
     */
    val nameForDisplay: String
        get() = displayName.ifBlank {
            personal?.let { "${it.firstName} ${it.lastName}".trim() }?.ifBlank { null }
                ?: "Unnamed"
        }
}

/** Fields the backend discloses only to the profile's owner. */
data class PersonalProfile(
    val firstName: String,
    val lastName: String,
    val preferredName: String = "",
    /** ISO-8601 instant, e.g. `1990-01-01T00:00:00Z` — not a plain date. */
    val dateOfBirth: String?,
    val gender: String,
    val timezone: String,
    val introMediaUrl: String,
    val ctaUrl: String,
    val updatedAt: String,
)

/**
 * Counters shown on the profile header.
 *
 * These arrive from two different endpoints with overlapping fields: the
 * profile payload carries follower/following/friend/post counts, and
 * `GET /v1/profiles/:userId/stats` carries those plus `total_sparks` and
 * `is_creator`. They are modelled once so a screen showing both cannot
 * display two disagreeing follower counts.
 */
data class ProfileCounts(
    val followers: Int,
    val following: Int,
    val friends: Int,
    val posts: Int,
)

/** The extra fields only the stats endpoint returns. */
data class ProfileStats(
    val counts: ProfileCounts,
    val totalSparks: Int,
    val isCreator: Boolean,
)

/**
 * The viewer's relationship to a profile.
 *
 * Deliberately NOT fetched from `GET /v1/graph/blocked-and-muted`. That route
 * accepts any `user_id`, requires no authentication, and returns another
 * account's block list to an anonymous caller (capture §4). Calling it would
 * make the client a participant in a privacy hole. Until the backend binds it
 * to the authenticated viewer, relationship state is only ever derived from
 * actions this device performed, which is why [isFollowing] and [isBlocked]
 * default to the honest "unknown" of `false` and are corrected optimistically.
 */
data class ProfileRelationship(
    val isFollowing: Boolean = false,
    val isBlocked: Boolean = false,
    /**
     * The friend edge: `none`, `pending_sent`, `pending_received` or
     * `accepted` — graph-service's words, not the client's. Empty when the
     * relationship has not been fetched.
     */
    val connectionStatus: String = "",
) {
    val isFriend: Boolean get() = connectionStatus == "accepted"
}
