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

        /** user-service applyStrictMode (service/user.go:257-266): a minor or strict account is never stored as "everyone". */
        private fun narrowed(value: String?): String? =
            if (value == "everyone" && (loaded.under18Mode || loaded.strictPrivacyMode)) "following" else value

        override suspend fun privacy() = ApiEnvelope(loaded)

        override suspend fun updatePrivacy(body: UpdatePrivacySettingsRequest): ApiEnvelope<PrivacySettingsDto> {
            lastRequest = body
            return ApiEnvelope(
                loaded.copy(
                    accountVisibility = body.accountVisibility,
                    allowCommentsFrom = body.allowCommentsFrom,
                    whoCanMessage = body.whoCanMessage,
                    whoCanMention = narrowed(body.whoCanMention ?: loaded.whoCanMention),
                    whoCanTag = narrowed(body.whoCanTag ?: loaded.whoCanTag),
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

    // ── Allow connection requests (2026-10-24) ───────────────────────────

    @Test
    fun `turning connection requests off sends no_one`() = runTest {
        val (viewModel, api) = buildViewModel(FakeApi().apply { loaded = PrivacySettingsDto(whoCanSendConnectionRequest = "everyone") })

        viewModel.setAllowConnectionRequests(false)
        viewModel.save()

        assertThat(requireNotNull(api.lastRequest).whoCanSendConnectionRequest).isEqualTo("no_one")
    }

    @Test
    fun `off and back on restores the saved narrower audience`() = runTest {
        val (viewModel, _) = buildViewModel(
            FakeApi().apply { loaded = PrivacySettingsDto(whoCanSendConnectionRequest = "friends_of_friends") },
        )

        viewModel.setAllowConnectionRequests(false)
        viewModel.setAllowConnectionRequests(true)

        assertThat(viewModel.editing().value.whoCanSendConnectionRequest).isEqualTo("friends_of_friends")
        assertThat(viewModel.editing().dirty).isFalse()
    }

    @Test
    fun `turning them on from no_one opens them to everyone`() = runTest {
        val (viewModel, _) = buildViewModel(FakeApi().apply { loaded = PrivacySettingsDto(whoCanSendConnectionRequest = "no_one") })

        viewModel.setAllowConnectionRequests(true)

        assertThat(viewModel.editing().value.whoCanSendConnectionRequest).isEqualTo("everyone")
    }

    // ── Who can mention me / Who can tag me (2026-10-24) ──────────────────

    @Test
    fun `mention and tag audiences are saved in the snapshot and read back from the server`() = runTest {
        val (viewModel, api) = buildViewModel(
            FakeApi().apply { loaded = PrivacySettingsDto(whoCanMention = "everyone", whoCanTag = "everyone") },
        )

        viewModel.select(PrivacyEnumField.MENTION, PrivacySettings.AUDIENCE_FOLLOWING)
        viewModel.select(PrivacyEnumField.TAG, PrivacySettings.AUDIENCE_NO_ONE)
        viewModel.save()

        val request = requireNotNull(api.lastRequest)
        assertThat(request.whoCanMention).isEqualTo("following")
        assertThat(request.whoCanTag).isEqualTo("no_one")
        assertThat(viewModel.editing().value.whoCanMention).isEqualTo("following")
        assertThat(viewModel.editing().value.whoCanTag).isEqualTo("no_one")
        assertThat(viewModel.editing().message).isEqualTo(PRIVACY_SAVED)
    }

    @Test
    fun `a server without the keys keeps the rows hidden and the PUT without them`() = runTest {
        val (viewModel, api) = buildViewModel()

        viewModel.select(PrivacyEnumField.MENTION, PrivacySettings.AUDIENCE_NO_ONE)
        viewModel.select(PrivacyEnumField.COMMENTS, PrivacySettings.COMMENTS_FRIENDS)
        viewModel.save()

        val request = requireNotNull(api.lastRequest)
        assertThat(request.whoCanMention).isNull()
        assertThat(request.whoCanTag).isNull()
        assertThat(viewModel.editing().value.whoCanMention).isNull()
    }

    @Test
    fun `a minor asking for everyone is shown the narrower value the server stored, and told why`() = runTest {
        val (viewModel, api) = buildViewModel(
            FakeApi().apply { loaded = PrivacySettingsDto(under18Mode = true, whoCanMention = "no_one", whoCanTag = "following") },
        )

        viewModel.select(PrivacyEnumField.MENTION, PrivacySettings.AUDIENCE_EVERYONE)
        viewModel.save()

        assertThat(requireNotNull(api.lastRequest).whoCanMention).isEqualTo("everyone")
        val editing = viewModel.editing()
        assertThat(editing.value.whoCanMention).isEqualTo("following")
        assertThat(editing.dirty).isFalse()
        assertThat(editing.message).isEqualTo(PRIVACY_SAVED_MENTIONS_NARROWED)
    }

    @Test
    fun `strict privacy narrows everyone to people you follow, and leaves the stricter choices`() = runTest {
        val (viewModel, _) = buildViewModel(
            FakeApi().apply { loaded = PrivacySettingsDto(whoCanMention = "everyone", whoCanTag = "no_one") },
        )

        viewModel.toggle(PrivacyToggleField.STRICT, true)

        assertThat(viewModel.editing().value.whoCanMention).isEqualTo("following")
        assertThat(viewModel.editing().value.whoCanTag).isEqualTo("no_one")
        assertThat(viewModel.editing().value.mentionAudienceNarrowed).isTrue()
    }

    @Test
    fun `the saved message names the narrowing only when the stored value differs from the one sent`() {
        val sent = PrivacySettingsRepositoryFixtures.settings(whoCanMention = "everyone", whoCanTag = "following")
        assertThat(savedMessage(sent, sent)).isEqualTo(PRIVACY_SAVED)
        assertThat(savedMessage(sent, sent.copy(whoCanMention = "following"))).isEqualTo(PRIVACY_SAVED_MENTIONS_NARROWED)
        assertThat(savedMessage(sent, sent.copy(whoCanTag = "no_one"))).isEqualTo(PRIVACY_SAVED_MENTIONS_NARROWED)
        assertThat(savedMessage(sent.copy(whoCanMention = null, whoCanTag = null), sent)).isEqualTo(PRIVACY_SAVED)
    }

    @Test
    fun `minors and strict accounts are offered no Everyone`() {
        assertThat(AUDIENCE_OPTIONS.map { it.value }).containsExactly("everyone", "following", "no_one").inOrder()
        assertThat(AUDIENCE_OPTIONS.map { it.label }).containsExactly("Everyone", "People you follow", "No one").inOrder()
        assertThat(NARROWED_AUDIENCE_OPTIONS.map { it.value }).containsExactly("following", "no_one").inOrder()
    }
}

/** A domain snapshot with every field set, for the pure checks. */
private object PrivacySettingsRepositoryFixtures {
    fun settings(whoCanMention: String?, whoCanTag: String?) = PrivacySettings(
        whoCanMessage = "people_i_follow",
        whoCanSendConnectionRequest = "everyone",
        whoCanCall = "accepted_chats_only",
        whoCanAddToGroups = "connections_only",
        whoCanSeeOnlineStatus = "connections_only",
        whoCanSeeReadReceipts = "connections_only",
        whoCanSeeLastSeen = "connections_only",
        whoCanSeeProfilePhoto = "everyone",
        allowPhoneDiscovery = false,
        allowContactSyncMatch = false,
        discoverableByPhoneToContacts = false,
        strictPrivacyMode = false,
        blockUnknownCalls = false,
        autoFilterAbusiveContent = true,
        under18Mode = false,
        trustedCircleCloseFriendsPosts = true,
        trustedCircleLocationPings = true,
        trustedCircleAfterHoursPosts = true,
        trustedCircleAudioRoomInvites = true,
        chatAvailability = "enabled",
        sendTypingIndicators = true,
        showMessagePreview = true,
        accountVisibility = "public",
        allowCommentsFrom = "everyone",
        privacyVersion = 1,
        whoCanMention = whoCanMention,
        whoCanTag = whoCanTag,
    )
}
