package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.PrivacySettings
import com.us.android.core.profile.data.PrivacySettingsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface PrivacySettingsUiState {
    data object Loading : PrivacySettingsUiState
    data class Error(val message: String) : PrivacySettingsUiState
    data class Editing(
        val original: PrivacySettings,
        val value: PrivacySettings,
        val saving: Boolean = false,
        val message: String? = null,
    ) : PrivacySettingsUiState {
        val dirty get() = original != value
    }
}

enum class PrivacyEnumField {
    MESSAGE, PHOTO, GROUP_ADD, ONLINE, LAST_SEEN, READ_RECEIPTS, CHAT_AVAILABILITY, COMMENTS,

    /** Who can mention me / Who can tag me (2026-10-24): everyone | following | no_one. */
    MENTION, TAG,
}
enum class PrivacyToggleField { STRICT, TC_POSTS, TYPING, MESSAGE_PREVIEW }

@HiltViewModel
class PrivacySettingsViewModel @Inject constructor(
    private val repository: PrivacySettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<PrivacySettingsUiState>(PrivacySettingsUiState.Loading)
    val state: StateFlow<PrivacySettingsUiState> = _state.asStateFlow()
    init { load() }

    fun load() {
        _state.value = PrivacySettingsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success -> PrivacySettingsUiState.Editing(result.data, result.data)
                is AppResult.Failure -> PrivacySettingsUiState.Error("Privacy settings could not be loaded.")
            }
        }
    }

    fun select(field: PrivacyEnumField, value: String) = edit { current ->
        when (field) {
            PrivacyEnumField.MESSAGE -> current.copy(whoCanMessage = value)
            PrivacyEnumField.PHOTO -> current.copy(whoCanSeeProfilePhoto = value)
            PrivacyEnumField.GROUP_ADD -> current.copy(whoCanAddToGroups = value)
            PrivacyEnumField.ONLINE -> current.copy(whoCanSeeOnlineStatus = value)
            PrivacyEnumField.LAST_SEEN -> current.copy(whoCanSeeLastSeen = value)
            PrivacyEnumField.READ_RECEIPTS -> current.copy(whoCanSeeReadReceipts = value)
            PrivacyEnumField.CHAT_AVAILABILITY -> current.copy(chatAvailability = value)
            PrivacyEnumField.COMMENTS -> current.copy(allowCommentsFrom = value)
            // Never introduce a key the server did not send (null = row hidden).
            PrivacyEnumField.MENTION -> if (current.whoCanMention == null) current else current.copy(whoCanMention = value)
            PrivacyEnumField.TAG -> if (current.whoCanTag == null) current else current.copy(whoCanTag = value)
        }
    }

    /**
     * "Private account". Kept separate from [select] because the caller (the
     * confirm dialog) only ever turns this ON with an explicit confirmation —
     * turning it back OFF needs none — and folding a boolean into the generic
     * string setter would hide that asymmetry.
     */
    fun setPrivateAccount(private: Boolean) = edit { current ->
        current.copy(
            accountVisibility = if (private) PrivacySettings.VISIBILITY_PRIVATE else PrivacySettings.VISIBILITY_PUBLIC,
        )
    }

    fun toggle(field: PrivacyToggleField, enabled: Boolean) = edit { current ->
        when (field) {
            PrivacyToggleField.STRICT -> if (enabled) current.strict() else current.copy(strictPrivacyMode = false)
            PrivacyToggleField.TC_POSTS -> current.copy(trustedCircleCloseFriendsPosts = enabled)
            PrivacyToggleField.TYPING -> current.copy(sendTypingIndicators = enabled)
            PrivacyToggleField.MESSAGE_PREVIEW -> current.copy(showMessagePreview = enabled)
        }
    }

    fun save() {
        val current = _state.value as? PrivacySettingsUiState.Editing ?: return
        if (!current.dirty || current.saving) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value)) {
                // The screen shows what the SERVER stored: a minor or strict
                // account asking for "everyone" is narrowed to "following".
                is AppResult.Success -> PrivacySettingsUiState.Editing(
                    result.data,
                    result.data,
                    message = savedMessage(sent = current.value, stored = result.data),
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }

    private fun edit(block: (PrivacySettings) -> PrivacySettings) = _state.update { state ->
        val editing = state as? PrivacySettingsUiState.Editing ?: return@update state
        if (editing.saving) {
            return@update editing
        }
        editing.copy(value = block(editing.value), message = null)
    }
}

internal const val PRIVACY_SAVED = "Privacy settings saved."
internal const val PRIVACY_SAVED_MENTIONS_NARROWED =
    "Privacy settings saved. Your account's protections allow only people you follow to mention or tag you."

/**
 * The save confirmation. When the server stored a narrower mention or tag
 * audience than the one sent (minors and Strict Privacy Mode: everyone →
 * following, user-service service/user.go applyStrictMode), say so — the
 * rows already show the stored value.
 */
internal fun savedMessage(sent: PrivacySettings, stored: PrivacySettings): String {
    val narrowed = (sent.whoCanMention != null && sent.whoCanMention != stored.whoCanMention) ||
        (sent.whoCanTag != null && sent.whoCanTag != stored.whoCanTag)
    return if (narrowed) PRIVACY_SAVED_MENTIONS_NARROWED else PRIVACY_SAVED
}

private fun narrowAudience(value: String?): String? =
    if (value == PrivacySettings.AUDIENCE_EVERYONE) PrivacySettings.AUDIENCE_FOLLOWING else value

private fun PrivacySettings.strict() = copy(
    whoCanMention = narrowAudience(whoCanMention),
    whoCanTag = narrowAudience(whoCanTag),
    strictPrivacyMode = true,
    whoCanMessage = "connections_only",
    whoCanCall = "no_one",
    whoCanAddToGroups = "no_one",
    whoCanSeeOnlineStatus = "connections_only",
    whoCanSeeReadReceipts = "connections_only",
    whoCanSeeLastSeen = "connections_only",
    whoCanSeeProfilePhoto = "connections_only",
    blockUnknownCalls = true,
    autoFilterAbusiveContent = true,
    allowPhoneDiscovery = false,
    discoverableByPhoneToContacts = false,
)
