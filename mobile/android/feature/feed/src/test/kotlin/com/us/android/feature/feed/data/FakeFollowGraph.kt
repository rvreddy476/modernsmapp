package com.us.android.feature.feed.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.FollowRequestDto
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileMediaUpdateDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.RelationshipDto
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.Json

/**
 * The graph as a feed test sees it: who the viewer is, what
 * `GET /v1/graph/relationship` answers per author, and a record of every
 * follow the feed sent. Only the graph routes are implemented; the profile
 * routes fail loudly, because a feed test that reaches them is testing the
 * wrong thing.
 */
internal class RecordingGraphApi(
    /** Author id → `follows`. Absent authors answer "not following". */
    val follows: MutableMap<String, Boolean> = mutableMapOf(),
    /** What `POST /v1/graph/follow` answers: `followed`, or `requested` for a private account. */
    var followAnswer: String = "followed",
    /** Throw on follow, so the optimistic edge must be put back. */
    var followFails: Boolean = false,
) : ProfileApi {
    val relationshipRequests = mutableListOf<Pair<String, String>>()
    val followRequests = mutableListOf<String>()
    val unfollowRequests = mutableListOf<String>()
    val blockRequests = mutableListOf<String>()

    /** Throw on block, so the optimistic removal must be undone. */
    var blockFails: Boolean = false

    override suspend fun relationship(userId: String, otherId: String): ApiEnvelope<RelationshipDto> {
        relationshipRequests += userId to otherId
        return ApiEnvelope(data = RelationshipDto(follows = follows[otherId] ?: false), meta = null)
    }

    override suspend fun follow(body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto> {
        followRequests += body.userId
        if (followFails) throw java.io.IOException("offline")
        return ApiEnvelope(data = GraphStatusDto(followAnswer), meta = null)
    }

    override suspend fun unfollow(body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto> {
        unfollowRequests += body.userId
        return ApiEnvelope(data = GraphStatusDto("unfollowed"), meta = null)
    }

    override suspend fun getProfile(userId: String): ApiEnvelope<PublicProfileDto> = error("unused")
    override suspend fun getOwnProfile(): ApiEnvelope<OwnProfileDto> = error("unused")
    override suspend fun updateProfile(body: UpdateProfileRequest): ApiEnvelope<OwnProfileDto> = error("unused")
    override suspend fun updateAvatar(body: UpdateMediaIdRequest): ApiEnvelope<ProfileMediaUpdateDto> =
        error("unused")

    override suspend fun updateCover(body: UpdateMediaIdRequest): ApiEnvelope<ProfileMediaUpdateDto> =
        error("unused")

    override suspend fun getStats(userId: String): ApiEnvelope<ProfileStatsDto> = error("unused")
    override suspend fun block(body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto> {
        blockRequests += body.userId
        if (blockFails) throw java.io.IOException("offline")
        return ApiEnvelope(data = GraphStatusDto("blocked"), meta = null)
    }
    override suspend fun unblock(body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto> = error("unused")
    override suspend fun cancelFollowRequest(targetId: String): ApiEnvelope<GraphStatusDto> = error("unused")
    override suspend fun incomingFollowRequests(limit: Int, cursor: String?): ApiEnvelope<List<FollowRequestDto>> =
        error("unused")

    override suspend fun acceptFollowRequest(requesterId: String): ApiEnvelope<GraphStatusDto> = error("unused")
    override suspend fun declineFollowRequest(requesterId: String): ApiEnvelope<GraphStatusDto> = error("unused")
}

/** A signed-in viewer with a fixed id, or nobody when [userId] is null. */
internal class FakeSession(userId: String? = "me") : SessionStateProvider {
    override val sessionState: StateFlow<SessionState> = MutableStateFlow(
        if (userId == null) SessionState.Unauthenticated else SessionState.Authenticated(userId, "session"),
    )
}

/** A [FollowGraph] over the recording api, for the ViewModel tests that need one. */
internal fun followGraph(
    api: RecordingGraphApi = RecordingGraphApi(),
    session: SessionStateProvider = FakeSession(),
): FollowGraph = FollowGraph(
    profiles = ProfileRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
    session = session,
)
