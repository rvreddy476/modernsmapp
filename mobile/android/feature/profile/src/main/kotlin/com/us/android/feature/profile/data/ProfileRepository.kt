package com.us.android.feature.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileStats
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.profile.data.dto.GraphUserIdRequest
import com.us.android.feature.profile.data.dto.OwnProfileDto
import com.us.android.feature.profile.data.dto.ProfileStatsDto
import com.us.android.feature.profile.data.dto.PublicProfileDto
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
