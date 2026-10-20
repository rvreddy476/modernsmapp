package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.MessageConsentRepository
import com.us.android.core.chat.data.MessageDecision
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import com.us.android.core.common.result.AppResult
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
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
    /**
     * DM consent: the button graph-service's decision draws — Message, Send
     * request, Requested, a disabled Message with its reason, or none. Plain
     * "Message" until the decision is read, and whenever it cannot be.
     */
    val button: MessageButtonState = MessageButtonState.Enabled(MessageButtonKind.Message),
    /** The viewer's own request to this person, still waiting: "Requested" opens it. */
    val pendingRequest: OpenConversation? = null,
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
 * DM consent (2026-10-20): the button is drawn from graph-service's
 * `message` decision ([check]), so the viewer sees "Send request" or the
 * reason they can't message BEFORE tapping. That decision is the server's,
 * read on every visit — never a local copy of the privacy rules — and the
 * start itself is still decided again by the server when tapped.
 *
 * `:feature:profile` depends on `:core:chat`, which is a feature depending on a
 * core module and exactly what the module rule allows. It does NOT depend on
 * `:feature:chat` — the screens stay unreachable from here, and `:app` decides
 * that the returned conversation opens the thread destination.
 */
@HiltViewModel
class StartChatViewModel @Inject constructor(
    repository: ChatRepository,
    // Null in tests that only exercise the start: the button then stays "Message".
    private val consent: MessageConsentRepository? = null,
) : ViewModel() {

    private val controller = StartDirectController(repository)

    private val _state = MutableStateFlow(StartChatUiState())
    val state: StateFlow<StartChatUiState> = _state.asStateFlow()

    private var checkJob: Job? = null

    /**
     * Reads the message decision for [userId] and draws the button from it.
     * Called when the profile loads, on every return to it, and after a
     * follow changes (following can earn a request under "Followers").
     */
    fun check(userId: String, displayName: String, handle: String) {
        val consent = consent ?: return
        if (userId.isBlank()) return
        checkJob?.cancel()
        checkJob = viewModelScope.launch {
            val permission = (consent.messagePermission(userId) as? AppResult.Success)?.data
            val pending = if (permission?.decision == MessageDecision.Request) {
                (consent.pendingRequestTo(userId) as? AppResult.Success)?.data
                    ?.let { OpenConversation(it.id, displayName) }
            } else {
                null
            }
            _state.update {
                it.copy(
                    button = messageButtonFor(permission, handle, hasPendingRequest = pending != null),
                    pendingRequest = pending,
                )
            }
        }
    }

    /**
     * Starts, or reopens, the conversation with [userId].
     *
     * [displayName] is only for the thread's top bar — the server decides
     * which conversation this is, and an existing thread (or the viewer's
     * waiting request) is returned rather than a second one created. A
     * refusal comes back as [StartDirectResult.NotAllowed], explained in the
     * server's own reason when it gives one.
     */
    fun open(userId: String, displayName: String, handle: String = displayName) {
        val current = _state.value
        if (current.busy) return
        current.pendingRequest?.let { waiting ->
            _state.update { it.copy(openConversation = waiting) }
            return
        }
        _state.value = current.copy(busy = true, error = null, notAllowed = null)

        viewModelScope.launch {
            val result = controller.open(userId)
            _state.value = when (result) {
                is StartDirectResult.Opened -> _state.value.copy(
                    busy = false,
                    openConversation = OpenConversation(
                        conversationId = result.conversation.id,
                        title = displayName,
                    ),
                )

                is StartDirectResult.NotAllowed -> _state.value.copy(
                    busy = false,
                    notAllowed = startRefusalText(result.error, handle),
                )

                is StartDirectResult.Failed -> {
                    // A request rate limit or the cooldown is a refusal with
                    // its own words, not a failure to retry at once.
                    val refusal = consentRefusalOrNull(result.error, handle)
                    _state.value.copy(
                        busy = false,
                        notAllowed = refusal,
                        error = if (refusal == null) startFailureText(result.error) else null,
                    )
                }
            }
            // A refusal means the decision moved since it was read: draw it again.
            if (result is StartDirectResult.NotAllowed) check(userId, displayName, handle)
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
