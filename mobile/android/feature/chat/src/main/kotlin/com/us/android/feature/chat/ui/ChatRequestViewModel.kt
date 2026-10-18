package com.us.android.feature.chat.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.requestTitle
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

enum class RequestOutcome { Accepted, Closed }

data class RequestUiState(
    val loading: Boolean = true,
    val preview: String? = null,
    val busy: Boolean = false,
    val error: String? = null,
    val outcome: RequestOutcome? = null,
    /**
     * Who sent it, from the request detail's `sender` (fix pass 2):
     * "Deleted account", their name or @username. Null until read — the
     * screen keeps the name it was opened with.
     */
    val title: String? = null,
    val avatarUrl: String? = null,
)

@HiltViewModel
class ChatRequestViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val store: ChatStore,
    session: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val viewerId: String =
        (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    val conversationId: String = savedStateHandle.get<String>("conversationId").orEmpty()

    private val _state = MutableStateFlow(RequestUiState())
    val state: StateFlow<RequestUiState> = _state.asStateFlow()

    init {
        loadPreview()
        loadSender()
    }

    /** The request detail names the sender; the list's name stands if it cannot be read. */
    private fun loadSender() = viewModelScope.launch {
        val detail = (repository.conversation(conversationId) as? AppResult.Success)?.data ?: return@launch
        _state.update {
            it.copy(
                title = detail.requestTitle(viewerId),
                avatarUrl = detail.sender?.takeUnless { who -> who.deleted }?.avatarUrl ?: detail.peer?.avatarUrl,
            )
        }
    }

    /** The introduction is the request conversation's single message. */
    private fun loadPreview() = viewModelScope.launch {
        when (val result = repository.messages(conversationId, limit = 1)) {
            is AppResult.Success -> _state.update {
                it.copy(loading = false, preview = result.data.items.firstOrNull()?.text)
            }
            is AppResult.Failure -> _state.update { it.copy(loading = false) }
        }
    }

    fun accept() = decide(RequestOutcome.Accepted) { repository.acceptRequest(conversationId) }

    fun decline() = decide(RequestOutcome.Closed) { repository.declineRequest(conversationId) }

    fun block() = decide(RequestOutcome.Closed) { repository.blockRequest(conversationId) }

    fun report() = decide(RequestOutcome.Closed) { repository.reportRequest(conversationId) }

    private fun decide(outcome: RequestOutcome, action: suspend () -> AppResult<Unit>) {
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (action()) {
                is AppResult.Success -> {
                    // The decision changed the inbox shape — resync the cache
                    // so the request row leaves (or becomes a conversation).
                    store.syncInbox()
                    _state.update { it.copy(busy = false, outcome = outcome) }
                }
                is AppResult.Failure -> _state.update {
                    it.copy(busy = false, error = "That didn't go through. Try again.")
                }
            }
        }
    }
}
