package com.us.android.feature.live.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.live.data.BanUserRequest
import com.us.android.feature.live.data.CreateStreamRequest
import com.us.android.feature.live.data.EndStreamDto
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveEligibilityDto
import com.us.android.feature.live.data.LiveReportRequest
import com.us.android.feature.live.data.LiveRoomFactory
import com.us.android.feature.live.data.LiveRoomSession
import com.us.android.feature.live.data.LiveRoomSignal
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.SendChatRequest
import com.us.android.feature.live.data.SetModeratorsRequest
import com.us.android.feature.live.data.StartStreamDto
import com.us.android.feature.live.data.ViewerTokenDto
import io.livekit.android.room.Room
import io.livekit.android.room.track.VideoTrack
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.test.TestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.HttpException
import retrofit2.Response

/** An HTTP failure exactly as Retrofit throws it, with the platform's error envelope. */
fun httpError(status: Int, code: String, details: String? = null): HttpException = HttpException(
    Response.error<Any>(
        status,
        ("""{"error":{"code":"$code","message":"m"""" + (details?.let { ""","details":$it""" } ?: "") + "}}")
            .toResponseBody("application/json".toMediaType()),
    ),
)

/**
 * A hand-written [LiveApi]. Each route answers from a mutable field (or
 * throws [failures] for it), and every call is recorded as "VERB path".
 */
class FakeLiveApi : LiveApi {
    val calls = mutableListOf<String>()
    val failures = mutableMapOf<String, Exception>()

    var created = LiveStreamDto(id = "s1", creatorUserId = "host", status = "scheduled")
    var started = StartStreamDto(
        stream = LiveStreamDto(id = "s1", creatorUserId = "host", status = "starting"),
        publisherToken = "pub-token",
        serverUrl = "wss://lk.test",
    )
    var stream = LiveStreamDto(id = "s1", creatorUserId = "host", title = "Hello", status = "live")
    var chat: List<LiveChatMessageDto> = emptyList()
    var sentReply = LiveChatMessageDto(id = "mine", userId = "me", text = "hi")

    /** Eligible unless a test says otherwise, so the host tests reach the form. */
    var eligibility = LiveEligibilityDto(mode = "open", eligible = true)
    val sentChat = mutableListOf<String>()

    val bans = mutableListOf<BanUserRequest>()
    val moderatorPuts = mutableListOf<List<String>>()
    val reports = mutableListOf<LiveReportRequest>()

    private fun record(call: String) {
        calls += call
        failures[call]?.let { throw it }
    }

    fun count(call: String): Int = calls.count { it == call }

    override suspend fun eligibility(): ApiEnvelope<LiveEligibilityDto> {
        record("GET eligibility")
        return ApiEnvelope(data = eligibility)
    }

    override suspend fun createStream(body: CreateStreamRequest): ApiEnvelope<LiveStreamDto> {
        record("POST create")
        return ApiEnvelope(data = created)
    }

    override suspend fun startStream(id: String): ApiEnvelope<StartStreamDto> {
        record("POST start")
        return ApiEnvelope(data = started)
    }

    override suspend fun endStream(id: String): ApiEnvelope<EndStreamDto> {
        record("POST end")
        return ApiEnvelope(data = EndStreamDto(status = "ended"))
    }

    override suspend fun listLiveNow(limit: Int): ApiEnvelope<List<LiveStreamDto>> {
        record("GET list")
        return ApiEnvelope(data = listOf(stream))
    }

    override suspend fun getStream(id: String): ApiEnvelope<LiveStreamDto> {
        record("GET stream")
        return ApiEnvelope(data = stream)
    }

    override suspend fun viewerToken(id: String): ApiEnvelope<ViewerTokenDto> {
        record("GET token")
        return ApiEnvelope(data = ViewerTokenDto(token = "view-token", serverUrl = "wss://lk.test"))
    }

    override suspend fun sendChat(id: String, body: SendChatRequest): ApiEnvelope<LiveChatMessageDto> {
        record("POST chat")
        sentChat += body.text
        return ApiEnvelope(data = sentReply.copy(text = body.text))
    }

    override suspend fun listChat(id: String, limit: Int): ApiEnvelope<List<LiveChatMessageDto>> {
        record("GET chat")
        return ApiEnvelope(data = chat)
    }

    override suspend fun removeChatMessage(id: String, messageId: String) {
        record("DELETE chat/$messageId")
    }

    override suspend fun banUser(id: String, body: BanUserRequest) {
        record("POST bans")
        bans += body
    }

    override suspend fun unbanUser(id: String, userId: String) {
        record("DELETE bans/$userId")
    }

    override suspend fun setModerators(id: String, body: SetModeratorsRequest) {
        record("PUT moderators")
        moderatorPuts += body.userIds
    }

    override suspend fun report(id: String, body: LiveReportRequest) {
        record("POST reports")
        reports += body
    }
}

/** A room that connects (or refuses to) without WebRTC. */
class FakeRoomSession(private val failConnect: Boolean = false) : LiveRoomSession {
    val signalFlow = MutableSharedFlow<LiveRoomSignal>(extraBufferCapacity = 8)
    var connectedWith: Pair<String, String>? = null
    var published = false
    var disconnected = false

    override val room: Room? = null
    override val signals = signalFlow

    override suspend fun connect(serverUrl: String, token: String) {
        check(!failConnect) { "no route to LiveKit" }
        connectedWith = serverUrl to token
    }

    override suspend fun publishCameraAndMicrophone() {
        published = true
    }

    override fun localVideo(): VideoTrack? = null

    override fun remoteVideo(): VideoTrack? = null

    override fun disconnect() {
        disconnected = true
    }
}

/** Hands out [FakeRoomSession]s and keeps them for inspection. */
class FakeRoomFactory(private val failConnect: Boolean = false) : LiveRoomFactory {
    val sessions = mutableListOf<FakeRoomSession>()

    override fun create(): LiveRoomSession = FakeRoomSession(failConnect).also { sessions += it }
}

/**
 * Holds a test's ViewModels so it can clear them — cancelling their
 * viewModelScope and its endless poll loop — BEFORE runTest drains the
 * scheduler. Without it the drain chases the poll's delay forever.
 */
class HeldViewModels {
    val store = ViewModelStore()
    var count = 0

    inline fun <reified T : ViewModel> hold(noinline build: () -> T): T =
        ViewModelProvider.create(store, viewModelFactory { initializer { build() } })["vm${count++}", T::class]

    fun clear() = store.clear()
}

/** runTest that always clears [vms] first, pass or fail. */
fun liveTest(
    dispatcher: TestDispatcher,
    vms: HeldViewModels,
    body: suspend TestScope.() -> Unit,
) = runTest(dispatcher) {
    try {
        body()
    } finally {
        vms.clear()
    }
}
