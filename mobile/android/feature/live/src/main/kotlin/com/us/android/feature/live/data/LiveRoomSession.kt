package com.us.android.feature.live.data

import android.content.Context
import io.livekit.android.LiveKit
import io.livekit.android.events.RoomEvent
import io.livekit.android.events.collect
import io.livekit.android.room.Room
import io.livekit.android.room.track.Track
import io.livekit.android.room.track.VideoTrack
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow

/** What the LiveKit room tells a screen, reduced to what a screen acts on. */
sealed interface LiveRoomSignal {
    /** A remote video track is subscribed: the broadcast is drawable. */
    data object VideoReady : LiveRoomSignal

    /** The remote video track went away (the host dropped or unpublished). */
    data object VideoGone : LiveRoomSignal

    /** THIS device's connection to the room ended. Not a statement about the stream. */
    data object Disconnected : LiveRoomSignal
}

/**
 * One LiveKit room, behind a seam so the ViewModels can be tested on the
 * JVM without WebRTC. The room is a transport only: whether the stream is
 * live is the server's answer (`GET …/streams/:id`), never this object's.
 */
interface LiveRoomSession {
    /** For rendering only (the renderer is initialised through it). Null in tests. */
    val room: Room?

    val signals: Flow<LiveRoomSignal>

    suspend fun connect(serverUrl: String, token: String)

    suspend fun publishCameraAndMicrophone()

    fun localVideo(): VideoTrack?

    fun remoteVideo(): VideoTrack?

    fun disconnect()
}

fun interface LiveRoomFactory {
    fun create(): LiveRoomSession
}

/** The real room, from the LiveKit Android SDK. */
class LiveKitRoomSession(context: Context) : LiveRoomSession {

    override val room: Room = LiveKit.create(context)

    @Volatile
    private var remote: VideoTrack? = null

    override val signals: Flow<LiveRoomSignal> = flow {
        room.events.collect { event ->
            when (event) {
                is RoomEvent.TrackSubscribed -> (event.track as? VideoTrack)?.let { track ->
                    remote = track
                    emit(LiveRoomSignal.VideoReady)
                }
                is RoomEvent.TrackUnsubscribed -> if (event.track == remote) {
                    remote = null
                    emit(LiveRoomSignal.VideoGone)
                }
                is RoomEvent.Disconnected -> emit(LiveRoomSignal.Disconnected)
                else -> Unit
            }
        }
    }

    override suspend fun connect(serverUrl: String, token: String) {
        room.connect(serverUrl, token)
    }

    override suspend fun publishCameraAndMicrophone() {
        room.localParticipant.setCameraEnabled(true)
        room.localParticipant.setMicrophoneEnabled(true)
    }

    override fun localVideo(): VideoTrack? =
        room.localParticipant.getTrackPublication(Track.Source.CAMERA)?.track as? VideoTrack

    override fun remoteVideo(): VideoTrack? = remote

    override fun disconnect() {
        room.disconnect()
    }
}
