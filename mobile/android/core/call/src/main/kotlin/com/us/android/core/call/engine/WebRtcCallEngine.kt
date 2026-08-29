package com.us.android.core.call.engine

import android.content.Context
import com.us.android.core.call.data.IceServerConfig
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import org.webrtc.AudioSource
import org.webrtc.AudioTrack
import org.webrtc.Camera2Enumerator
import org.webrtc.CameraVideoCapturer
import org.webrtc.DefaultVideoDecoderFactory
import org.webrtc.DefaultVideoEncoderFactory
import org.webrtc.EglBase
import org.webrtc.IceCandidate
import org.webrtc.MediaConstraints
import org.webrtc.MediaStream
import org.webrtc.PeerConnection
import org.webrtc.PeerConnectionFactory
import org.webrtc.RtpReceiver
import org.webrtc.SdpObserver
import org.webrtc.SessionDescription
import org.webrtc.SurfaceTextureHelper
import org.webrtc.VideoSource
import org.webrtc.VideoTrack
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * The real libwebrtc engine for 1:1 direct calls.
 *
 * Unit tests never construct this (the state machine takes [CallEngineFactory]
 * and fakes it); correctness here is proven on a device. Threading: libwebrtc
 * invokes observers on its signaling thread — every callback only posts into
 * [events] or mutates libwebrtc's own objects, which are thread-safe for
 * these operations.
 */
@Suppress("TooManyFunctions")
class WebRtcCallEngine(private val appContext: Context) : CallEngine {

    private val _events = MutableSharedFlow<CallEngineEvent>(extraBufferCapacity = 64)
    override val events: SharedFlow<CallEngineEvent> = _events.asSharedFlow()

    private var eglBase: EglBase? = null
    private var factory: PeerConnectionFactory? = null
    private var peerConnection: PeerConnection? = null
    private var audioSource: AudioSource? = null
    private var audioTrack: AudioTrack? = null
    private var videoSource: VideoSource? = null
    private var videoTrack: VideoTrack? = null
    private var remoteVideo: VideoTrack? = null
    private var capturer: CameraVideoCapturer? = null
    private var textureHelper: SurfaceTextureHelper? = null

    @Volatile
    private var released = false

    override fun start(video: Boolean, iceServers: List<IceServerConfig>) {
        PeerConnectionFactory.initialize(
            PeerConnectionFactory.InitializationOptions.builder(appContext)
                .createInitializationOptions(),
        )
        val egl = EglBase.create().also { eglBase = it }
        val builtFactory = PeerConnectionFactory.builder()
            .setVideoEncoderFactory(DefaultVideoEncoderFactory(egl.eglBaseContext, true, true))
            .setVideoDecoderFactory(DefaultVideoDecoderFactory(egl.eglBaseContext))
            .createPeerConnectionFactory()
        factory = builtFactory

        val rtcConfig = PeerConnection.RTCConfiguration(
            iceServers.map { server ->
                PeerConnection.IceServer.builder(server.urls)
                    .apply {
                        val user = server.username
                        val credential = server.credential
                        if (!user.isNullOrBlank() && !credential.isNullOrBlank()) {
                            setUsername(user)
                            setPassword(credential)
                        }
                    }
                    .createIceServer()
            },
        ).apply {
            sdpSemantics = PeerConnection.SdpSemantics.UNIFIED_PLAN
        }

        peerConnection = builtFactory.createPeerConnection(rtcConfig, observer)
            ?: error("peer connection creation failed")

        audioSource = builtFactory.createAudioSource(MediaConstraints())
        audioTrack = builtFactory.createAudioTrack("audio0", audioSource).also {
            peerConnection?.addTrack(it, listOf(STREAM_ID))
        }

        if (video) {
            val enumerator = Camera2Enumerator(appContext)
            val deviceName = enumerator.deviceNames.firstOrNull { enumerator.isFrontFacing(it) }
                ?: enumerator.deviceNames.firstOrNull()
            if (deviceName != null) {
                val videoCapturer = enumerator.createCapturer(deviceName, null)
                capturer = videoCapturer
                val helper = SurfaceTextureHelper.create("call-capture", egl.eglBaseContext)
                textureHelper = helper
                val source = builtFactory.createVideoSource(videoCapturer.isScreencast)
                videoSource = source
                videoCapturer.initialize(helper, appContext, source.capturerObserver)
                videoCapturer.startCapture(CAPTURE_WIDTH, CAPTURE_HEIGHT, CAPTURE_FPS)
                videoTrack = builtFactory.createVideoTrack("video0", source).also {
                    peerConnection?.addTrack(it, listOf(STREAM_ID))
                }
            }
        }
    }

    override suspend fun createOffer(): String {
        val pc = peerConnection ?: error("engine not started")
        val offer = pc.awaitCreate(offer = true)
        pc.awaitSet(local = true, sdp = offer)
        return offer.description
    }

    override suspend fun acceptOffer(remoteSdp: String): String {
        val pc = peerConnection ?: error("engine not started")
        pc.awaitSet(local = false, sdp = SessionDescription(SessionDescription.Type.OFFER, remoteSdp))
        val answer = pc.awaitCreate(offer = false)
        pc.awaitSet(local = true, sdp = answer)
        return answer.description
    }

    override suspend fun completeWithAnswer(remoteSdp: String) {
        val pc = peerConnection ?: error("engine not started")
        pc.awaitSet(local = false, sdp = SessionDescription(SessionDescription.Type.ANSWER, remoteSdp))
    }

    override fun addRemoteIceCandidate(candidate: String, sdpMid: String, sdpMLineIndex: Int) {
        peerConnection?.addIceCandidate(IceCandidate(sdpMid, sdpMLineIndex, candidate))
    }

    override fun setMuted(muted: Boolean) {
        audioTrack?.setEnabled(!muted)
    }

    override fun setVideoEnabled(enabled: Boolean) {
        videoTrack?.setEnabled(enabled)
    }

    override fun switchCamera() {
        capturer?.switchCamera(null)
    }

    override fun localVideoTrack(): VideoTrack? = videoTrack

    override fun remoteVideoTrack(): VideoTrack? = remoteVideo

    override fun eglContext(): EglBase.Context? = eglBase?.eglBaseContext

    override fun release() {
        if (released) return
        released = true
        runCatching { capturer?.stopCapture() }
        runCatching { capturer?.dispose() }
        runCatching { textureHelper?.dispose() }
        runCatching { peerConnection?.close() }
        runCatching { peerConnection?.dispose() }
        runCatching { audioSource?.dispose() }
        runCatching { videoSource?.dispose() }
        runCatching { factory?.dispose() }
        runCatching { eglBase?.release() }
        peerConnection = null
        factory = null
        eglBase = null
    }

    private val observer = object : PeerConnection.Observer {
        override fun onIceCandidate(candidate: IceCandidate?) {
            candidate ?: return
            _events.tryEmit(
                CallEngineEvent.LocalIceCandidate(
                    candidate = candidate.sdp,
                    sdpMid = candidate.sdpMid.orEmpty(),
                    sdpMLineIndex = candidate.sdpMLineIndex,
                ),
            )
        }

        override fun onConnectionChange(newState: PeerConnection.PeerConnectionState?) {
            when (newState) {
                PeerConnection.PeerConnectionState.CONNECTED ->
                    _events.tryEmit(CallEngineEvent.Connected)
                PeerConnection.PeerConnectionState.FAILED,
                PeerConnection.PeerConnectionState.CLOSED,
                -> if (!released) _events.tryEmit(CallEngineEvent.Failed)
                else -> Unit
            }
        }

        override fun onAddTrack(receiver: RtpReceiver?, streams: Array<out MediaStream>?) {
            val track = receiver?.track()
            if (track is VideoTrack) {
                remoteVideo = track
                _events.tryEmit(CallEngineEvent.RemoteVideo(available = true))
            }
        }

        override fun onRemoveTrack(receiver: RtpReceiver?) {
            if (receiver?.track() is VideoTrack) {
                remoteVideo = null
                _events.tryEmit(CallEngineEvent.RemoteVideo(available = false))
            }
        }

        override fun onSignalingChange(state: PeerConnection.SignalingState?) = Unit
        override fun onIceConnectionChange(state: PeerConnection.IceConnectionState?) = Unit
        override fun onIceConnectionReceivingChange(receiving: Boolean) = Unit
        override fun onIceGatheringChange(state: PeerConnection.IceGatheringState?) = Unit
        override fun onIceCandidatesRemoved(candidates: Array<out IceCandidate>?) = Unit
        override fun onAddStream(stream: MediaStream?) = Unit
        override fun onRemoveStream(stream: MediaStream?) = Unit
        override fun onDataChannel(channel: org.webrtc.DataChannel?) = Unit
        override fun onRenegotiationNeeded() = Unit
    }

    private suspend fun PeerConnection.awaitCreate(offer: Boolean): SessionDescription =
        suspendCancellableCoroutine { continuation ->
            val observer = object : SdpObserverAdapter() {
                override fun onCreateSuccess(sdp: SessionDescription?) {
                    if (sdp == null) {
                        continuation.resumeWithException(IllegalStateException("null sdp"))
                    } else {
                        continuation.resume(sdp)
                    }
                }

                override fun onCreateFailure(error: String?) {
                    continuation.resumeWithException(IllegalStateException(error ?: "sdp create failed"))
                }
            }
            if (offer) createOffer(observer, MediaConstraints()) else createAnswer(observer, MediaConstraints())
        }

    private suspend fun PeerConnection.awaitSet(local: Boolean, sdp: SessionDescription) =
        suspendCancellableCoroutine { continuation ->
            val observer = object : SdpObserverAdapter() {
                override fun onSetSuccess() {
                    continuation.resume(Unit)
                }

                override fun onSetFailure(error: String?) {
                    continuation.resumeWithException(IllegalStateException(error ?: "sdp set failed"))
                }
            }
            if (local) setLocalDescription(observer, sdp) else setRemoteDescription(observer, sdp)
        }

    private companion object {
        const val STREAM_ID = "call-stream"
        const val CAPTURE_WIDTH = 1280
        const val CAPTURE_HEIGHT = 720
        const val CAPTURE_FPS = 30
    }
}

/** SdpObserver with every member defaulted; override what each await needs. */
private abstract class SdpObserverAdapter : SdpObserver {
    override fun onCreateSuccess(sdp: SessionDescription?) = Unit
    override fun onSetSuccess() = Unit
    override fun onCreateFailure(error: String?) = Unit
    override fun onSetFailure(error: String?) = Unit
}
