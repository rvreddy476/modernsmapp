package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.ConnectionStatus
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.AuthorPostCountsApi
import com.us.android.core.profile.data.AuthorPostCountsRepository
import com.us.android.core.profile.data.GraphCountsApi
import com.us.android.core.profile.data.GraphCountsRepository
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.FollowRequestDto
import com.us.android.core.profile.data.dto.GraphCountsDto
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileMediaUpdateDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.RelationshipDto
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class ProfileViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * A hand-written fake rather than a mocking framework: the assertions here
     * are about call ordering and rollback, and a fake that records calls
     * states that far more directly than a verify() chain.
     */
    private class FakeApi : ProfileApi {
        var publicProfile: ApiEnvelope<PublicProfileDto> = ApiEnvelope(PublicProfileDto(userId = "u"))
        var ownProfile: ApiEnvelope<OwnProfileDto> = ApiEnvelope(OwnProfileDto(userId = "me"))
        var stats: ApiEnvelope<ProfileStatsDto> = ApiEnvelope(ProfileStatsDto(followerCount = 7))
        var mutationResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("ok"))
        var followResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("followed"))
        var cancelResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("cancelled"))
        var incomingRequests: ApiEnvelope<List<FollowRequestDto>> = ApiEnvelope(emptyList())
        val calls = mutableListOf<String>()

        override suspend fun getProfile(userId: String) =
            publicProfile.also { calls += "getProfile($userId)" }

        override suspend fun getOwnProfile() = ownProfile.also { calls += "getOwnProfile" }

        override suspend fun getStats(userId: String) = stats.also { calls += "getStats($userId)" }

        // The read-only profile screen never writes. Present only to satisfy
        // the interface; EditProfileViewModelTest exercises the real thing.
        override suspend fun updateProfile(body: UpdateProfileRequest) =
            ownProfile.also { calls += "updateProfile" }

        override suspend fun updateAvatar(body: UpdateMediaIdRequest) =
            ApiEnvelope(ProfileMediaUpdateDto(avatarMediaId = body.mediaId))

        override suspend fun updateCover(body: UpdateMediaIdRequest) =
            ApiEnvelope(ProfileMediaUpdateDto(coverMediaId = body.mediaId))

        var relationship: ApiEnvelope<RelationshipDto> = ApiEnvelope(RelationshipDto())

        override suspend fun relationship(userId: String, otherId: String) =
            relationship.also { calls += "relationship($userId,$otherId)" }

        override suspend fun follow(body: GraphUserIdRequest) =
            followResult.also { calls += "follow(${body.userId})" }

        override suspend fun unfollow(body: GraphUserIdRequest) =
            mutationResult.also { calls += "unfollow(${body.userId})" }

        override suspend fun block(body: GraphUserIdRequest) =
            mutationResult.also { calls += "block(${body.userId})" }

        override suspend fun unblock(body: GraphUserIdRequest) =
            mutationResult.also { calls += "unblock(${body.userId})" }

        override suspend fun cancelFollowRequest(targetId: String) =
            cancelResult.also { calls += "cancelFollowRequest($targetId)" }

        override suspend fun incomingFollowRequests(limit: Int, cursor: String?) =
            incomingRequests.also { calls += "incomingFollowRequests" }

        override suspend fun acceptFollowRequest(requesterId: String): Nothing =
            error("not used")

        override suspend fun declineFollowRequest(requesterId: String): Nothing =
            error("not used")
    }

    /** A signed-in viewer; the relationship fetch requires a known viewer id. */
    private class FakeSessionProvider(
        state: SessionState = SessionState.Authenticated(userId = "viewer", sessionId = "s"),
    ) : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> = MutableStateFlow(state)
    }

    /**
     * graph-service's count route. Fails by default, so every older test
     * here still sees the profile-side counts it was written against.
     */
    private class FakeCountsApi : GraphCountsApi {
        var answers: ArrayDeque<ApiEnvelope<GraphCountsDto>> = ArrayDeque()
        val calls = mutableListOf<String>()

        override suspend fun counts(userId: String): ApiEnvelope<GraphCountsDto> {
            calls += userId
            return answers.removeFirstOrNull() ?: ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
    }

    /**
     * post-service's per-author counts. Fails by default, like the graph's,
     * so the older tests keep the profile-side post count they were written
     * against.
     */
    private class FakePostCountsApi : AuthorPostCountsApi {
        var answer: ApiEnvelope<Map<String, Long>> = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        val calls = mutableListOf<String>()

        override suspend fun counts(authorId: String): ApiEnvelope<Map<String, Long>> {
            calls += authorId
            return answer
        }
    }

    private fun viewModel(
        api: FakeApi,
        userId: String? = "other-user",
        counts: FakeCountsApi = FakeCountsApi(),
        postCounts: FakePostCountsApi = FakePostCountsApi(),
        connections: FakeConnections = FakeConnections(),
    ) = ProfileViewModel(
        repository = ProfileRepository(api, ErrorMapper(json)),
        graphCounts = GraphCountsRepository(counts, ErrorMapper(json)),
        postCounts = AuthorPostCountsRepository(postCounts, ErrorMapper(json)),
        connections = connections,
        sessionStateProvider = FakeSessionProvider(),
        savedStateHandle = SavedStateHandle(
            if (userId == null) emptyMap() else mapOf("userId" to userId),
        ),
    )

    @Test
    fun `an absent route id loads the signed-in user`() = runTest {
        val api = FakeApi()

        val state = viewModel(api, userId = null).state.value

        assertThat(api.calls).contains("getOwnProfile")
        assertThat(api.calls).doesNotContain("getProfile(null)")
        assertThat(state).isInstanceOf(ProfileUiState.Content::class.java)
    }

    @Test
    fun `a route id loads that user's public profile`() = runTest {
        val api = FakeApi()

        viewModel(api, userId = "other-user")

        assertThat(api.calls).contains("getProfile(other-user)")
        assertThat(api.calls).doesNotContain("getOwnProfile")
    }

    /**
     * The header must survive a stats failure. The profile payload already
     * carries follower/following/post counts, so losing the whole screen over
     * the secondary call would be a self-inflicted outage.
     */
    @Test
    fun `a stats failure still yields content, falling back to profile counts`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", followerCount = 3))
            stats = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val state = viewModel(api).state.value

        assertThat(state).isInstanceOf(ProfileUiState.Content::class.java)
        val content = state as ProfileUiState.Content
        assertThat(content.stats).isNull()
        assertThat(content.counts.followers).isEqualTo(3)
    }

    @Test
    fun `stats override the profile counts when both arrive`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", followerCount = 3))
            stats = ApiEnvelope(ProfileStatsDto(followerCount = 9))
        }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.counts.followers).isEqualTo(9)
    }

    @Test
    fun `a profile failure yields a retryable error`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val state = viewModel(api).state.value

        assertThat(state).isInstanceOf(ProfileUiState.Error::class.java)
        assertThat((state as ProfileUiState.Error).retryable).isTrue()
    }

    @Test
    fun `follow flips optimistically and settles on success`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)

        vm.onFollowToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.isFollowing).isTrue()
        assertThat(content.relationshipBusy).isFalse()
        assertThat(content.actionError).isNull()
        assertThat(api.calls).contains("follow(u)")
    }

    /**
     * The rollback is the point of the optimistic update. Without it the UI
     * claims a follow the server rejected, and the user only discovers it on
     * the next cold start.
     */
    @Test
    fun `a failed follow rolls back and reports`() = runTest {
        val api = FakeApi().apply {
            followResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)

        vm.onFollowToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.isFollowing).isFalse()
        assertThat(content.actionError).isNotNull()
        assertThat(content.relationshipBusy).isFalse()
    }

    @Test
    fun `toggling an existing follow calls unfollow`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFollowToggle() // now following

        vm.onFollowToggle()

        assertThat(api.calls).contains("unfollow(u)")
        assertThat((vm.state.value as ProfileUiState.Content).relationship.isFollowing).isFalse()
    }

    /**
     * The regression that motivated the fetch: the screen used to hardcode an
     * empty relationship, so Follow re-armed on every visit no matter what the
     * server knew. The header must render the graph's answer.
     */
    @Test
    fun `the loaded relationship comes from the graph, not a guess`() = runTest {
        val api = FakeApi().apply {
            relationship = ApiEnvelope(
                RelationshipDto(follows = true),
            )
        }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(api.calls).contains("relationship(viewer,u)")
        assertThat(content.relationship.isFollowing).isTrue()
    }

    /** The graph endpoint is best-effort: a blip must not cost the screen. */
    @Test
    fun `a relationship failure still yields content`() = runTest {
        val api = FakeApi().apply {
            relationship = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val state = viewModel(api).state.value

        assertThat(state).isInstanceOf(ProfileUiState.Content::class.java)
    }

    /** Blocking ends the follow relationship server-side; the UI must agree. */
    @Test
    fun `blocking clears the following state`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFollowToggle()

        vm.onBlockToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.isBlocked).isTrue()
        assertThat(content.relationship.isFollowing).isFalse()
    }

    @Test
    fun `unblocking clears the blocked state`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onBlockToggle()

        vm.onBlockToggle()

        assertThat(api.calls).contains("unblock(u)")
        assertThat((vm.state.value as ProfileUiState.Content).relationship.isBlocked).isFalse()
    }

    /** Relationship controls are never offered on your own profile. */
    @Test
    fun `relationship actions are ignored on the own profile`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api, userId = null)

        vm.onFollowToggle()
        vm.onBlockToggle()

        assertThat(api.calls).doesNotContain("follow(me)")
        assertThat(api.calls).doesNotContain("block(me)")
    }

    @Test
    fun `dismissing the action error clears it without touching the profile`() = runTest {
        val api = FakeApi().apply {
            followResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle()

        vm.dismissActionError()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.actionError).isNull()
        assertThat(content.profile.userId).isEqualTo("u")
    }

    // ── Private accounts ────────────────────────────────────────────────

    /** A private target answers `POST /v1/graph/follow` with "requested", not "followed". */
    @Test
    fun `following a private account settles on Requested rather than Following`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            followResult = ApiEnvelope(GraphStatusDto("requested"))
        }
        val vm = viewModel(api)

        vm.onFollowToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.followStatus).isEqualTo(FollowStatus.REQUESTED)
        assertThat(content.relationship.isFollowing).isFalse()
        assertThat(content.relationshipBusy).isFalse()
        assertThat(content.actionError).isNull()
    }

    /**
     * Tapping "Requested" is destructive enough to confirm first: it does not
     * cancel on the spot, it only arms the confirmation.
     */
    @Test
    fun `tapping a pending request arms the cancel confirmation rather than cancelling`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            followResult = ApiEnvelope(GraphStatusDto("requested"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle() // now requested

        vm.onFollowToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.showCancelRequestConfirm).isTrue()
        assertThat(content.relationship.followStatus).isEqualTo(FollowStatus.REQUESTED)
        assertThat(api.calls).doesNotContain("cancelFollowRequest(u)")
    }

    @Test
    fun `confirming cancels the pending request`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            followResult = ApiEnvelope(GraphStatusDto("requested"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle()
        vm.onFollowToggle() // arms the confirmation

        vm.onConfirmCancelRequest()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.followStatus).isEqualTo(FollowStatus.NONE)
        assertThat(content.showCancelRequestConfirm).isFalse()
        assertThat(content.relationshipBusy).isFalse()
        assertThat(api.calls).contains("cancelFollowRequest(u)")
    }

    /** The rollback the optimistic cancel exists for. */
    @Test
    fun `a failed cancel rolls back to Requested and reports`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            followResult = ApiEnvelope(GraphStatusDto("requested"))
            cancelResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle()
        vm.onFollowToggle()

        vm.onConfirmCancelRequest()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.relationship.followStatus).isEqualTo(FollowStatus.REQUESTED)
        assertThat(content.actionError).isNotNull()
        assertThat(content.relationshipBusy).isFalse()
    }

    @Test
    fun `dismissing the cancel confirmation leaves the request pending and sends nothing`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            followResult = ApiEnvelope(GraphStatusDto("requested"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle()
        vm.onFollowToggle()

        vm.onDismissCancelRequestConfirm()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.showCancelRequestConfirm).isFalse()
        assertThat(content.relationship.followStatus).isEqualTo(FollowStatus.REQUESTED)
        assertThat(api.calls).doesNotContain("cancelFollowRequest(u)")
    }

    /**
     * When the graph relationship call itself fails, the profile payload's own
     * `is_private` still reaches the screen rather than resetting to "unknown".
     */
    @Test
    fun `a relationship failure still carries is_private from the profile payload`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", isPrivate = true))
            relationship = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.relationship.isPrivate).isTrue()
    }

    @Test
    fun `the own profile loads the first page count of incoming follow requests`() = runTest {
        val api = FakeApi().apply {
            incomingRequests = ApiEnvelope(
                listOf(FollowRequestDto(requesterId = "a"), FollowRequestDto(requesterId = "b")),
            )
        }

        val content = viewModel(api, userId = null).state.value as ProfileUiState.Content

        assertThat(content.incomingFollowRequestCount).isEqualTo(2)
    }

    /** Asking "who wants to follow someone else" is not a thing the viewer can do. */
    @Test
    fun `another user's profile never fetches incoming follow requests`() = runTest {
        val api = FakeApi()

        viewModel(api, userId = "other-user")

        assertThat(api.calls).doesNotContain("incomingFollowRequests")
    }

    // ── Live counts (T-20, audit R-1) ────────────────────────────────────

    private fun graph(followers: Long, following: Long, friends: Long = 0) =
        ApiEnvelope(GraphCountsDto(followerCount = followers, followingCount = following, friendCount = friends))

    /** The payload's post_count is never written (always 0); post-service's total replaces it. */
    @Test
    fun `post-service's post total replaces the profile-side post count`() = runTest {
        val api = FakeApi().apply {
            stats = ApiEnvelope(ProfileStatsDto(followerCount = 9, followingCount = 4, postCount = 0))
        }
        val posts = FakePostCountsApi().apply {
            answer = ApiEnvelope(mapOf("post" to 5L, "long_video" to 2L, "total" to 7L))
        }

        val content = viewModel(api, postCounts = posts).state.value as ProfileUiState.Content

        assertThat(posts.calls).containsExactly("other-user")
        assertThat(content.counts.posts).isEqualTo(7)
        assertThat(content.counts.followers).isEqualTo(9)
        assertThat(content.counts.following).isEqualTo(4)
    }

    @Test
    fun `the owner's post total is read for the id the own profile names`() = runTest {
        val posts = FakePostCountsApi().apply { answer = ApiEnvelope(mapOf("total" to 3L)) }

        val content = viewModel(FakeApi(), userId = null, postCounts = posts).state.value as ProfileUiState.Content

        assertThat(posts.calls).containsExactly("me")
        assertThat(content.counts.posts).isEqualTo(3)
    }

    @Test
    fun `a post counts failure keeps the profile-side post count`() = runTest {
        val api = FakeApi().apply { stats = ApiEnvelope(ProfileStatsDto(followerCount = 9, postCount = 6)) }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.postTotal).isNull()
        assertThat(content.counts.posts).isEqualTo(6)
    }

    /** profile_stats stopped moving when follows moved to the graph; the graph's numbers win. */
    @Test
    fun `the graph's follow counts replace the frozen profile-side ones and keep the post count`() = runTest {
        val api = FakeApi().apply {
            publicProfile = ApiEnvelope(PublicProfileDto(userId = "u", followerCount = 3, postCount = 5))
            stats = ApiEnvelope(ProfileStatsDto(followerCount = 9, followingCount = 9, postCount = 6))
        }
        val counts = FakeCountsApi().apply { answers.add(graph(followers = 40, following = 12, friends = 2)) }

        val content = viewModel(api, counts = counts).state.value as ProfileUiState.Content

        assertThat(counts.calls).containsExactly("other-user")
        assertThat(content.counts.followers).isEqualTo(40)
        assertThat(content.counts.following).isEqualTo(12)
        assertThat(content.counts.friends).isEqualTo(2)
        assertThat(content.counts.posts).isEqualTo(6)
    }

    @Test
    fun `the owner's counts are read for the id the own profile names`() = runTest {
        val api = FakeApi()
        val counts = FakeCountsApi().apply { answers.add(graph(followers = 1, following = 2)) }

        val content = viewModel(api, userId = null, counts = counts).state.value as ProfileUiState.Content

        assertThat(counts.calls).containsExactly("me")
        assertThat(content.counts.following).isEqualTo(2)
    }

    @Test
    fun `a graph counts failure falls back to the profile-side counts`() = runTest {
        val api = FakeApi().apply { stats = ApiEnvelope(ProfileStatsDto(followerCount = 9)) }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.graphCounts).isNull()
        assertThat(content.counts.followers).isEqualTo(9)
    }

    @Test
    fun `a follow re-reads the counts so the header moves with it`() = runTest {
        val api = FakeApi()
        val counts = FakeCountsApi().apply {
            answers.add(graph(followers = 10, following = 0))
            answers.add(graph(followers = 11, following = 0))
        }
        val vm = viewModel(api, counts = counts)

        vm.onFollowToggle()

        assertThat(counts.calls).containsExactly("other-user", "u").inOrder()
        assertThat((vm.state.value as ProfileUiState.Content).counts.followers).isEqualTo(11)
    }

    @Test
    fun `an unfollow re-reads the counts too`() = runTest {
        val api = FakeApi().apply { relationship = ApiEnvelope(RelationshipDto(follows = true)) }
        val counts = FakeCountsApi().apply {
            answers.add(graph(followers = 11, following = 0))
            answers.add(graph(followers = 10, following = 0))
        }
        val vm = viewModel(api, counts = counts)

        vm.onFollowToggle()

        assertThat(api.calls).contains("unfollow(u)")
        assertThat((vm.state.value as ProfileUiState.Content).counts.followers).isEqualTo(10)
    }

    @Test
    fun `a failed follow does not re-read the counts`() = runTest {
        val api = FakeApi().apply { followResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR")) }
        val counts = FakeCountsApi().apply { answers.add(graph(followers = 10, following = 0)) }
        val vm = viewModel(api, counts = counts)

        vm.onFollowToggle()

        assertThat(counts.calls).containsExactly("other-user")
        assertThat((vm.state.value as ProfileUiState.Content).counts.followers).isEqualTo(10)
    }

    @Test
    fun `a failed re-read keeps the counts the header already had`() = runTest {
        val api = FakeApi()
        val counts = FakeCountsApi().apply { answers.add(graph(followers = 10, following = 0)) }
        val vm = viewModel(api, counts = counts)

        vm.onFollowToggle()

        assertThat(counts.calls).hasSize(2)
        assertThat((vm.state.value as ProfileUiState.Content).counts.followers).isEqualTo(10)
    }

    // ── Block state (graph viewer_blocked_target / target_blocked_viewer) ──

    @Test
    fun `a profile that blocked the viewer is unavailable and refuses a follow`() = runTest {
        val api = FakeApi().apply {
            relationship = ApiEnvelope(RelationshipDto(targetBlockedViewer = true, viewerBlockedTarget = false))
        }
        val vm = viewModel(api)

        vm.onFollowToggle()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.unavailable).isTrue()
        assertThat(content.relationship.isBlocked).isFalse()
        assertThat(api.calls.none { it.startsWith("follow(") }).isTrue()
    }

    /** An older graph without the named fields: on this route `blocked` is the target's block. */
    @Test
    fun `the legacy key still marks the profile unavailable`() = runTest {
        val api = FakeApi().apply { relationship = ApiEnvelope(RelationshipDto(blocked = true)) }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.unavailable).isTrue()
    }

    @Test
    fun `the viewer's own block is not the unavailable state`() = runTest {
        val api = FakeApi().apply { relationship = ApiEnvelope(RelationshipDto(viewerBlockedTarget = true)) }

        val content = viewModel(api).state.value as ProfileUiState.Content

        assertThat(content.unavailable).isFalse()
        assertThat(content.relationship.isBlocked).isTrue()
    }

    // ── Connections and mute (2026-10-24) ────────────────────────────────

    private fun FakeApi.answering(connection: String, muted: Boolean = false) = apply {
        relationship = ApiEnvelope(RelationshipDto(connectionStatus = connection, isMuted = muted))
    }

    private fun content(vm: ProfileViewModel) = vm.state.value as ProfileUiState.Content

    @Test
    fun `a request to the viewer is accepted with the sender's id and settles on connected`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("pending_received"), connections = connections)

        vm.onAcceptConnection()

        assertThat(connections.calls).containsExactly("accept(u)")
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.CONNECTED)
        assertThat(content(vm).connectionBusy).isFalse()
    }

    @Test
    fun `declining a request settles on no connection`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("pending_received"), connections = connections)

        vm.onDeclineConnection()

        assertThat(connections.calls).containsExactly("decline(u)")
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.NONE)
    }

    @Test
    fun `a refused accept keeps the request and says why`() = runTest {
        val connections = FakeConnections().apply { writeResult = FakeConnections.FAILURE }
        val vm = viewModel(FakeApi().answering("pending_received"), connections = connections)

        vm.onAcceptConnection()

        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.PENDING_RECEIVED)
        assertThat(content(vm).actionError).isNotNull()
        assertThat(content(vm).connectionBusy).isFalse()
    }

    @Test
    fun `accept does nothing when no request is pending`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("none"), connections = connections)

        vm.onAcceptConnection()
        vm.onDeclineConnection()

        assertThat(connections.calls).isEmpty()
    }

    @Test
    fun `tapping a sent request withdraws it`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("pending_sent"), connections = connections)

        vm.onConnectionTap()

        assertThat(connections.calls).containsExactly("cancel(u)")
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.NONE)
    }

    @Test
    fun `tapping Connected asks first, and only the confirmation removes it`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("accepted"), connections = connections)

        vm.onConnectionTap()

        assertThat(connections.calls).isEmpty()
        assertThat(content(vm).showRemoveConnectionConfirm).isTrue()

        vm.onConfirmRemoveConnection()

        assertThat(connections.calls).containsExactly("remove(u)")
        assertThat(content(vm).showRemoveConnectionConfirm).isFalse()
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.NONE)
    }

    @Test
    fun `dismissing the remove confirmation keeps the connection`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("accepted"), connections = connections)

        vm.onConnectionTap()
        vm.onDismissRemoveConnection()

        assertThat(connections.calls).isEmpty()
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.CONNECTED)
    }

    @Test
    fun `with no connection, a tap sends nothing while Connect is not offered`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("none"), connections = connections)

        vm.onConnectionTap()

        assertThat(connections.calls).isEmpty()
    }

    @Test
    fun `with Connect offered, a tap sends the request and settles on sent`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("none"), connections = connections)
        vm.connectOffered = true

        vm.onConnectionTap()

        assertThat(connections.calls).containsExactly("send(u)")
        assertThat(content(vm).relationship.connectionStatus).isEqualTo(ConnectionStatus.PENDING_SENT)
    }

    @Test
    fun `mute names the account, flips to muted and says so`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("none"), connections = connections)

        vm.onMuteToggle()

        assertThat(connections.calls).containsExactly("mute(u)")
        assertThat(content(vm).relationship.isMuted).isTrue()
        assertThat(content(vm).notice).startsWith("Muted")
    }

    @Test
    fun `a muted account is unmuted`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi().answering("none", muted = true), connections = connections)

        vm.onMuteToggle()

        assertThat(connections.calls).containsExactly("unmute(u)")
        assertThat(content(vm).relationship.isMuted).isFalse()
    }

    @Test
    fun `a refused mute leaves the account unmuted`() = runTest {
        val connections = FakeConnections().apply { writeResult = FakeConnections.FAILURE }
        val vm = viewModel(FakeApi().answering("none"), connections = connections)

        vm.onMuteToggle()

        assertThat(content(vm).relationship.isMuted).isFalse()
        assertThat(content(vm).actionError).isNotNull()
        assertThat(content(vm).notice).isNull()
    }

    @Test
    fun `the viewer's own profile never mutes or connects`() = runTest {
        val connections = FakeConnections()
        val vm = viewModel(FakeApi(), userId = null, connections = connections)

        vm.onMuteToggle()
        vm.onConnectionTap()

        assertThat(connections.calls).isEmpty()
    }
}
