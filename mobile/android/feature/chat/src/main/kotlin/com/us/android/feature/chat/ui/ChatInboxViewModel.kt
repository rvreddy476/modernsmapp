package com.us.android.feature.chat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Conversation
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything the inbox renders. */
data class InboxUiState(
    val conversations: List<Conversation> = emptyList(),
    val loading: Boolean = false,
    val error: AppError? = null,
    /** Needed to name a direct thread after the OTHER participant. */
    val viewerId: String = "",
) {
    val isEmpty: Boolean get() = conversations.isEmpty() && !loading && error == null
}

@HiltViewModel
class ChatInboxViewModel @Inject constructor(
    private val repository: ChatRepository,
    authRepository: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(
        InboxUiState(
            viewerId = (authRepository.sessionState.value as? SessionState.Authenticated)
                ?.userId.orEmpty(),
        ),
    )
    val state: StateFlow<InboxUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.conversations()) {
                is AppResult.Success -> _state.value.copy(
                    // Newest activity first. `updated_at` advances when a
                    // message is sent, which is what makes it the sort key
                    // rather than creation time.
                    conversations = result.data.sortedByDescending { it.updatedAt },
                    loading = false,
                    error = null,
                )

                is AppResult.Failure -> _state.value.copy(
                    loading = false,
                    // Loaded rows are preserved: a failed refresh must not
                    // empty someone's inbox over a transient network blip.
                    error = result.error,
                )
            }
        }
    }
}
