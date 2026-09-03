package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.DirectMessageAudience
import com.us.android.core.profile.data.PrivacySettingsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface DirectMessagesUiState {
    data object Loading : DirectMessagesUiState
    data class Error(val message: String) : DirectMessagesUiState
    data class Loaded(val audience: DirectMessageAudience, val saving: Boolean = false) : DirectMessagesUiState
}

/**
 * "Who can message you", as its own immediate-effect screen rather than a
 * field on the pending Privacy form: each row commits the instant it is
 * tapped, the same way the equivalent TikTok screen behaves. It reads and
 * writes `who_can_message` through the same full-snapshot endpoint as the
 * Privacy screen, but always against the server's CURRENT settings — opening
 * this screen with unsaved edits pending on Privacy is not a supported path,
 * matching how the two screens are reached (Direct messages is one tap
 * beneath Privacy, and a tap here reloads rather than assuming the caller's
 * in-memory draft).
 */
@HiltViewModel
class DirectMessagesViewModel @Inject constructor(
    private val repository: PrivacySettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<DirectMessagesUiState>(DirectMessagesUiState.Loading)
    val state: StateFlow<DirectMessagesUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.value = DirectMessagesUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success ->
                    DirectMessagesUiState.Loaded(DirectMessageAudience.fromRaw(result.data.whoCanMessage))
                is AppResult.Failure -> DirectMessagesUiState.Error("Message settings could not be loaded.")
            }
        }
    }

    fun setEveryone(requests: Boolean) = apply { it.copy(everyoneRequests = requests) }
    fun setFollowers(requests: Boolean) = apply { it.copy(followersRequests = requests) }
    fun setFriends(direct: Boolean) = apply { it.copy(friendsDirect = direct) }

    private fun apply(edit: (DirectMessageAudience) -> DirectMessageAudience) {
        val current = _state.value as? DirectMessagesUiState.Loaded ?: return
        if (current.saving) return
        val next = edit(current.audience)
        _state.value = current.copy(audience = next, saving = true)
        viewModelScope.launch {
            _state.value = when (val settings = repository.get()) {
                is AppResult.Success -> when (
                    val saved = repository.save(settings.data.copy(whoCanMessage = next.toRaw()))
                ) {
                    is AppResult.Success ->
                        DirectMessagesUiState.Loaded(DirectMessageAudience.fromRaw(saved.data.whoCanMessage))
                    is AppResult.Failure -> current
                }
                is AppResult.Failure -> current
            }
        }
    }
}
