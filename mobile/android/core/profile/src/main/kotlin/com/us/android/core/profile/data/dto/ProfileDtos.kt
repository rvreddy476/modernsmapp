package com.us.android.core.profile.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Wire types for profile-service, transcribed from live capture on 2026-08-16
 * and the 2026-08-17 repair recapture (prompt/android-api-contracts.md §5) —
 * not from the Go handler structs.
 *
 * Every field on a RESPONSE type carries a default. That is not defensive
 * habit: the same capture showed `renditions` arriving as `null` where a list
 * was expected, and showed `PUT /v1/profiles/me` with an empty body clearing
 * string fields to `""` rather than leaving them absent. A missing or null
 * field must degrade to an empty value, never throw during deserialization and
 * lose the whole screen.
 *
 * [UpdateProfileRequest] inverts that rule and carries NO defaults at all. The
 * reason is spelled out on the type; do not "make it consistent" with the
 * response DTOs above it.
 */

/**
 * `GET /v1/profiles/:userId` — the public projection.
 *
 * Auth: none required (capture confirmed 200 anonymous).
 *
 * This intentionally has NO first name, last name, `dob`, `gender`,
 * `timezone`, `intro_media_url`, `cta_url` or `updated_at`. The server does not
 * send them to a third party. Adding the fields here "just in case" would
 * produce nulls indistinguishable from a user who left them blank.
 */
@Serializable
data class PublicProfileDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    val bio: String = "",
    val category: String = "",
    val profession: String = "",
    val website: String = "",
    val location: String = "",
    @SerialName("badge_flags") val badgeFlags: Int = 0,
    @SerialName("is_verified") val isVerified: Boolean = false,
    @SerialName("verification_level") val verificationLevel: String = "",
    @SerialName("status_text") val statusText: String = "",
    @SerialName("status_emoji") val statusEmoji: String = "",
    @SerialName("profile_theme_color") val profileThemeColor: String = "",
    @SerialName("intro_media_type") val introMediaType: String = "",
    @SerialName("cta_label") val ctaLabel: String = "",
    @SerialName("member_since_badge") val memberSinceBadge: Boolean = false,
    @SerialName("follower_count") val followerCount: Int = 0,
    @SerialName("following_count") val followingCount: Int = 0,
    @SerialName("friend_count") val friendCount: Int = 0,
    @SerialName("post_count") val postCount: Int = 0,
    @SerialName("created_at") val createdAt: String = "",
)

/**
 * `GET /v1/profiles/me` — the owner projection.
 *
 * Auth: access JWT. Without one the capture returned
 * `401 UNAUTHORIZED / "Missing access token"`.
 *
 * A superset of [PublicProfileDto]. `dob` arrives as a full ISO-8601 instant
 * (`1990-01-01T00:00:00Z`), not the `yyyy-MM-dd` string registration accepts —
 * so the request and response formats for the same concept differ.
 */
@Serializable
data class OwnProfileDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("first_name") val firstName: String = "",
    @SerialName("last_name") val lastName: String = "",
    val bio: String = "",
    val dob: String? = null,
    val gender: String = "",
    val category: String = "",
    val profession: String = "",
    val website: String = "",
    val location: String = "",
    @SerialName("badge_flags") val badgeFlags: Int = 0,
    @SerialName("is_verified") val isVerified: Boolean = false,
    @SerialName("verification_level") val verificationLevel: String = "",
    @SerialName("status_text") val statusText: String = "",
    @SerialName("status_emoji") val statusEmoji: String = "",
    @SerialName("profile_theme_color") val profileThemeColor: String = "",
    @SerialName("intro_media_url") val introMediaUrl: String = "",
    @SerialName("intro_media_type") val introMediaType: String = "",
    @SerialName("cta_label") val ctaLabel: String = "",
    @SerialName("cta_url") val ctaUrl: String = "",
    @SerialName("member_since_badge") val memberSinceBadge: Boolean = false,
    val timezone: String = "",
    @SerialName("follower_count") val followerCount: Int = 0,
    @SerialName("following_count") val followingCount: Int = 0,
    @SerialName("friend_count") val friendCount: Int = 0,
    @SerialName("post_count") val postCount: Int = 0,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/**
 * Body for `PUT /v1/profiles/me` — auth: `Authorization: Bearer <access JWT>`.
 *
 * NOT ONE PROPERTY HERE HAS A KOTLIN DEFAULT, AND THAT IS THE ENTIRE DESIGN.
 *
 * Two captured facts combine into a data-loss bug if this type is written the
 * obvious way:
 *
 *  1. `PUT` is a FULL REPLACEMENT, not a patch. The capture sent `{}`, got a
 *     `200`, and the account came back with `display_name`, `category`,
 *     `profession` and `location` cleared to `""`. An omitted key is not
 *     "leave it alone" — it is "erase it".
 *  2. The app-wide `Json` leaves `encodeDefaults` at its default of FALSE
 *     (`:core:network` `NetworkModule.provideJson`). kotlinx omits any
 *     property whose value equals its DECLARED KOTLIN DEFAULT.
 *
 * So `val bio: String = ""` would look harmless and would silently drop `bio`
 * from the body for every user who has no bio — turning a save of unrelated
 * fields into an erasure of that one. With no declared default there is no
 * value kotlinx can consider "default", so all seven keys are always on the
 * wire, and the Kotlin compiler additionally refuses to construct this type
 * without every field. Partial bodies become unrepresentable rather than
 * merely discouraged.
 *
 * Property ORDER is also load-bearing, though only for the tests: kotlinx
 * serializes in declaration order, so this order reproduces the captured
 * request body byte for byte and lets `EditProfileContractTest` assert against
 * recorded evidence instead of a re-derived string.
 *
 * The seven fields are exactly the ones the capture proved the server accepts.
 * `first_name`, `last_name`, `dob`, `gender` and `timezone` appear on the `/me`
 * RESPONSE but were never observed in an accepted request body, so they are not
 * claimed here — and because this is a full replacement, guessing a field name
 * the server ignores would be indistinguishable from a field that never saves.
 */
@Serializable
data class UpdateProfileRequest(
    @SerialName("profile_theme_color") val profileThemeColor: String,
    val website: String,
    val profession: String,
    @SerialName("display_name") val displayName: String,
    val location: String,
    val category: String,
    val bio: String,
)

/**
 * `GET /v1/profiles/:userId/stats` — auth: none.
 *
 * Overlaps the count fields on the profile payload and adds two of its own.
 * The overlap is why the domain model folds both into one counts object: two
 * sources for "follower_count" must never render as two different numbers.
 */
@Serializable
data class ProfileStatsDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("post_count") val postCount: Int = 0,
    @SerialName("follower_count") val followerCount: Int = 0,
    @SerialName("following_count") val followingCount: Int = 0,
    @SerialName("friend_count") val friendCount: Int = 0,
    @SerialName("total_sparks") val totalSparks: Int = 0,
    @SerialName("is_creator") val isCreator: Boolean = false,
    @SerialName("updated_at") val updatedAt: String = "",
)

/**
 * Body for every graph mutation this feature performs.
 *
 * Captured validation: omitting the field returns `INVALID_REQUEST` with
 * `Key: 'UserIDRequest.UserID' ... failed on the 'required' tag`.
 */
@Serializable
data class GraphUserIdRequest(
    @SerialName("user_id") val userId: String,
)

/**
 * Response to follow/unfollow/block/unblock.
 *
 * Captured values of [status]: `followed`, `unfollowed`, `blocked`,
 * `unblocked`. The client does not branch on the string — a 2xx is the signal,
 * and the field is kept only so the envelope has a payload to deserialize.
 */
@Serializable
data class GraphStatusDto(
    val status: String = "",
)
