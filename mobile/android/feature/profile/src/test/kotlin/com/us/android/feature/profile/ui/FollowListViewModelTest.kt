package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.BatchRelationshipDto
import com.us.android.core.profile.data.FollowListApi
import com.us.android.core.profile.data.FollowListRepository
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileBatchRequest
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.ProfileSummaryDto
import com.us.android.core.profile.data.RelationshipBatchRequest
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Rule
import org.junit.Test

class FollowListViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** The graph lists and the two batches, answered from per-list page queues. */
    private class FakeListApi : FollowListApi {
        val pages = mutableMapOf<String, ArrayDeque<ApiEnvelope<JsonElement>>>()
        val listCalls = mutableListOf<String>()
        var edges: Map<String, BatchRelationshipDto?> = emptyMap()

        fun enqueue(list: String, ids: List<String>, next: String = "") {
            pages.getOrPut(list) { ArrayDeque() } += ApiEnvelope(
                JsonObject(
                    mapOf(
                        "items" to JsonArray(ids.map(::JsonPrimitive)),
                        "next_cursor" to JsonPrimitive(next),
                    ),
                ),
            )
        }

        fun enqueueFailure(list: String) {
            pages.getOrPut(list) { ArrayDeque() } += ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        private fun answer(list: String, cursor: String?): ApiEnvelope<JsonElement> {
            listCalls += "$list:${cursor ?: "first"}"
            return pages[list]?.removeFirstOrNull() ?: ApiEnvelope(JsonArray(emptyList()))
        }

        override suspend fun followers(userId: String, limit: Int, cursor: String?, paginate: String?) =
            answer("followers", cursor)

        override suspend fun following(userId: String, limit: Int, cursor: String?, paginate: String?) =
            answer("following", cursor)

        override suspend fun profiles(body: ProfileBatchRequest): Map<String, ProfileSummaryDto?> =
            body.userIds.associateWith { ProfileSummaryDto(userId = it, username = "h_$it", displayName = "N $it") }

        override suspend fun relationships(body: RelationshipBatchRequest): Map<String, BatchRelationshipDto?> = edges
    }

    private class FakeProfileApi : ProfileApi {
        var followResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("followed"))
        var unfollowResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("unfollowed"))
        var cancelResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("cancelled"))
        val calls = mutableListOf<String>()

        override suspend fun follow(body: GraphUserIdRequest) = followResult.also { calls += "follow(${body.userId})" }
        override suspend fun unfollow(body: GraphUserIdRequest) =
            unfollowResult.also { calls += "unfollow(${body.userId})" }
        override suspend fun cancelFollowRequest(targetId: String) =
            cancelResult.also { calls += "cancel($targetId)" }

        override suspend fun getProfile(userId: String): Nothing = error("not used")
        override suspend fun getOwnProfile(): Nothing = error("not used")
        override suspend fun updateProfile(body: UpdateProfileRequest): Nothing = error("not used")
        override suspend fun updateAvatar(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun updateCover(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun getStats(userId: String): Nothing = error("not used")
        override suspend fun relationship(userId: String, otherId: String): Nothing = error("not used")
        override suspend fun block(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun unblock(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun incomingFollowRequests(limit: Int, cursor: String?): Nothing = error("not used")
        override suspend fun acceptFollowRequest(requesterId: String): Nothing = error("not used")
        override suspend fun declineFollowRequest(requesterId: String): Nothing = error("not used")
    }

    private class FakeSession(viewer: String = "me") : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> =
            MutableStateFlow(SessionState.Authenticated(userId = viewer, sessionId = "s"))
    }

    private val listApi = FakeListApi()
    private val profileApi = FakeProfileApi()

    private fun viewModel(userId: String = "me", tab: FollowListTab = FollowListTab.FOLLOWING) = FollowListViewModel(
        lists = FollowListRepository(listApi, ErrorMapper(json)),
        profiles = ProfileRepository(profileApi, ErrorMapper(json)),
        session = FakeSession(),
        savedStateHandle = SavedStateHandle(mapOf("userId" to userId, "handle" to "self", "tab" to tab.wire)),
    )

    private fun FollowListViewModel.row(tab: FollowListTab, id: String) =
        state.value.page(tab).rows.single { it.userId == id }

    @Test
    fun `opens on the tapped tab and loads only that tab`() = runTest {
        listApi.enqueue("following", listOf("a", "b"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = true), "b" to BatchRelationshipDto(follows = true))

        val vm = viewModel(tab = FollowListTab.FOLLOWING)

        val state = vm.state.value
        assertThat(state.selected).isEqualTo(FollowListTab.FOLLOWING)
        assertThat(state.handle).isEqualTo("self")
        assertThat(state.isOwnList).isTrue()
        assertThat(state.following.rows.map { it.userId }).containsExactly("a", "b").inOrder()
        assertThat(state.following.rows.first().displayName).isEqualTo("N a")
        assertThat(state.followers.loaded).isFalse()
        assertThat(listApi.listCalls).containsExactly("following:first")
    }

    @Test
    fun `switching tabs loads the other tab once`() = runTest {
        listApi.enqueue("following", listOf("a"))
        listApi.enqueue("followers", listOf("c"))
        val vm = viewModel()

        vm.select(FollowListTab.FOLLOWERS)
        vm.select(FollowListTab.FOLLOWING)
        vm.select(FollowListTab.FOLLOWERS)

        assertThat(vm.state.value.followers.rows.map { it.userId }).containsExactly("c")
        assertThat(listApi.listCalls).containsExactly("following:first", "followers:first")
    }

    @Test
    fun `load more appends the next page with its cursor and stops at the last`() = runTest {
        listApi.enqueue("following", listOf("a", "b"), next = "c2")
        listApi.enqueue("following", listOf("b", "c"))
        val vm = viewModel()

        vm.loadMore()
        vm.loadMore()

        val page = vm.state.value.following
        assertThat(page.rows.map { it.userId }).containsExactly("a", "b", "c").inOrder()
        assertThat(page.hasMore).isFalse()
        assertThat(listApi.listCalls).containsExactly("following:first", "following:c2")
    }

    @Test
    fun `a failed next page keeps the rows and can be tried again`() = runTest {
        listApi.enqueue("following", listOf("a"), next = "c2")
        listApi.enqueueFailure("following")
        listApi.enqueue("following", listOf("b"))
        val vm = viewModel()

        vm.loadMore()
        assertThat(vm.state.value.following.loadMoreFailed).isTrue()
        assertThat(vm.state.value.following.rows.map { it.userId }).containsExactly("a")

        vm.loadMore()
        assertThat(vm.state.value.following.rows.map { it.userId }).containsExactly("a", "b").inOrder()
        assertThat(vm.state.value.following.loadMoreFailed).isFalse()
    }

    @Test
    fun `a failed first page shows the error and retry loads it`() = runTest {
        listApi.enqueueFailure("following")
        listApi.enqueue("following", listOf("a"))
        val vm = viewModel()

        assertThat(vm.state.value.following.error).isEqualTo(FollowListViewModel.MESSAGE_LOAD_FAILED)

        vm.retry()
        assertThat(vm.state.value.following.error).isNull()
        assertThat(vm.state.value.following.rows.map { it.userId }).containsExactly("a")
    }

    @Test
    fun `an empty list reads as empty, not loading`() = runTest {
        listApi.enqueue("following", emptyList())
        val vm = viewModel()

        assertThat(vm.state.value.following.isEmpty).isTrue()
        assertThat(vm.state.value.following.loading).isFalse()
    }

    @Test
    fun `tapping Following on the own Following tab asks first, then unfollows`() = runTest {
        listApi.enqueue("following", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = true))
        val vm = viewModel()

        vm.onPillTap("a")
        assertThat(vm.state.value.confirm).isEqualTo(FollowListConfirm.Unfollow(vm.row(FollowListTab.FOLLOWING, "a")))
        assertThat(profileApi.calls).isEmpty()

        vm.onConfirm()
        assertThat(profileApi.calls).containsExactly("unfollow(a)")
        assertThat(vm.state.value.confirm).isNull()
        assertThat(vm.row(FollowListTab.FOLLOWING, "a").viewerEdge).isEqualTo(FollowStatus.NONE)
        assertThat(vm.state.value.pillFor(FollowListTab.FOLLOWING, vm.row(FollowListTab.FOLLOWING, "a")))
            .isEqualTo(FollowPill.FOLLOW)
        assertThat(vm.state.value.busyIds).isEmpty()
    }

    @Test
    fun `cancelling the confirmation unfollows nobody`() = runTest {
        listApi.enqueue("following", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = true))
        val vm = viewModel()

        vm.onPillTap("a")
        vm.onDismissConfirm()

        assertThat(vm.state.value.confirm).isNull()
        assertThat(profileApi.calls).isEmpty()
        assertThat(vm.row(FollowListTab.FOLLOWING, "a").viewerEdge).isEqualTo(FollowStatus.FOLLOWING)
    }

    @Test
    fun `a refused unfollow puts Following back and says so`() = runTest {
        listApi.enqueue("following", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = true))
        profileApi.unfollowResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        val vm = viewModel()

        vm.onPillTap("a")
        vm.onConfirm()

        assertThat(vm.row(FollowListTab.FOLLOWING, "a").viewerEdge).isEqualTo(FollowStatus.FOLLOWING)
        assertThat(vm.state.value.actionError).isNotNull()
        assertThat(vm.state.value.busyIds).isEmpty()
    }

    @Test
    fun `Follow back on the own Followers tab follows at once and shows Following`() = runTest {
        listApi.enqueue("followers", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false))
        val vm = viewModel(tab = FollowListTab.FOLLOWERS)

        val before = vm.row(FollowListTab.FOLLOWERS, "a")
        assertThat(vm.state.value.pillFor(FollowListTab.FOLLOWERS, before)).isEqualTo(FollowPill.FOLLOW_BACK)

        vm.onPillTap("a")

        assertThat(profileApi.calls).containsExactly("follow(a)")
        assertThat(vm.state.value.confirm).isNull()
        assertThat(vm.row(FollowListTab.FOLLOWERS, "a").viewerEdge).isEqualTo(FollowStatus.FOLLOWING)
    }

    @Test
    fun `a private account's answer settles the pill on Requested`() = runTest {
        listApi.enqueue("followers", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false))
        profileApi.followResult = ApiEnvelope(GraphStatusDto("requested"))
        val vm = viewModel(tab = FollowListTab.FOLLOWERS)

        vm.onPillTap("a")

        assertThat(vm.row(FollowListTab.FOLLOWERS, "a").viewerEdge).isEqualTo(FollowStatus.REQUESTED)
    }

    @Test
    fun `a refused follow rolls back to Follow back`() = runTest {
        listApi.enqueue("followers", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false))
        profileApi.followResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        val vm = viewModel(tab = FollowListTab.FOLLOWERS)

        vm.onPillTap("a")

        val row = vm.row(FollowListTab.FOLLOWERS, "a")
        assertThat(row.viewerEdge).isEqualTo(FollowStatus.NONE)
        assertThat(vm.state.value.pillFor(FollowListTab.FOLLOWERS, row)).isEqualTo(FollowPill.FOLLOW_BACK)
        assertThat(vm.state.value.actionError).isNotNull()
    }

    @Test
    fun `a follow moves the same person's row on the other tab too`() = runTest {
        listApi.enqueue("followers", listOf("a"))
        listApi.enqueue("following", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false))
        val vm = viewModel(tab = FollowListTab.FOLLOWERS)
        vm.select(FollowListTab.FOLLOWING)
        vm.select(FollowListTab.FOLLOWERS)

        vm.onPillTap("a")

        assertThat(vm.row(FollowListTab.FOLLOWING, "a").viewerEdge).isEqualTo(FollowStatus.FOLLOWING)
    }

    @Test
    fun `tapping Requested asks, then cancels the request`() = runTest {
        listApi.enqueue("following", listOf("a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false, followRequestStatus = "pending_sent"))
        val vm = viewModel(userId = "someone-else")

        vm.onPillTap("a")
        assertThat(vm.state.value.confirm).isInstanceOf(FollowListConfirm.CancelRequest::class.java)

        vm.onConfirm()
        assertThat(profileApi.calls).containsExactly("cancel(a)")
        assertThat(vm.row(FollowListTab.FOLLOWING, "a").viewerEdge).isEqualTo(FollowStatus.NONE)
    }

    @Test
    fun `someone else's list is not the viewer's own, and the viewer's row has no pill`() = runTest {
        listApi.enqueue("followers", listOf("me", "a"))
        listApi.edges = mapOf("a" to BatchRelationshipDto(follows = false))
        val vm = viewModel(userId = "someone-else", tab = FollowListTab.FOLLOWERS)

        val state = vm.state.value
        assertThat(state.isOwnList).isFalse()
        assertThat(state.pillFor(FollowListTab.FOLLOWERS, vm.row(FollowListTab.FOLLOWERS, "me"))).isNull()
        assertThat(state.pillFor(FollowListTab.FOLLOWERS, vm.row(FollowListTab.FOLLOWERS, "a")))
            .isEqualTo(FollowPill.FOLLOW)

        vm.onPillTap("me")
        assertThat(profileApi.calls).isEmpty()
    }
}
