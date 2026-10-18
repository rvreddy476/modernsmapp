package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.AlertTone
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStreak
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationSettingsDto
import com.us.android.core.chat.data.DeleteMessageRequest
import com.us.android.core.chat.data.ForwardOutcome
import com.us.android.core.chat.data.ForwardResult
import com.us.android.core.chat.data.LastMessageInfo
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.StickerInfo
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.chat.data.TypingRequest
import com.us.android.core.chat.data.downsampleWaveform
import com.us.android.core.chat.data.formatVoiceDuration
import com.us.android.core.chat.data.forwardSummary
import com.us.android.core.chat.data.inboxPreview
import com.us.android.core.chat.data.mediaInfoOf
import com.us.android.core.chat.data.orderInbox
import com.us.android.core.chat.data.ownStatusLabel
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.chat.data.plainSnippet
import com.us.android.core.chat.data.presenceLabel
import com.us.android.core.chat.data.presenceOf
import com.us.android.core.chat.data.requestTitle
import com.us.android.core.chat.data.streakLabel
import com.us.android.core.chat.data.typingFrame
import com.us.android.core.chat.data.unreadPillText
import com.us.android.core.chat.data.SendMessageRequest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Test
import java.time.Instant

/** Chat parity (2026-10-18): the pure rules behind the inbox, the thread and the wire. */
class ChatParityRulesTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val now = Instant.parse("2026-10-18T12:00:00Z").toEpochMilli()

    // ── Presence and streak labels ──────────────────────────────────────

    @Test
    fun `presence reads Active now, minutes, hours, days, then nothing`() {
        assertThat(presenceLabel(true, null, now)).isEqualTo("Active now")
        assertThat(presenceLabel(false, "2026-10-18T11:59:40Z", now)).isEqualTo("Active now")
        assertThat(presenceLabel(false, "2026-10-18T11:55:00Z", now)).isEqualTo("Active 5m ago")
        assertThat(presenceLabel(null, "2026-10-18T09:00:00Z", now)).isEqualTo("Active 3h ago")
        assertThat(presenceLabel(false, "2026-10-16T12:00:00Z", now)).isEqualTo("Active 2d ago")
        assertThat(presenceLabel(false, "2026-10-01T12:00:00Z", now)).isNull()
        // Privacy denied: nothing honest to say.
        assertThat(presenceLabel(null, null, now)).isNull()
        assertThat(presenceLabel(false, "not a time", now)).isNull()
    }

    @Test
    fun `a streak shows from three days`() {
        assertThat(streakLabel(null)).isNull()
        assertThat(streakLabel(ChatStreak(2, atRisk = false, expiresAt = null))).isNull()
        assertThat(streakLabel(ChatStreak(3, atRisk = true, expiresAt = null))).isEqualTo("3")
    }

    @Test
    fun `the unread pill caps at 99+ and falls back to a dot`() {
        assertThat(unreadPillText(0, hasUnread = false)).isNull()
        assertThat(unreadPillText(0, hasUnread = true)).isEqualTo("")
        assertThat(unreadPillText(7, hasUnread = true)).isEqualTo("7")
        assertThat(unreadPillText(99, hasUnread = true)).isEqualTo("99")
        assertThat(unreadPillText(100, hasUnread = true)).isEqualTo("99+")
    }

    @Test
    fun `Seen only for the viewer's own read message`() {
        val read = LastMessageInfo("text", "hi", senderId = "me", at = "t", ownStatus = "read")
        assertThat(ownStatusLabel(read, "me")).isEqualTo("Seen")
        assertThat(ownStatusLabel(read.copy(ownStatus = "sent"), "me")).isEqualTo("Sent")
        assertThat(ownStatusLabel(read, "someone-else")).isNull()
        assertThat(ownStatusLabel(read.copy(ownStatus = null), "me")).isNull()
        assertThat(ownStatusLabel(read, "")).isNull()
    }

    // ── Inbox ordering ──────────────────────────────────────────────────

    private fun row(id: String, at: String, pinned: Boolean = false) = Conversation(
        id = id, type = "direct", title = id, isRequest = false, members = emptyList(), updatedAt = at,
        lastMessageAt = at, isPinned = pinned,
    )

    @Test
    fun `pinned first, then most recent`() {
        val ordered = orderInbox(
            listOf(
                row("old", "2026-10-01T00:00:00Z"),
                row("new", "2026-10-18T00:00:00Z"),
                row("pinned-old", "2026-09-01T00:00:00Z", pinned = true),
                row("pinned-new", "2026-10-10T00:00:00Z", pinned = true),
            ),
        )
        assertThat(ordered.map { it.id }).containsExactly("pinned-new", "pinned-old", "new", "old").inOrder()
    }

    @Test
    fun `last_message at outranks the legacy timestamp`() {
        val stale = row("a", "2026-10-01T00:00:00Z").copy(
            lastMessage = LastMessageInfo("text", "x", "u", at = "2026-10-20T00:00:00Z", ownStatus = null),
        )
        assertThat(orderInbox(listOf(row("b", "2026-10-18T00:00:00Z"), stale)).map { it.id }).containsExactly("a", "b").inOrder()
    }

    // ── Voice ───────────────────────────────────────────────────────────

    @Test
    fun `waveform downsamples to 64 bucket peaks scaled to 0-255`() {
        val samples = (0 until 640).map { if (it == 320) 16_000 else it % 10 * 100 }
        val wave = downsampleWaveform(samples)
        assertThat(wave).hasSize(64)
        assertThat(wave.max()).isEqualTo(255)
        assertThat(wave.min()).isAtLeast(0)
        // The loud sample lands in its own bucket, which is the peak.
        assertThat(wave.indexOf(255)).isEqualTo(32)
    }

    @Test
    fun `a short recording keeps one point per sample and silence stays flat`() {
        assertThat(downsampleWaveform(listOf(0, 32_767, 0))).containsExactly(0, 255, 0).inOrder()
        assertThat(downsampleWaveform(emptyList())).isEmpty()
        assertThat(downsampleWaveform(List(10) { 0 })).containsExactly(*Array(10) { 0 })
        // Out-of-range input is clamped, never wrapped.
        assertThat(downsampleWaveform(listOf(-5, 99_999)).last()).isEqualTo(255)
    }

    @Test
    fun `voice duration formats as minutes and seconds`() {
        assertThat(formatVoiceDuration(4_200)).isEqualTo("0:04")
        assertThat(formatVoiceDuration(83_000)).isEqualTo("1:23")
        assertThat(formatVoiceDuration(0)).isEqualTo("0:00")
    }

    @Test
    fun `a voice send carries only its own fields and clamps them`() {
        val typed = TypedSend.voice("media-1", durationMs = 400_000, waveform = List(80) { 300 })
        val wire = json.encodeToString(SendMessageRequest.serializer(), typed.toRequest())
        assertThat(wire).contains("\"type\":\"voice\"")
        assertThat(wire).contains("\"media_id\":\"media-1\"")
        assertThat(wire).contains("\"duration_ms\":300000")
        assertThat(typed.waveform).hasSize(64)
        assertThat(typed.waveform!!.max()).isEqualTo(255)
        assertThat(wire).doesNotContain("sticker_id")
        assertThat(wire).doesNotContain("gif")
    }

    @Test
    fun `a gif send names the provider's own url and still`() {
        val typed = TypedSend.gif(
            com.us.android.core.chat.data.GifInfo("giphy", "g1", "https://x/y.gif?a=1&b=2", null, 480, 270, "https://x/s.gif"),
        )
        val wire = json.encodeToString(SendMessageRequest.serializer(), typed.toRequest())
        assertThat(wire).contains(
            "\"gif\":{\"provider\":\"giphy\",\"id\":\"g1\",\"url\":\"https://x/y.gif?a=1&b=2\",\"width\":480,\"height\":270," +
                "\"still_url\":\"https://x/s.gif\"}",
        )
    }

    @Test
    fun `a sticker and a note reply go out with their ids`() {
        val sticker = json.encodeToString(SendMessageRequest.serializer(), TypedSend.sticker(StickerInfo("s1", "u", 1, 1)).toRequest())
        assertThat(sticker).isEqualTo("""{"type":"sticker","text":"","sticker_id":"s1"}""")
        val reply = json.encodeToString(SendMessageRequest.serializer(), TypedSend.noteReply("where to?", "n1").toRequest())
        assertThat(reply).isEqualTo("""{"type":"text","text":"where to?","reply_note_id":"n1"}""")
    }

    // ── Wire shapes that must stay byte-identical for old sends ─────────

    @Test
    fun `old bodies are unchanged and new fields are omitted when unset`() {
        assertThat(json.encodeToString(SendMessageRequest.serializer(), SendMessageRequest(text = "hi")))
            .isEqualTo("""{"type":"text","text":"hi"}""")
        assertThat(json.encodeToString(DeleteMessageRequest.serializer(), DeleteMessageRequest("b", "t")))
            .isEqualTo("""{"bucket":"b","ts":"t"}""")
        assertThat(json.encodeToString(DeleteMessageRequest.serializer(), DeleteMessageRequest("b", "t", scope = "me")))
            .isEqualTo("""{"bucket":"b","ts":"t","scope":"me"}""")
        assertThat(json.encodeToString(TypingRequest.serializer(), TypingRequest(true))).isEqualTo("""{"typing":true}""")
        assertThat(json.encodeToString(TypingRequest.serializer(), TypingRequest(false, isTyping = false)))
            .isEqualTo("""{"typing":false,"is_typing":false}""")
        assertThat(json.encodeToString(ConversationSettingsDto.serializer(), ConversationSettingsDto(isMuted = true)))
            .isEqualTo("""{"is_muted":true,"is_pinned":false}""")
        assertThat(
            json.encodeToString(
                ConversationSettingsDto.serializer(),
                ConversationSettingsDto(isMuted = false, isPinned = true, muteCalls = true, alertTone = "pop"),
            ),
        ).isEqualTo("""{"is_muted":false,"is_pinned":true,"mute_calls":true,"alert_tone":"pop"}""")
    }

    @Test
    fun `an unknown alert tone reads as Default`() {
        assertThat(AlertTone.fromWire("bell")).isEqualTo(AlertTone.BELL)
        assertThat(AlertTone.fromWire("airhorn")).isEqualTo(AlertTone.DEFAULT)
        assertThat(AlertTone.fromWire(null)).isEqualTo(AlertTone.DEFAULT)
    }

    // ── Tolerant reads ──────────────────────────────────────────────────

    @Test
    fun `media info tolerates any shape`() {
        assertThat(mediaInfoOf(null)).isNull()
        assertThat(mediaInfoOf(JsonPrimitive("a-bare-id"))).isNull()
        assertThat(mediaInfoOf(buildJsonObject { })).isNull()
        val video = mediaInfoOf(buildJsonObject { put("kind", "video"); put("duration_ms", 1200); put("sensitive", true) })
        assertThat(video?.isVideo).isTrue()
        assertThat(video?.durationMs).isEqualTo(1200)
        assertThat(video?.sensitive).isTrue()
    }

    @Test
    fun `presence reads booleans today and objects tomorrow`() {
        val today = presenceOf(buildJsonObject { put("u1", true); put("u2", false) })
        assertThat(today["u1"]?.isOnline).isTrue()
        assertThat(today["u2"]?.lastActiveAt).isNull()
        val tomorrow = presenceOf(
            buildJsonObject {
                put("u3", buildJsonObject { put("is_online", false); put("last_active_at", "2026-10-18T11:00:00Z") })
            },
        )
        assertThat(tomorrow["u3"]?.lastActiveAt).isEqualTo("2026-10-18T11:00:00Z")
        assertThat(presenceOf(JsonPrimitive("nope"))).isEmpty()
    }

    @Test
    fun `search snippets lose their highlight markup`() {
        assertThat(plainSnippet("see you at the <em>station</em>")).isEqualTo("see you at the station")
        assertThat(plainSnippet("a < b and c > d")).isEqualTo("a < b and c > d")
    }

    @Test
    fun `typed messages say what they are in the inbox`() {
        fun msg(type: String) = Message("m", "c", "u", null, "", "t", type = type)
        assertThat(inboxPreview(msg("voice"))).isEqualTo("Voice message")
        assertThat(inboxPreview(msg("sticker"))).isEqualTo("Sticker")
        assertThat(inboxPreview(msg("gif"))).isEqualTo("GIF")
        assertThat(inboxPreview(Message("m", "c", "u", null, "hello", "t", type = "voice"))).isEqualTo("hello")
    }

    // ── Forward ─────────────────────────────────────────────────────────

    @Test
    fun `forward summary words every outcome`() {
        fun r(o: ForwardOutcome) = ForwardResult("c", o, null)
        assertThat(forwardSummary(listOf(r(ForwardOutcome.SENT)))).isEqualTo("Sent")
        assertThat(forwardSummary(listOf(r(ForwardOutcome.SENT), r(ForwardOutcome.SENT)))).isEqualTo("Sent to 2 chats")
        assertThat(forwardSummary(listOf(r(ForwardOutcome.SENT), r(ForwardOutcome.BLOCKED), r(ForwardOutcome.UNAVAILABLE))))
            .isEqualTo("Sent to 1 of 3 chats. 2 couldn't receive it.")
        assertThat(forwardSummary(listOf(r(ForwardOutcome.BLOCKED)))).isEqualTo("This chat can't receive it.")
        assertThat(forwardSummary(emptyList())).isEqualTo("Couldn't forward. Try again.")
    }

    // ── Realtime frames ─────────────────────────────────────────────────

    @Test
    fun `message_deleted, reaction and flat presence frames decode`() {
        assertThat(parseChatFrame(json, """{"type":"message_deleted","payload":{"conversation_id":"c1","message_id":"m1"}}"""))
            .isEqualTo(ChatSocketEvent.MessageDeleted("c1", "m1"))
        assertThat(
            parseChatFrame(
                json,
                """{"type":"reaction","payload":{"conversation_id":"c1","message_id":"m1","user_id":"u2","emoji":"❤️","added":false}}""",
            ),
        ).isEqualTo(ChatSocketEvent.ReactionChanged("c1", "m1", "u2", "❤️", added = false))
        assertThat(parseChatFrame(json, """{"type":"presence_update","user_id":"u2","is_online":true}"""))
            .isEqualTo(ChatSocketEvent.PresenceUpdate("u2", true, null))
        assertThat(parseChatFrame(json, """{"type":"presence_update","user_id":"u2","status":"offline","last_active_at":"t"}"""))
            .isEqualTo(ChatSocketEvent.PresenceUpdate("u2", false, "t"))
        // Fail closed on identifiers, like every other frame.
        assertThat(parseChatFrame(json, """{"type":"message_deleted","payload":{"conversation_id":"c1"}}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat(parseChatFrame(json, """{"type":"reaction","payload":{"conversation_id":"c1","message_id":"m1","user_id":"u2","emoji":"x"}}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
    }

    @Test
    fun `typing goes out as typing start and stop frames`() {
        assertThat(typingFrame(json, "c1", typing = true)).isEqualTo("""{"type":"typing.start","conversation_id":"c1"}""")
        assertThat(typingFrame(json, "c1", typing = false)).isEqualTo("""{"type":"typing.stop","conversation_id":"c1"}""")
    }

    @Test
    fun `a realtime voice message keeps its typed fields`() {
        val event = parseChatFrame(
            json,
            """{"type":"message","payload":{"message_id":"m1","conversation_id":"c1","sender_id":"u2","type":"voice",""" +
                """"media_id":"a1","voice":{"duration_ms":1500,"waveform":[1,2,3]},"forwarded":true}}""",
        ) as ChatSocketEvent.MessageReceived
        assertThat(event.message.type).isEqualTo("voice")
        assertThat(event.message.voice?.waveform).containsExactly(1, 2, 3).inOrder()
        assertThat(event.message.forwarded).isTrue()
        assertThat(event.message.mediaId).isEqualTo("a1")
    }

    // ── fix pass 2: a request's name ────────────────────────────────────

    private fun request(
        sender: com.us.android.core.chat.data.RequestSender? = null,
        members: List<com.us.android.core.chat.data.ConversationMember> = emptyList(),
    ) = Conversation(
        id = "r1", type = "direct", title = null, isRequest = true, members = members, updatedAt = "t", sender = sender,
    )

    private fun sender(name: String = "", username: String = "", deleted: Boolean = false) =
        com.us.android.core.chat.data.RequestSender("u9", name, username, null, deleted)

    @Test
    fun `a request is named by its sender, never Conversation`() {
        assertThat(request(sender(name = "Chen", username = "chen")).requestTitle("me")).isEqualTo("Chen")
        assertThat(request(sender(username = "chen")).requestTitle("me")).isEqualTo("@chen")
        assertThat(request(sender(username = "@chen")).requestTitle("me")).isEqualTo("@chen")
        // Deleted wins even over a stale name.
        assertThat(request(sender(name = "Old name", deleted = true)).requestTitle("me")).isEqualTo("Deleted account")
        assertThat(request(sender(deleted = true)).requestTitle("me")).isEqualTo("Deleted account")
        // No sender block (an older server): the members, then a neutral word.
        val member = com.us.android.core.chat.data.ConversationMember("u9", "member", "Dev")
        assertThat(request(members = listOf(member)).requestTitle("me")).isEqualTo("Dev")
        assertThat(request().requestTitle("me")).isEqualTo("Someone")
        assertThat(request(sender(name = " ")).requestTitle("me")).isNotEqualTo("Conversation")
    }
}
