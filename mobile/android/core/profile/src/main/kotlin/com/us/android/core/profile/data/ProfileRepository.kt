package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileStats
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The one place profile data is fetched and mapped into domain models.
 *
 * Mapping lives here rather than in the ViewModel so DTOs never reach the UI
 * layer: a Compose component that knows about `@SerialName` is a component
 * that cannot be reused when the wire format changes.
 */
@Singleton
class ProfileRepository @Inject constructor(
    private val api: ProfileApi,
    private val errorMapper: ErrorMapper,
) {

    /** Another user's profile. Public projection — no private fields. */
    suspend fun getProfile(userId: String): AppResult<Profile> =
        apiCall(errorMapper) { api.getProfile(userId) }.map { it.toDomain() }

    /** The signed-in user's own profile, including the private fields. */
    suspend fun getOwnProfile(): AppResult<Profile> =
        apiCall(errorMapper) { api.getOwnProfile() }.map { it.toDomain() }

    /**
     * Saves the owner's editable fields and returns the profile the server
     * stored.
     *
     * The parameter is a complete [EditableProfile] and there is no per-field
     * overload, because `PUT /v1/profiles/me` replaces rather than patches.
     * A signature like `updateProfile(displayName: String?)` would let a
     * caller express "just this one field", which the endpoint would honour by
     * erasing the rest. The only way to reach this function is to have loaded
     * a snapshot first.
     *
     * The response is the saved owner projection, so callers re-seed their
     * form from it rather than from what they sent — the server is the
     * authority on what was actually stored.
     */
    suspend fun updateProfile(snapshot: EditableProfile): AppResult<Profile> =
        apiCall(errorMapper) { api.updateProfile(snapshot.toRequest()) }.map { it.toDomain() }

    suspend fun getStats(userId: String): AppResult<ProfileStats> =
        apiCall(errorMapper) { api.getStats(userId) }.map { it.toDomain() }

    suspend fun follow(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.follow(GraphUserIdRequest(userId)) }.map { }

    suspend fun unfollow(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.unfollow(GraphUserIdRequest(userId)) }.map { }

    suspend fun block(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.block(GraphUserIdRequest(userId)) }.map { }

    suspend fun unblock(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.unblock(GraphUserIdRequest(userId)) }.map { }
}

/**
 * The one and only place an [UpdateProfileRequest] is constructed.
 *
 * Private, and takes a whole snapshot, so no other file can assemble a body
 * with a field missing. Because [UpdateProfileRequest] declares no Kotlin
 * defaults, this mapping also fails to compile the moment a new editable field
 * is added to either type without being wired through — which is exactly the
 * failure mode that a defaulted DTO would have turned into a silent field
 * erasure at runtime.
 */
private fun EditableProfile.toRequest() = UpdateProfileRequest(
    profileThemeColor = profileThemeColor,
    website = website,
    profession = profession,
    displayName = displayName,
    location = location,
    category = category,
    bio = bio,
)

private fun PublicProfileDto.toDomain() = Profile(
    userId = userId,
    displayName = displayName,
    bio = bio,
    category = category,
    profession = profession,
    website = website,
    location = location,
    badgeFlags = badgeFlags,
    isVerified = isVerified,
    verificationLevel = verificationLevel,
    statusText = statusText,
    statusEmoji = statusEmoji,
    profileThemeColor = profileThemeColor,
    memberSinceBadge = memberSinceBadge,
    counts = ProfileCounts(
        followers = followerCount,
        following = followingCount,
        friends = friendCount,
        posts = postCount,
    ),
    createdAt = createdAt,
    // Null, and that is the whole point: the server did not disclose these
    // fields, which is different from the user not having filled them in.
    personal = null,
)

private fun OwnProfileDto.toDomain() = Profile(
    userId = userId,
    displayName = displayName,
    bio = bio,
    category = category,
    profession = profession,
    website = website,
    location = location,
    badgeFlags = badgeFlags,
    isVerified = isVerified,
    verificationLevel = verificationLevel,
    statusText = statusText,
    statusEmoji = statusEmoji,
    profileThemeColor = profileThemeColor,
    memberSinceBadge = memberSinceBadge,
    counts = ProfileCounts(
        followers = followerCount,
        following = followingCount,
        friends = friendCount,
        posts = postCount,
    ),
    createdAt = createdAt,
    personal = PersonalProfile(
        firstName = firstName,
        lastName = lastName,
        dateOfBirth = dob,
        gender = gender,
        timezone = timezone,
        introMediaUrl = introMediaUrl,
        ctaUrl = ctaUrl,
        updatedAt = updatedAt,
    ),
)

private fun ProfileStatsDto.toDomain() = ProfileStats(
    counts = ProfileCounts(
        followers = followerCount,
        following = followingCount,
        friends = friendCount,
        posts = postCount,
    ),
    totalSparks = totalSparks,
    isCreator = isCreator,
)
