package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the Message button on a profile is doing right now. */
data class StartChatUiState(
    val busy: Boolean = false,
    /**
     * Set when the server refused on policy grounds.
     *
     * Held separately from [error] because it is NOT retryable, and the UI
     * must not offer a retry that cannot succeed.
     */
    val notAllowed: String? = null,
    val error: String? = null,
    /** The conversation to open. Consumed once, then cleared. */
    val openConversation: OpenConversation? = null,
)

/** A conversation the host should navigate to. */
data class OpenConversation(val conversationId: String, val title: String)

/**
 * Opens a direct conversation from a profile.
 *
 * Its own ViewModel rather than another responsibility on `ProfileViewModel`:
 * this owns a network intent with an idempotency key whose lifetime is the
 * screen, and folding it into the profile loader would tie the key's lifetime
 * to profile refreshes.
 *
 * `:feature:profile` depends on `:core:chat`, which is a feature depending on a
 * core module and exactly what the module rule allows. It does NOT depend on
 * `:feature:chat` — the screens stay unreachable from here, and `:app` decides
 * that the returned conversation opens the thread destination.
 */
@HiltViewModel
class StartChatViewModel @Inject constructor(
    repository: ChatRepository,
) : ViewModel() {

    private val controller = StartDirectController(repository)

    private val _state = MutableStateFlow(StartChatUiState())
    val state: StateFlow<StartChatUiState> = _state.asStateFlow()

    /**
     * Starts, or reopens, the conversation with [userId].
     *
     * [displayName] is only for the thread's top bar — the server decides
     * which conversation this is, and an existing thread is returned rather
     * than a second one created. The client never guesses eligibility either:
     * a refusal comes back as [StartDirectResult.NotAllowed].
     */
    fun open(userId: String, displayName: String) {
        if (_state.value.busy) return
        _state.value = _state.value.copy(busy = true, error = null, notAllowed = null)

        viewModelScope.launch {
            _state.value = when (val result = controller.open(userId)) {
                is StartDirectResult.Opened -> _state.value.copy(
                    busy = false,
                    openConversation = OpenConversation(
                        conversationId = result.conversation.id,
                        title = displayName,
                    ),
                )

                is StartDirectResult.NotAllowed -> _state.value.copy(
                    busy = false,
                    notAllowed = "$displayName isn't accepting messages from you.",
                )

                is StartDirectResult.Failed -> _state.value.copy(
                    busy = false,
                    error = "We couldn't open that conversation.",
                )
            }
        }
    }

    /**
     * Clears the navigation request after the host has acted on it.
     *
     * Without this the state would re-emit on every recomposition and the
     * thread would be pushed onto the back stack again each time the profile
     * resumed.
     */
    fun onConversationOpened() {
        _state.value = _state.value.copy(openConversation = null)
    }

    fun dismissError() {
        _state.value = _state.value.copy(error = null, notAllowed = null)
    }
}
