package com.us.android.core.call.engine

import com.us.android.core.call.data.IceServerConfig
import kotlinx.coroutines.flow.SharedFlow
import org.webrtc.EglBase
import org.webrtc.VideoTrack

/**
 * One call's media engine. A fresh instance per call — [release] is
 * terminal. The seam exists so the state machine is JVM-testable with a fake
 * while :feature:call renders the real thing.
 */
// One seam for one engine: splitting media controls from SDP handling would
// scatter a single peer connection's lifecycle across two interfaces.
@Suppress("TooManyFunctions")
interface CallEngine {

    /** Engine → state machine events. */
    val events: SharedFlow<CallEngineEvent>

    /**
     * Builds the peer connection, capturers and local tracks. [video] decides
     * whether a camera is opened at all — an audio call never touches it.
     */
    fun start(video: Boolean, iceServers: List<IceServerConfig>)

    /** Caller side: local offer SDP (local description is set). */
    suspend fun createOffer(): String

    /** Callee side: applies the remote offer, returns the answer SDP. */
    suspend fun acceptOffer(remoteSdp: String): String

    /** Caller side: applies the callee's answer. */
    suspend fun completeWithAnswer(remoteSdp: String)

    fun addRemoteIceCandidate(candidate: String, sdpMid: String, sdpMLineIndex: Int)

    fun setMuted(muted: Boolean)
    fun setVideoEnabled(enabled: Boolean)
    fun switchCamera()

    /** Rendering hooks for :feature:call; null until available / for audio. */
    fun localVideoTrack(): VideoTrack?
    fun remoteVideoTrack(): VideoTrack?
    fun eglContext(): EglBase.Context?

    fun release()
}

sealed interface CallEngineEvent {
    /** A locally gathered ICE candidate to trickle to the peer. */
    data class LocalIceCandidate(
        val candidate: String,
        val sdpMid: String,
        val sdpMLineIndex: Int,
    ) : CallEngineEvent

    /** Media is flowing. */
    data object Connected : CallEngineEvent

    /** The transport dropped and did not recover. */
    data object Failed : CallEngineEvent

    /** The remote video track arrived (or went away, null). */
    data class RemoteVideo(val available: Boolean) : CallEngineEvent
}

/** Fresh engine per call. */
fun interface CallEngineFactory {
    fun create(): CallEngine
}
