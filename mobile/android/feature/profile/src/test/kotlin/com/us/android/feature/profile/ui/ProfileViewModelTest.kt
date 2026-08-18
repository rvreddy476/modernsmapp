package com.us.android.feature.profile.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
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
        val calls = mutableListOf<String>()

        override suspend fun getProfile(userId: String) =
            publicProfile.also { calls += "getProfile($userId)" }

        override suspend fun getOwnProfile() = ownProfile.also { calls += "getOwnProfile" }

        override suspend fun getStats(userId: String) = stats.also { calls += "getStats($userId)" }

        // The read-only profile screen never writes. Present only to satisfy
        // the interface; EditProfileViewModelTest exercises the real thing.
        override suspend fun updateProfile(body: UpdateProfileRequest) =
            ownProfile.also { calls += "updateProfile" }

        override suspend fun follow(body: GraphUserIdRequest) =
            mutationResult.also { calls += "follow(${body.userId})" }

        override suspend fun unfollow(body: GraphUserIdRequest) =
            mutationResult.also { calls += "unfollow(${body.userId})" }

        override suspend fun block(body: GraphUserIdRequest) =
            mutationResult.also { calls += "block(${body.userId})" }

        override suspend fun unblock(body: GraphUserIdRequest) =
            mutationResult.also { calls += "unblock(${body.userId})" }
    }

    private fun viewModel(api: FakeApi, userId: String? = "other-user") = ProfileViewModel(
        repository = ProfileRepository(api, ErrorMapper(json)),
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
            mutationResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
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
            mutationResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFollowToggle()

        vm.dismissActionError()

        val content = vm.state.value as ProfileUiState.Content
        assertThat(content.actionError).isNull()
        assertThat(content.profile.userId).isEqualTo("u")
    }
}
