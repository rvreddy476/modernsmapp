package com.us.android.feature.live.data

import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

/** What the live room's socket tells the watch screen. */
sealed interface LiveSocketEvent {
    /** The socket is open and the subscribe was sent. */
    data object Open : LiveSocketEvent

    /** One room frame for the stream this socket subscribed to. */
    data class Frame(val frame: LiveFrame) : LiveSocketEvent

    /**
     * The socket closed. [permanent] means do not try again on this visit:
     * no session, no socket address, or the credential was refused.
     */
    data class Closed(val permanent: Boolean) : LiveSocketEvent
}

/**
 * The realtime side of ONE live stream, behind a seam so the watch
 * ViewModel is tested on the JVM without a socket.
 *
 * The HTTP poll stays the truth (status, count, chat); the socket only adds
 * what has no HTTP read — `viewer.joined` — and makes the rest arrive
 * sooner. The flow ends when the socket closes; the caller decides whether
 * to open another.
 */
fun interface LiveRoomSocket {
    fun room(streamId: String): Flow<LiveSocketEvent>
}

/**
 * The real socket: ws-gateway's `/v1/ws/connect`, its own connection (the
 * messaging session's socket neither subscribes to live rooms nor parses
 * their frames), on the AUTHENTICATED OkHttp client so it shares the
 * app's interceptors.
 *
 * The bearer goes in the Authorization header, never in the URL: a URL is
 * written to proxy logs and crash reports. Nothing here logs a frame, a
 * header or a token.
 */
class OkHttpLiveRoomSocket(
    private val client: OkHttpClient,
    private val wsBaseUrl: String,
    private val accessToken: () -> String?,
    private val json: Json,
) : LiveRoomSocket {

    override fun room(streamId: String): Flow<LiveSocketEvent> = callbackFlow {
        val token = accessToken()
        if (token.isNullOrBlank() || wsBaseUrl.isBlank() || streamId.isBlank()) {
            trySend(LiveSocketEvent.Closed(permanent = true))
            close()
            return@callbackFlow
        }
        val request = Request.Builder()
            .url(wsBaseUrl.trimEnd('/') + CONNECT_PATH)
            .header("Authorization", "Bearer $token")
            .build()
        val socket = client.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    webSocket.send(subscribeLiveStreamFrame(streamId))
                    trySend(LiveSocketEvent.Open)
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    val frame = parseLiveFrame(json, text) ?: return
                    // The socket may carry other rooms' frames: only this stream's reach the screen.
                    if (frame.streamId == streamId) trySend(LiveSocketEvent.Frame(frame))
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    trySend(LiveSocketEvent.Closed(permanent = false))
                    close()
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    val refused = response?.code == HTTP_UNAUTHORIZED || response?.code == HTTP_FORBIDDEN
                    trySend(LiveSocketEvent.Closed(permanent = refused))
                    close()
                }
            },
        )
        awaitClose {
            socket.send(unsubscribeLiveStreamFrame(streamId))
            socket.close(NORMAL_CLOSURE, null)
        }
    }

    private companion object {
        const val CONNECT_PATH = "/v1/ws/connect"
        const val NORMAL_CLOSURE = 1000
        const val HTTP_UNAUTHORIZED = 401
        const val HTTP_FORBIDDEN = 403
    }
}

/** How long to wait before reopening a dropped room socket: 2 s, doubling, at most 30 s. */
fun liveSocketRetryMillis(attempt: Int): Long {
    if (attempt <= 0) return 0
    val exponent = (attempt - 1).coerceAtMost(MAX_RETRY_EXPONENT)
    return (BASE_RETRY_MILLIS shl exponent).coerceAtMost(MAX_RETRY_MILLIS)
}

private const val BASE_RETRY_MILLIS = 2_000L
private const val MAX_RETRY_MILLIS = 30_000L
private const val MAX_RETRY_EXPONENT = 4
