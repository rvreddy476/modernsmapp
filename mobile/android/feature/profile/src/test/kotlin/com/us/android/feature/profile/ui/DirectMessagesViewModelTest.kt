package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.PrivacySettingsApi
import com.us.android.core.profile.data.PrivacySettingsRepository
import com.us.android.core.profile.data.dto.PrivacySettingsDto
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class DirectMessagesViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : PrivacySettingsApi {
        var loaded = PrivacySettingsDto(whoCanMessage = "connections_only")

        override suspend fun privacy() = ApiEnvelope(loaded)

        override suspend fun updatePrivacy(body: UpdatePrivacySettingsRequest): ApiEnvelope<PrivacySettingsDto> {
            loaded = loaded.copy(whoCanMessage = body.whoCanMessage)
            return ApiEnvelope(loaded)
        }
    }

    private fun buildViewModel(api: FakeApi = FakeApi()) =
        DirectMessagesViewModel(PrivacySettingsRepository(api, ErrorMapper(json))) to api

    private fun DirectMessagesViewModel.loaded() = state.value as DirectMessagesUiState.Loaded

    @Test
    fun `loads the current audience from the connections-only default`() = runTest {
        val (viewModel, _) = buildViewModel()

        val audience = viewModel.loaded().audience
        assertThat(audience.everyoneRequests).isFalse()
        assertThat(audience.followersRequests).isFalse()
        assertThat(audience.friendsDirect).isTrue()
    }

    @Test
    fun `turning Everyone on commits immediately, without a separate save step`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setEveryone(true)

        assertThat(api.loaded.whoCanMessage).isEqualTo("everyone_message_requests")
        assertThat(viewModel.loaded().audience.everyoneRequests).isTrue()
        assertThat(viewModel.loaded().saving).isFalse()
    }

    @Test
    fun `turning Friends off with nothing else on lands on no_one`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setFriends(false)

        assertThat(api.loaded.whoCanMessage).isEqualTo("no_one")
        assertThat(viewModel.loaded().audience).isEqualTo(
            com.us.android.core.profile.data.DirectMessageAudience(false, false, false),
        )
    }
}
