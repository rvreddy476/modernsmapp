package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.PrivacySettings
import com.us.android.core.profile.data.PrivacySettingsApi
import com.us.android.core.profile.data.PrivacySettingsRepository
import com.us.android.core.profile.data.dto.PrivacySettingsDto
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

class PrivacySettingsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** Echoes the request back, the same way the live server does. */
    private class FakeApi : PrivacySettingsApi {
        var loaded = PrivacySettingsDto()
        var lastRequest: UpdatePrivacySettingsRequest? = null

        override suspend fun privacy() = ApiEnvelope(loaded)

        override suspend fun updatePrivacy(body: UpdatePrivacySettingsRequest): ApiEnvelope<PrivacySettingsDto> {
            lastRequest = body
            return ApiEnvelope(
                loaded.copy(
                    accountVisibility = body.accountVisibility,
                    allowCommentsFrom = body.allowCommentsFrom,
                    whoCanMessage = body.whoCanMessage,
                ),
            )
        }
    }

    private fun buildViewModel(api: FakeApi = FakeApi()) =
        PrivacySettingsViewModel(PrivacySettingsRepository(api, ErrorMapper(json))) to api

    private fun PrivacySettingsViewModel.editing() = state.value as PrivacySettingsUiState.Editing

    @Test
    fun `turning the private-account field on stages a value the screen can save`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.setPrivateAccount(true)

        val editing = viewModel.editing()
        assertThat(editing.value.accountVisibility).isEqualTo(PrivacySettings.VISIBILITY_PRIVATE)
        assertThat(editing.value.isPrivateAccount).isTrue()
        assertThat(editing.dirty).isTrue()
    }

    @Test
    fun `turning the private-account field back off returns to public`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.setPrivateAccount(true)
        viewModel.setPrivateAccount(false)

        assertThat(viewModel.editing().value.accountVisibility).isEqualTo(PrivacySettings.VISIBILITY_PUBLIC)
    }

    @Test
    fun `comments audience is a plain field edit`() = runTest {
        val (viewModel, _) = buildViewModel()

        viewModel.select(PrivacyEnumField.COMMENTS, PrivacySettings.COMMENTS_FRIENDS)

        assertThat(viewModel.editing().value.allowCommentsFrom).isEqualTo(PrivacySettings.COMMENTS_FRIENDS)
    }

    @Test
    fun `save sends the staged private-account and comments values in the full snapshot`() = runTest {
        val (viewModel, api) = buildViewModel()
        viewModel.setPrivateAccount(true)
        viewModel.select(PrivacyEnumField.COMMENTS, PrivacySettings.COMMENTS_FRIENDS)

        viewModel.save()

        val request = requireNotNull(api.lastRequest)
        assertThat(request.accountVisibility).isEqualTo(PrivacySettings.VISIBILITY_PRIVATE)
        assertThat(request.allowCommentsFrom).isEqualTo(PrivacySettings.COMMENTS_FRIENDS)
        assertThat(viewModel.editing().dirty).isFalse()
    }
}
