package com.us.android.feature.chat.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.TYPING_TTL_MILLIS
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.chat.data.reconnectDelayMillis
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.TokenProvider
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class ChatThreadViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val socket: ChatSocket,
    private val tokenProvider: TokenProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val conversationId: String =
        savedStateHandle.get<String>(CONVERSATION_ID_KEY).orEmpty()

    private val controller = ThreadController(conversationId, repository)

    private val _state = MutableStateFlow(controller.snapshot())
    val state: StateFlow<ThreadUiState> = _state.asStateFlow()

    /**
     * One expiry timer per typing user.
     *
     * The server publishes only `is_typing: true` and lets a 3-second Redis key
     * expire; there is no stop frame. Without a local timer the indicator would
     * be permanent — which is exactly what the previous implementation did, and
     * why the flag it set was never cleared.
     */
    private val typingTimers = mutableMapOf<String, Job>()

    init {
        loadMembers()
        refresh()
        observeSocket()
    }

    fun refresh() = viewModelScope.launch {
        _state.value = controller.refresh()
        // Reading the thread is what marks it read; doing it on send instead
        // would leave a conversation unread for someone who only reads.
        controller.markRead()
    }

    fun loadMore() = viewModelScope.launch { _state.value = controller.loadMore() }

    fun onDraftChange(text: String) {
        _state.value = controller.onDraftChange(text)
    }

    fun send() = viewModelScope.launch { _state.value = controller.send() }

    /**
     * Fetches the member roster once, so every message can be attributed.
     *
     * One request for the whole thread, not one per message. The conversation
     * payload carries `display_name` for each member, which covers messages
     * that arrive over the socket (their frames have no name) and the viewer's
     * own sends (the send response omits the field the list response includes).
     *
     * A failure here is deliberately not surfaced as an error: an unnamed
     * message is worth reading, and blocking the thread on a roster fetch would
     * make a cosmetic failure look like a broken conversation.
     */
    private fun loadMembers() = viewModelScope.launch {
        when (val result = repository.conversation(conversationId)) {
            is AppResult.Success -> _state.value = controller.setMembers(result.data.members)
            is AppResult.Failure -> Unit
        }
    }

    /**
     * Keeps the socket up for as long as this screen lives.
     *
     * Reconnection is owned here rather than inside [ChatSocket] because the
     * loop has to end when the ViewModel does — a socket that reconnects on
     * its own outlives the screen that wanted it and cannot be told to stop.
     *
     * A permanent failure (401/403) breaks the loop: retrying a rejected
     * credential earns a rate limit, not a connection.
     *
     * SCOPE, HONESTLY: this is one socket per open thread, not one per session.
     * It is enough for the launch loop — read a thread, receive while it is
     * open — and it is not enough for inbox badges or unread counts while no
     * thread is open. Those depend on push notifications, which is where the
     * review put them. A session-scoped connection manager is the next step and
     * is deliberately not built here, because it changes who owns the token
     * lifecycle for every future realtime surface.
     */
    private fun observeSocket() = viewModelScope.launch {
        var attempt = 0
        while (true) {
            var permanent = false
            socket.connect { tokenProvider.currentAccessToken() }.collect { event ->
                when (event) {
                    is ChatSocketEvent.Connected -> attempt = 0
                    is ChatSocketEvent.Disconnected -> permanent = event.permanent
                    else -> apply(event)
                }
            }
            if (permanent) return@launch
            attempt += 1
            delay(reconnectDelayMillis(attempt))
            // History is re-fetched on every reconnect. Messages sent while
            // the socket was down were never broadcast to this client, so
            // resuming the stream alone would leave a silent hole in the
            // conversation. The controller de-duplicates by id, so a message
            // that arrived both ways still renders exactly once.
            _state.value = controller.refresh()
        }
    }

    /**
     * Applies one socket event.
     *
     * The controller decides what the event means — including dropping
     * messages addressed to another conversation, which matters because the
     * gateway subscribes one socket to every conversation the user belongs to
     * rather than to a room.
     */
    private fun apply(event: ChatSocketEvent) {
        controller.onSocketEvent(event)?.let { _state.value = it }
        if (event is ChatSocketEvent.Typing && event.isTyping &&
            event.conversationId == conversationId
        ) {
            restartTypingTimer(event.userId)
        }
    }

    private fun restartTypingTimer(userId: String) {
        typingTimers.remove(userId)?.cancel()
        typingTimers[userId] = viewModelScope.launch {
            delay(TYPING_TTL_MILLIS)
            controller.onTypingStopped(userId)?.let { _state.value = it }
            typingTimers.remove(userId)
        }
    }

    private companion object {
        const val CONVERSATION_ID_KEY = "conversationId"
    }
}
