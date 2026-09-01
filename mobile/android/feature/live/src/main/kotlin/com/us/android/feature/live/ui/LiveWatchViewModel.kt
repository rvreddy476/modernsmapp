package com.us.android.feature.live.ui

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.SendChatRequest
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import io.livekit.android.LiveKit
import io.livekit.android.events.RoomEvent
import io.livekit.android.events.collect
import io.livekit.android.room.Room
import io.livekit.android.room.track.VideoTrack
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * A viewer's session: viewer token → subscriber-only LiveKit connect → the
 * first remote video track is the show. Chat is REST: poll for the tail,
 * POST to speak — the ws-gateway live-tail subscription is a later step.
 */
@HiltViewModel
class LiveWatchViewModel @Inject constructor(
    private val api: LiveApi,
    @ApplicationContext private val appContext: Context,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    data class UiState(
        val title: String = "",
        val connecting: Boolean = true,
        val error: String? = null,
        val ended: Boolean = false,
        val chat: List<LiveChatMessageDto> = emptyList(),
        val draft: String = "",
        /** Bumped when the remote video track changes so the UI re-attaches. */
        val videoVersion: Int = 0,
    )

    private val streamId: String = savedStateHandle.get<String>("streamId").orEmpty()

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    var room: Room? = null
        private set

    /** The broadcaster's video, once subscribed. */
    var remoteVideo: VideoTrack? = null
        private set

    init {
        viewModelScope.launch { connect() }
        viewModelScope.launch { pollChat() }
    }

    private suspend fun connect() {
        runCatching {
            val stream = api.getStream(streamId).data
            _state.value = _state.value.copy(title = stream?.title.orEmpty())
            val grant = requireNotNull(api.viewerToken(streamId).data) { "no viewer token" }

            val lkRoom = LiveKit.create(appContext)
            room = lkRoom
            viewModelScope.launch {
                lkRoom.events.collect { event ->
                    when (event) {
                        is RoomEvent.TrackSubscribed -> {
                            (event.track as? VideoTrack)?.let { track ->
                                remoteVideo = track
                                _state.value = _state.value.copy(
                                    connecting = false,
                                    videoVersion = _state.value.videoVersion + 1,
                                )
                            }
                        }
                        is RoomEvent.Disconnected ->
                            _state.value = _state.value.copy(ended = true)
                        else -> Unit
                    }
                }
            }
            lkRoom.connect(grant.serverUrl, grant.token)
        }.onFailure { error ->
            _state.value = _state.value.copy(
                connecting = false,
                error = error.message ?: "Couldn't join the stream",
            )
        }
    }

    private suspend fun pollChat() {
        while (viewModelScope.isActive) {
            runCatching { api.listChat(streamId).data }
                .getOrNull()
                ?.let { messages -> _state.value = _state.value.copy(chat = messages) }
            delay(CHAT_POLL_MILLIS)
        }
    }

    fun onDraftChanged(draft: String) {
        _state.value = _state.value.copy(draft = draft)
    }

    fun onSendChat() {
        val text = _state.value.draft.trim()
        if (text.isEmpty()) return
        _state.value = _state.value.copy(draft = "")
        viewModelScope.launch {
            runCatching { api.sendChat(streamId, SendChatRequest(text = text)) }
        }
    }

    override fun onCleared() {
        room?.disconnect()
        room = null
    }

    private companion object {
        const val CHAT_POLL_MILLIS = 3_000L
    }
}
