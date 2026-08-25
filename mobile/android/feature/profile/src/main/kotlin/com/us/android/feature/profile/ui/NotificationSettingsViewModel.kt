package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.NotificationSettings
import com.us.android.core.profile.data.NotificationSettingsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface NotificationSettingsUiState {
    data object Loading : NotificationSettingsUiState

    data class Error(val message: String) : NotificationSettingsUiState

    data class Editing(
        val original: NotificationSettings,
        val value: NotificationSettings,
        val saving: Boolean = false,
        val message: String? = null,
    ) : NotificationSettingsUiState {
        val dirty get() = original != value
        val quietHoursValid get() = !value.quietHoursEnabled || (
            TIME.matches(value.quietHoursStart) &&
                TIME.matches(value.quietHoursEnd) &&
                value.quietHoursTimeZone.isNotBlank()
            )

        companion object {
            val TIME = Regex("^(?:[01]\\d|2[0-3]):[0-5]\\d$")
        }
    }
}

enum class NotificationToggle {
    PUSH, EMAIL, QUIET, LIKES, SUPER_LIKES, COMMENTS, REPLIES, MENTIONS, FOLLOWS,
    FRIEND_REQUESTS, GROUP_POSTS, GROUP_MENTIONS, CHANNEL_UPDATES, CHANNEL_URGENT,
    COMMUNITY_POSTS, COMMUNITY_MENTIONS, EVENTS, SYSTEM,
}

@HiltViewModel
class NotificationSettingsViewModel @Inject constructor(
    private val repository: NotificationSettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<NotificationSettingsUiState>(NotificationSettingsUiState.Loading)
    val state: StateFlow<NotificationSettingsUiState> = _state.asStateFlow()
    init {
        load()
    }

    fun load() {
        _state.value = NotificationSettingsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success -> NotificationSettingsUiState.Editing(result.data, result.data)
                is AppResult.Failure -> NotificationSettingsUiState.Error("Notification settings could not be loaded.")
            }
        }
    }

    @Suppress("CyclomaticComplexMethod") // Exhaustive enum-to-setting mapping; each branch is one immutable copy.
    fun toggle(field: NotificationToggle, enabled: Boolean) = edit { value ->
        when (field) {
            NotificationToggle.PUSH -> value.copy(pushEnabled = enabled)
            NotificationToggle.EMAIL -> value.copy(emailEnabled = enabled)
            NotificationToggle.QUIET -> value.copy(quietHoursEnabled = enabled)
            NotificationToggle.LIKES -> value.copy(pushLikes = enabled)
            NotificationToggle.SUPER_LIKES -> value.copy(pushSuperLikes = enabled)
            NotificationToggle.COMMENTS -> value.copy(pushComments = enabled)
            NotificationToggle.REPLIES -> value.copy(pushReplies = enabled)
            NotificationToggle.MENTIONS -> value.copy(pushMentions = enabled)
            NotificationToggle.FOLLOWS -> value.copy(pushFollows = enabled)
            NotificationToggle.FRIEND_REQUESTS -> value.copy(pushFriendRequests = enabled)
            NotificationToggle.GROUP_POSTS -> value.copy(pushGroupPosts = enabled)
            NotificationToggle.GROUP_MENTIONS -> value.copy(pushGroupMentions = enabled)
            NotificationToggle.CHANNEL_UPDATES -> value.copy(pushChannelUpdates = enabled)
            NotificationToggle.CHANNEL_URGENT -> value.copy(pushChannelUrgent = enabled)
            NotificationToggle.COMMUNITY_POSTS -> value.copy(pushCommunityPosts = enabled)
            NotificationToggle.COMMUNITY_MENTIONS -> value.copy(pushCommunityMentions = enabled)
            NotificationToggle.EVENTS -> value.copy(pushEventReminders = enabled)
            NotificationToggle.SYSTEM -> value.copy(pushSystem = enabled)
        }
    }

    fun quietHours(start: String? = null, end: String? = null, timezone: String? = null) = edit {
        it.copy(
            quietHoursStart = start ?: it.quietHoursStart,
            quietHoursEnd = end ?: it.quietHoursEnd,
            quietHoursTimeZone = timezone ?: it.quietHoursTimeZone,
        )
    }
    fun digest(value: String) = edit { it.copy(emailDigest = value) }

    fun save() {
        val current = _state.value as? NotificationSettingsUiState.Editing ?: return
        if (!current.dirty || current.saving || !current.quietHoursValid) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value)) {
                is AppResult.Success -> NotificationSettingsUiState.Editing(
                    result.data,
                    result.data,
                    message = "Notification settings saved.",
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }

    private fun edit(block: (NotificationSettings) -> NotificationSettings) = _state.update { state ->
        val current = state as? NotificationSettingsUiState.Editing ?: return@update state
        if (current.saving) current else current.copy(value = block(current.value), message = null)
    }
}
