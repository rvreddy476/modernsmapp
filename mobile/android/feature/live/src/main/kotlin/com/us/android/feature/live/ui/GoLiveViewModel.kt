package com.us.android.feature.live.ui

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.live.data.CreateStreamRequest
import com.us.android.feature.live.data.LiveApi
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import io.livekit.android.LiveKit
import io.livekit.android.room.Room
import io.livekit.android.room.track.Track
import io.livekit.android.room.track.VideoTrack
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The broadcaster's lifecycle: title → create+start on live-service-v2 →
 * LiveKit publisher connect → camera+mic on → END on the way out.
 *
 * The room is owned HERE, not by the composable: a recomposition must never
 * cost a broadcast. onCleared is the backstop that stops the camera when the
 * screen dies in any way other than the End button.
 */
@HiltViewModel
class GoLiveViewModel @Inject constructor(
    private val api: LiveApi,
    @ApplicationContext private val appContext: Context,
) : ViewModel() {

    sealed interface Phase {
        data object Setup : Phase
        data object Connecting : Phase
        data object Live : Phase
        data class Failure(val message: String) : Phase
        data object Ended : Phase
    }

    data class UiState(
        val title: String = "",
        val phase: Phase = Phase.Setup,
        val viewerCount: Int = 0,
    ) {
        val canGoLive: Boolean get() = title.isNotBlank() && phase == Phase.Setup
    }

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    /** The LiveKit room; non-null once connecting. Owned by this VM. */
    var room: Room? = null
        private set

    private var streamId: String? = null

    fun onTitleChanged(title: String) {
        _state.value = _state.value.copy(title = title)
    }

    fun onGoLive() {
        val current = _state.value
        if (!current.canGoLive) return
        _state.value = current.copy(phase = Phase.Connecting)
        viewModelScope.launch {
            runCatching {
                val created = api.createStream(CreateStreamRequest(title = current.title.trim()))
                val id = requireNotNull(created.data?.id?.takeIf { it.isNotBlank() }) {
                    "create returned no stream id"
                }
                streamId = id
                val started = requireNotNull(api.startStream(id).data) { "start returned nothing" }

                val lkRoom = LiveKit.create(appContext)
                room = lkRoom
                lkRoom.connect(started.serverUrl, started.publisherToken)
                lkRoom.localParticipant.setCameraEnabled(true)
                lkRoom.localParticipant.setMicrophoneEnabled(true)
            }.onSuccess {
                _state.value = _state.value.copy(phase = Phase.Live)
            }.onFailure { error ->
                teardown()
                _state.value = _state.value.copy(
                    phase = Phase.Failure(error.message ?: "Couldn't go live"),
                )
            }
        }
    }

    /** The local camera track, once publishing. Null until then. */
    fun localVideoTrack(): VideoTrack? =
        room?.localParticipant?.getTrackPublication(Track.Source.CAMERA)?.track as? VideoTrack

    fun onEndStream() {
        viewModelScope.launch {
            streamId?.let { id -> runCatching { api.endStream(id) } }
            teardown()
            _state.value = _state.value.copy(phase = Phase.Ended)
        }
    }

    private fun teardown() {
        room?.disconnect()
        room = null
    }

    override fun onCleared() {
        // The end call is best-effort fire-and-forget here; the camera stop
        // (disconnect) is the part that must be synchronous with VM death.
        teardown()
    }
}
