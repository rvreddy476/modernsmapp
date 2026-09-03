package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.FollowRequestDto
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.RelationshipDto
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * The private-account owner's approval queue.
 *
 * Accept/Decline are not optimistic — the row waits for the server, same rule
 * the notification inbox's message-request row follows — so these tests are
 * about the settled outcome (the row leaves the list) and the failure path
 * (the row stays, and says so).
 */
class FollowRequestsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : ProfileApi {
        var page: ApiEnvelope<List<FollowRequestDto>> = ApiEnvelope(emptyList())
        var actionResult: ApiEnvelope<GraphStatusDto> = ApiEnvelope(GraphStatusDto("ok"))
        val calls = mutableListOf<String>()

        override suspend fun getProfile(userId: String) =
            ApiEnvelope(PublicProfileDto(userId = userId, displayName = "User $userId"))
                .also { calls += "getProfile($userId)" }

        override suspend fun getOwnProfile(): Nothing = error("not used")
        override suspend fun updateProfile(body: UpdateProfileRequest): Nothing = error("not used")
        override suspend fun updateAvatar(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun updateCover(body: UpdateMediaIdRequest): Nothing = error("not used")
        override suspend fun getStats(userId: String): Nothing = error("not used")

        override suspend fun relationship(userId: String, otherId: String): ApiEnvelope<RelationshipDto> =
            ApiEnvelope(RelationshipDto())

        override suspend fun follow(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun unfollow(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun block(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun unblock(body: GraphUserIdRequest): Nothing = error("not used")
        override suspend fun cancelFollowRequest(targetId: String): Nothing = error("not used")

        override suspend fun incomingFollowRequests(limit: Int, cursor: String?) =
            page.also { calls += "incomingFollowRequests(cursor=$cursor)" }

        override suspend fun acceptFollowRequest(requesterId: String) =
            actionResult.also { calls += "accept($requesterId)" }

        override suspend fun declineFollowRequest(requesterId: String) =
            actionResult.also { calls += "decline($requesterId)" }
    }

    private fun viewModel(api: FakeApi) = FollowRequestsViewModel(ProfileRepository(api, ErrorMapper(json)))

    @Test
    fun `the first page loads and rows resolve their names`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
        }

        val vm = viewModel(api)

        val row = vm.state.value.rows.single()
        assertThat(row.requesterId).isEqualTo("r1")
        assertThat(row.displayName).isEqualTo("User r1")
        assertThat(vm.state.value.loading).isFalse()
    }

    @Test
    fun `an empty page is empty rather than an error`() = runTest {
        val vm = viewModel(FakeApi())

        assertThat(vm.state.value.isEmpty).isTrue()
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a failed first load surfaces an error`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val vm = viewModel(api)

        assertThat(vm.state.value.error).isNotNull()
    }

    /** Accept is not optimistic: the row is gone only once the server confirms it. */
    @Test
    fun `accepting removes the row on success`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
        }
        val vm = viewModel(api)

        vm.accept("r1")

        assertThat(vm.state.value.rows).isEmpty()
        assertThat(api.calls).contains("accept(r1)")
    }

    @Test
    fun `declining removes the row on success`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
        }
        val vm = viewModel(api)

        vm.decline("r1")

        assertThat(vm.state.value.rows).isEmpty()
        assertThat(api.calls).contains("decline(r1)")
    }

    /**
     * The regression a non-optimistic row exists to prevent the OPPOSITE of:
     * a failure must leave the row in place, marked so it can be retried, not
     * silently drop the request the user still has not decided on.
     */
    @Test
    fun `a failed accept keeps the row and marks it failed`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
            actionResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)

        vm.accept("r1")

        val row = vm.state.value.rows.single()
        assertThat(row.requesterId).isEqualTo("r1")
        assertThat(row.actionFailed).isTrue()
        assertThat(row.busy).isFalse()
    }

    @Test
    fun `a failed decline keeps the row and marks it failed`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
            actionResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)

        vm.decline("r1")

        val row = vm.state.value.rows.single()
        assertThat(row.actionFailed).isTrue()
    }

    @Test
    fun `load-more appends and de-duplicates by requester id`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(
                data = listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")),
                meta = ApiMeta(nextCursor = "c1"),
            )
        }
        val vm = viewModel(api)
        api.page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r2", createdAt = "t2")))

        vm.loadMore()

        assertThat(vm.state.value.rows.map { it.requesterId }).containsExactly("r1", "r2").inOrder()
    }

    @Test
    fun `load-more with no cursor issues no request`() = runTest {
        val api = FakeApi().apply {
            page = ApiEnvelope(listOf(FollowRequestDto(requesterId = "r1", createdAt = "t1")))
        }
        val vm = viewModel(api)
        val callsAfterLoad = api.calls.size

        vm.loadMore()

        assertThat(api.calls).hasSize(callsAfterLoad)
    }
}
