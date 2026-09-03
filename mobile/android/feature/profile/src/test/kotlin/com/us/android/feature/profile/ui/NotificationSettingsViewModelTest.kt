package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationPreferenceCodec
import com.us.android.core.profile.data.NotificationSettingsApi
import com.us.android.core.profile.data.NotificationSettingsRepository
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test

class NotificationSettingsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** Echoes the request back by decoding and re-encoding it, like the real endpoint's merge. */
    private class FakeApi : NotificationSettingsApi {
        var lastRequest: JsonObject? = null

        override suspend fun notifications() = ApiEnvelope(DEFAULT_BODY)

        override suspend fun updateNotifications(body: JsonObject): ApiEnvelope<JsonObject> {
            lastRequest = body
            return ApiEnvelope(body)
        }

        companion object {
            val DEFAULT_BODY = NotificationPreferenceCodec.encode(
                NotificationPreferenceCodec.decode(JsonObject(emptyMap())),
            )
        }
    }

    private fun buildViewModel(api: FakeApi = FakeApi()) =
        NotificationSettingsViewModel(NotificationSettingsRepository(api, ErrorMapper(json))) to api

    private fun NotificationSettingsViewModel.editing() = state.value as NotificationSettingsUiState.Editing

    @Test
    fun `a primary category's in-app and push switches are independent`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.setChannel(NotificationCategory.LIKES, inApp = false)

        val channels = viewModel.editing().value.channels(NotificationCategory.LIKES)
        assertThat(channels.inApp).isFalse()
        assertThat(channels.push).isTrue()
    }

    @Test
    fun `push can be turned off while in-app stays on for the same category`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.setChannel(NotificationCategory.COMMENTS, push = false)

        val channels = viewModel.editing().value.channels(NotificationCategory.COMMENTS)
        assertThat(channels.inApp).isTrue()
        assertThat(channels.push).isFalse()
    }

    @Test
    fun `a secondary category round-trips through save the same way a primary one does`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.setChannel(NotificationCategory.FRIEND_REQUESTS, inApp = false, push = false)
        viewModel.save()

        val saved = NotificationPreferenceCodec.decode(requireNotNull(api.lastRequest))
        val channels = saved.channels(NotificationCategory.FRIEND_REQUESTS)
        assertThat(channels.inApp).isFalse()
        assertThat(channels.push).isFalse()
        assertThat(viewModel.editing().dirty).isFalse()
    }

    @Test
    fun `the More section starts collapsed and toggles independently of dirty state`() = runTest {
        val (viewModel, _) = buildViewModel()

        assertThat(viewModel.editing().moreExpanded).isFalse()
        viewModel.toggleMore()
        assertThat(viewModel.editing().moreExpanded).isTrue()
        assertThat(viewModel.editing().dirty).isFalse()
    }
}
