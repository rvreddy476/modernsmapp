package com.us.android.feature.echo.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.Profile
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.MuteSource
import com.us.android.core.profile.data.ProfileRepository
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The people half of Echo: profile headers, the drawer's own account, and
 * the card menu's Follow / Unfollow, Mute and Block. All existing
 * profile-service and graph-service routes; Echo adds none.
 */
interface EchoPeople {
    /** The signed-in viewer's id, or "" signed out. */
    val viewerId: String

    suspend fun profile(userId: String): AppResult<EchoProfile>
    suspend fun ownProfile(): AppResult<EchoProfile>

    /** Whether the viewer follows [userId] (the graph's own answer). */
    suspend fun follows(userId: String): AppResult<Boolean>
    suspend fun setFollowing(userId: String, follow: Boolean): AppResult<Unit>
    suspend fun mute(userId: String): AppResult<Unit>
    suspend fun block(userId: String): AppResult<Unit>
}

@Singleton
class DefaultEchoPeople @Inject constructor(
    private val profiles: ProfileRepository,
    private val mutes: MuteSource,
    private val session: SessionStateProvider,
) : EchoPeople {

    override val viewerId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    override suspend fun profile(userId: String): AppResult<EchoProfile> =
        profiles.getProfile(userId).map { it.toEchoProfile() }

    override suspend fun ownProfile(): AppResult<EchoProfile> = profiles.getOwnProfile().map { it.toEchoProfile() }

    override suspend fun follows(userId: String): AppResult<Boolean> =
        profiles.relationship(viewerId, userId).map { it.isFollowing }

    override suspend fun setFollowing(userId: String, follow: Boolean): AppResult<Unit> =
        if (follow) profiles.follow(userId).map { } else profiles.unfollow(userId)

    override suspend fun mute(userId: String): AppResult<Unit> = mutes.mute(userId)

    override suspend fun block(userId: String): AppResult<Unit> = profiles.block(userId)
}

fun Profile.toEchoProfile(): EchoProfile = EchoProfile(
    userId = userId,
    username = username,
    displayName = displayName,
    bio = bio,
    avatarMediaId = avatarMediaId?.takeIf { it.isNotBlank() },
    coverMediaId = coverMediaId?.takeIf { it.isNotBlank() },
    mark = VerificationMark.of(isVerified, verificationLevel),
    followers = counts.followers.toLong(),
    following = counts.following.toLong(),
    joinedAt = createdAt,
    viewerFollows = followStatus == FollowStatus.FOLLOWING,
)
