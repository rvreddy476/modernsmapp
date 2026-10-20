package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.AlertTone
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatParityApi
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ConversationPrefs
import com.us.android.core.chat.data.DisappearTimer
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.disappearingNote
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Disappearing messages (20 Oct 2026): the settings round trip on
 * `/v1/chat/conversations/:id/settings` (`disappear_after_ms`, Off sent as 0),
 * the timer's wire values, and an expired message leaving an open thread.
 */
class DisappearingMessagesTest {

    /** NetworkModule.provideJson, exactly. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var parity: ChatParityRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChatParityApi::class.java)
        parity = ChatParityRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun respond(body: String, code: Int = 200) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    private fun settings(disappear: String?) =
        """{"data":{"conversation_id":"c1","user_id":"me","is_muted":false,"read_receipts_enabled":true,""" +
            """"theme":"default","is_pinned":false,"mute_calls":false,"alert_tone":"default"""" +
            (disappear?.let { ""","disappear_after_ms":$it""" } ?: "") + "}}"

    private fun sentBody() = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject

    // ── The round trip ──────────────────────────────────────────────────

    @Test
    fun `the settings read carries the timer and the thread map learns it`() = runTest {
        respond(settings("604800000"))

        val prefs = (parity.prefs("c1") as AppResult.Success).data

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.url.encodedPath).isEqualTo("/v1/chat/conversations/c1/settings")
        assertThat(prefs.disappearing).isEqualTo(DisappearTimer.WEEK)
        assertThat(parity.disappearing.value["c1"]).isEqualTo(DisappearTimer.WEEK)
    }

    @Test
    fun `absent, null and zero all read as off`() = runTest {
        respond(settings(null))
        respond(settings("null"))
        respond(settings("0"))

        repeat(3) {
            assertThat((parity.prefs("c1") as AppResult.Success).data.disappearing).isEqualTo(DisappearTimer.OFF)
        }
    }

    @Test
    fun `setting a timer PUTs only disappear_after_ms and keeps the echo`() = runTest {
        respond(settings("86400000"))

        val result = parity.setDisappearing("c1", DisappearTimer.DAY)

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PUT")
        assertThat(request.url.encodedPath).isEqualTo("/v1/chat/conversations/c1/settings")
        // Nothing else rides along: a timer change never rewrites mute or pin.
        assertThat(json.parseToJsonElement(request.body!!.utf8()))
            .isEqualTo(json.parseToJsonElement("""{"disappear_after_ms":86400000}"""))
        assertThat((result as AppResult.Success).data).isEqualTo(DisappearTimer.DAY)
        assertThat(parity.disappearing.value["c1"]).isEqualTo(DisappearTimer.DAY)
    }

    @Test
    fun `Off is sent as 0, never omitted or null`() = runTest {
        respond(settings(null))

        val result = parity.setDisappearing("c1", DisappearTimer.OFF)

        assertThat(sentBody()).isEqualTo(json.parseToJsonElement("""{"disappear_after_ms":0}"""))
        assertThat((result as AppResult.Success).data).isEqualTo(DisappearTimer.OFF)
    }

    @Test
    fun `ninety days is the long wire value`() = runTest {
        respond(settings("7776000000"))

        parity.setDisappearing("c1", DisappearTimer.NINETY_DAYS)

        assertThat(sentBody()).isEqualTo(json.parseToJsonElement("""{"disappear_after_ms":7776000000}"""))
    }

    @Test
    fun `a refused change leaves the known timer alone`() = runTest {
        respond(settings("604800000"))
        parity.prefs("c1")
        server.takeRequest()
        respond("""{"error":{"code":"INVALID_REQUEST","message":"no"}}""", code = 400)

        val result = parity.setDisappearing("c1", DisappearTimer.DAY)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(parity.disappearing.value["c1"]).isEqualTo(DisappearTimer.WEEK)
    }

    @Test
    fun `saving the other switches never carries the timer and keeps what the page knew`() = runTest {
        respond(settings(null))

        val saved = parity.savePrefs(
            "c1",
            ConversationPrefs(
                isMuted = true,
                isPinned = false,
                muteCalls = false,
                alertTone = AlertTone.DEFAULT,
                disappearing = DisappearTimer.WEEK,
            ),
        )

        assertThat(sentBody().containsKey("disappear_after_ms")).isFalse()
        assertThat((saved as AppResult.Success).data.disappearing).isEqualTo(DisappearTimer.WEEK)
    }

    // ── The timer's rules ───────────────────────────────────────────────

    @Test
    fun `the four timers carry the server's allowed values`() {
        assertThat(DisappearTimer.entries.map { it.millis })
            .containsExactly(0L, 86_400_000L, 604_800_000L, 7_776_000_000L).inOrder()
        assertThat(DisappearTimer.entries.map { it.label })
            .containsExactly("Off", "24 hours", "7 days", "90 days").inOrder()
        DisappearTimer.entries.forEach { assertThat(DisappearTimer.fromMillis(it.millis)).isEqualTo(it) }
    }

    @Test
    fun `an unexpected value that is on is never shown as off`() {
        assertThat(DisappearTimer.fromMillis(3_600_000L)).isEqualTo(DisappearTimer.DAY)
        assertThat(DisappearTimer.fromMillis(Long.MAX_VALUE)).isEqualTo(DisappearTimer.NINETY_DAYS)
        assertThat(DisappearTimer.fromMillis(-5L)).isEqualTo(DisappearTimer.OFF)
    }

    @Test
    fun `the thread note shows only while the timer is on`() {
        assertThat(disappearingNote(null)).isNull()
        assertThat(disappearingNote(DisappearTimer.OFF)).isNull()
        assertThat(disappearingNote(DisappearTimer.DAY)).contains("24 hours")
    }

    // ── An expired message in an open thread ────────────────────────────

    @Test
    fun `message_deleted with reason expired decodes as an expiry, a plain one as an unsend`() {
        assertThat(
            parseChatFrame(
                json,
                """{"type":"message_deleted","payload":{"conversation_id":"c1","message_id":"m1","reason":"expired"}}""",
            ),
        ).isEqualTo(ChatSocketEvent.MessageDeleted("c1", "m1", expired = true))
        assertThat(
            parseChatFrame(json, """{"type":"message_deleted","payload":{"conversation_id":"c1","message_id":"m1"}}"""),
        ).isEqualTo(ChatSocketEvent.MessageDeleted("c1", "m1", expired = false))
    }

    @Test
    fun `the shared timer frame decodes, and fails closed without its fields`() {
        assertThat(
            parseChatFrame(
                json,
                """{"type":"disappearing_timer_updated","payload":{"conversation_id":"c1",""" +
                    """"disappear_after_ms":604800000,"updated_by":"u2","updated_at":"2026-10-20T10:00:00Z"}}""",
            ),
        ).isEqualTo(ChatSocketEvent.DisappearingTimerUpdated("c1", 604_800_000L, "u2"))
        assertThat(
            parseChatFrame(json, """{"type":"disappearing_timer_updated","payload":{"conversation_id":"c1"}}"""),
        ).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat(
            parseChatFrame(json, """{"type":"disappearing_timer_updated","payload":{"disappear_after_ms":0}}"""),
        ).isInstanceOf(ChatSocketEvent.Malformed::class.java)
    }

    @Test
    fun `a remote timer change reaches the shared map, off included`() {
        parity.applyRemoteDisappearing("c1", 86_400_000L)
        assertThat(parity.disappearing.value["c1"]).isEqualTo(DisappearTimer.DAY)

        parity.applyRemoteDisappearing("c1", 0L)
        assertThat(parity.disappearing.value["c1"]).isEqualTo(DisappearTimer.OFF)
    }

    @Test
    fun `an open thread ignores the timer frame itself`() {
        val thread = thread(msg("m1"))
        assertThat(thread.onSocketEvent(ChatSocketEvent.DisappearingTimerUpdated("c1", 86_400_000L))).isNull()
    }

    private val chatApi = java.lang.reflect.Proxy.newProxyInstance(
        ChatApi::class.java.classLoader,
        arrayOf(ChatApi::class.java),
    ) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

    private fun thread(vararg messages: Message): ThreadController {
        val thread = ThreadController("c1", ChatRepository(chatApi, ErrorMapper(json)), "me")
        messages.reversed().forEach { thread.onRealtimeMessage(it) }
        return thread
    }

    private fun msg(id: String) = Message(id, "c1", "u2", null, "hi $id", "2026-10-20T10:00:00Z")

    @Test
    fun `an expired message leaves the thread with no placeholder`() {
        val thread = thread(msg("m1"), msg("m2"))

        val next = thread.onSocketEvent(ChatSocketEvent.MessageDeleted("c1", "m1", expired = true))

        assertThat(next!!.messages.map { it.id }).containsExactly("m2")
        // Twice is once; another conversation's expiry is not ours.
        assertThat(thread.onSocketEvent(ChatSocketEvent.MessageDeleted("c1", "m1", expired = true))).isNull()
        assertThat(thread.onSocketEvent(ChatSocketEvent.MessageDeleted("other", "m2", expired = true))).isNull()
    }

    @Test
    fun `a plain unsend still keeps its placeholder`() {
        val thread = thread(msg("m1"))

        val next = thread.onSocketEvent(ChatSocketEvent.MessageDeleted("c1", "m1"))

        assertThat(next!!.messages.single().deleted).isTrue()
    }
}
