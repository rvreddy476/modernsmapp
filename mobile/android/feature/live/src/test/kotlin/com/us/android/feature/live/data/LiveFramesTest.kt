package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The room's realtime frames as live-service-v2 publishes them
 * (internal/service/realtime.go, joins.go): the payload's fields at the top
 * level AND under "payload". Either shape decodes the same; a frame missing
 * what it needs is ignored, never repaired.
 */
class LiveFramesTest {

    private val json = NetworkModule.provideJson()

    private fun parse(text: String) = parseLiveFrame(json, text)

    // ── viewer.joined ──────────────────────────────────────────────────

    @Test
    fun `viewer joined reads the top-level shape`() {
        val frame = parse(
            """
            {"type":"viewer.joined","stream_id":"s1","at":"2026-10-14T10:00:01Z",
             "user_id":"u2",
             "user":{"user_id":"u2","name":"Asha Rao","handle":"asha","badges":[],"role":"viewer"},
             "joined_at":"2026-10-14T10:00:00Z",
             "payload":{"stream_id":"s1","user_id":"u2","user":{"user_id":"u2","name":"Asha Rao"},"joined_at":"2026-10-14T10:00:00Z"}}
            """.trimIndent(),
        )

        assertThat(frame).isEqualTo(
            LiveFrame.Joined(
                streamId = "s1",
                userId = "u2",
                author = LiveChatAuthorDto(userId = "u2", name = "Asha Rao", handle = "asha", role = "viewer"),
                joinedAt = "2026-10-14T10:00:00Z",
            ),
        )
    }

    @Test
    fun `viewer joined reads the payload-only shape`() {
        val frame = parse(
            """
            {"type":"viewer.joined",
             "payload":{"stream_id":"s1","user_id":"u3","user":{"name":"Ravi"},"joined_at":"2026-10-14T10:00:02Z"}}
            """.trimIndent(),
        ) as LiveFrame.Joined

        assertThat(frame.streamId).isEqualTo("s1")
        assertThat(frame.userId).isEqualTo("u3")
        // The card's own id is missing: the frame's user_id stands in.
        assertThat(frame.author?.userId).isEqualTo("u3")
        assertThat(frame.author?.name).isEqualTo("Ravi")
        assertThat(frame.joinedAt).isEqualTo("2026-10-14T10:00:02Z")
    }

    @Test
    fun `the top level wins over the payload`() {
        val frame = parse(
            """{"type":"viewer.joined","stream_id":"s1","user_id":"top","payload":{"stream_id":"s1","user_id":"nested"}}""",
        ) as LiveFrame.Joined

        assertThat(frame.userId).isEqualTo("top")
    }

    @Test
    fun `a join without a user card has no author, and without joined_at falls back to at`() {
        val frame = parse(
            """{"type":"viewer.joined","stream_id":"s1","user_id":"u2","at":"2026-10-14T10:00:05Z"}""",
        ) as LiveFrame.Joined

        assertThat(frame.author).isNull()
        assertThat(frame.joinedAt).isEqualTo("2026-10-14T10:00:05Z")
    }

    @Test
    fun `a join missing its user or its stream is ignored`() {
        assertThat(parse("""{"type":"viewer.joined","stream_id":"s1","user":{"name":"A"}}""")).isNull()
        assertThat(parse("""{"type":"viewer.joined","stream_id":"s1","user_id":"  "}""")).isNull()
        assertThat(parse("""{"type":"viewer.joined","user_id":"u2"}""")).isNull()
        assertThat(parse("""{"type":"viewer.joined","stream_id":"s1","user_id":null}""")).isNull()
        assertThat(parse("""{"type":"viewer.joined","stream_id":"s1","user_id":42}""")).isNull()
    }

    // ── chat and moderation ────────────────────────────────────────────

    @Test
    fun `a chat message is the chat row, from either shape`() {
        val top = parse(
            """
            {"type":"chat.message","stream_id":"s1","id":"m1","user_id":"u2","text":" hi ","is_pinned":false,
             "created_at":"2026-10-14T10:00:00Z","author":{"user_id":"u2","handle":"kiran","role":"moderator"}}
            """.trimIndent(),
        ) as LiveFrame.Chat
        val nested = parse(
            """
            {"type":"chat.message","payload":{"stream_id":"s1","id":"m1","user_id":"u2","text":" hi ",
             "created_at":"2026-10-14T10:00:00Z","author":{"user_id":"u2","handle":"kiran","role":"moderator"}}}
            """.trimIndent(),
        ) as LiveFrame.Chat

        assertThat(top.message.id).isEqualTo("m1")
        assertThat(top.message.text).isEqualTo(" hi ")
        assertThat(top.message.author?.role).isEqualTo("moderator")
        assertThat(nested).isEqualTo(top)
    }

    @Test
    fun `a chat message without an id or an author id is ignored`() {
        assertThat(parse("""{"type":"chat.message","stream_id":"s1","user_id":"u2","text":"x"}""")).isNull()
        assertThat(parse("""{"type":"chat.message","stream_id":"s1","id":"m1","text":"x"}""")).isNull()
    }

    @Test
    fun `mute, unmute, pin, unpin, ban and removal read their ids`() {
        assertThat(parse("""{"type":"moderation.mute","stream_id":"s1","user_id":"me","by_role":"host"}"""))
            .isEqualTo(LiveFrame.Muted("s1", "me"))
        assertThat(parse("""{"type":"moderation.unmute","payload":{"stream_id":"s1","user_id":"me"}}"""))
            .isEqualTo(LiveFrame.Unmuted("s1", "me"))
        assertThat(parse("""{"type":"moderation.pin","stream_id":"s1","message_id":"m1","user_id":"u2","text":"Rules"}"""))
            .isEqualTo(LiveFrame.Pinned("s1", "m1", "u2", "Rules"))
        assertThat(parse("""{"type":"moderation.unpin","stream_id":"s1","message_id":"m1"}"""))
            .isEqualTo(LiveFrame.Unpinned("s1", "m1"))
        assertThat(parse("""{"type":"moderation.ban","stream_id":"s1","user_id":"u9"}"""))
            .isEqualTo(LiveFrame.Banned("s1", "u9"))
        assertThat(parse("""{"type":"chat.removed","stream_id":"s1","message_id":"m3","by_role":"moderator"}"""))
            .isEqualTo(LiveFrame.Removed("s1", "m3"))
        assertThat(parse("""{"type":"moderation.moderators","stream_id":"s1","user_ids":["a","a"," b ",""]}"""))
            .isEqualTo(LiveFrame.Moderators("s1", listOf("a", "b")))
    }

    @Test
    fun `moderation frames missing their target are ignored`() {
        assertThat(parse("""{"type":"moderation.mute","stream_id":"s1"}""")).isNull()
        assertThat(parse("""{"type":"moderation.pin","stream_id":"s1","text":"x"}""")).isNull()
        assertThat(parse("""{"type":"moderation.unpin","stream_id":"s1"}""")).isNull()
        assertThat(parse("""{"type":"moderation.moderators","stream_id":"s1","user_ids":"a"}""")).isNull()
    }

    @Test
    fun `the host's word list never reaches a viewer`() {
        assertThat(parse("""{"type":"moderation.word_filter_added","stream_id":"s1","word":"x"}""")).isNull()
        assertThat(parse("""{"type":"moderation.word_filter_removed","stream_id":"s1","word":"x"}""")).isNull()
    }

    // ── status, counts, hearts ─────────────────────────────────────────

    @Test
    fun `status and viewer count read their numbers, absent counts stay null`() {
        assertThat(parse("""{"type":"status.changed","stream_id":"s1","status":"ended","ended_reason":"host_ended"}"""))
            .isEqualTo(LiveFrame.Status("s1", "ended", "host_ended", null, null))
        assertThat(parse("""{"type":"viewer.count","stream_id":"s1","viewer_count":12,"viewer_peak":40}"""))
            .isEqualTo(LiveFrame.Viewers("s1", 12, 40))
        assertThat(parse("""{"type":"viewer.count","stream_id":"s1","viewer_count":-1}""")).isNull()
        assertThat(parse("""{"type":"viewer.count","stream_id":"s1","viewer_count":"12"}""")).isNull()
    }

    @Test
    fun `a hearts frame carries the batch and the total, and an empty one says nothing`() {
        assertThat(parse("""{"type":"hearts","stream_id":"s1","count":3,"heart_count":515}"""))
            .isEqualTo(LiveFrame.Hearts("s1", 3, 515))
        assertThat(parse("""{"type":"hearts","stream_id":"s1"}""")).isNull()
    }

    // ── the gateway, and junk ──────────────────────────────────────────

    @Test
    fun `a refused subscribe and a revoked seat both read as refused`() {
        assertThat(parse("""{"type":"error","code":"LIVE_ROOM_REFUSED","stream_id":"s1"}"""))
            .isEqualTo(LiveFrame.Refused("s1"))
        assertThat(parse("""{"type":"subscription_revoked","stream_id":"s1"}"""))
            .isEqualTo(LiveFrame.Refused("s1"))
        assertThat(parse("""{"type":"error","code":"OTHER","stream_id":"s1"}""")).isNull()
    }

    @Test
    fun `another product's frame, an unknown type and junk are ignored`() {
        assertThat(parse("""{"type":"message","payload":{"conversation_id":"c1","message_id":"m1"}}""")).isNull()
        assertThat(parse("""{"type":"something.new","stream_id":"s1"}""")).isNull()
        assertThat(parse("""{"stream_id":"s1"}""")).isNull()
        assertThat(parse("not json")).isNull()
        assertThat(parse("[1,2]")).isNull()
        assertThat(parse("""{"type":"viewer.joined","stream_id":"s1","user_id":"u2","payload":"raw"}"""))
            .isInstanceOf(LiveFrame.Joined::class.java)
    }

    @Test
    fun `no gift, coin or guest frame is understood`() {
        listOf("gift", "gift.sent", "coins", "guest.joined", "cohost.invite").forEach { type ->
            assertThat(parse("""{"type":"$type","stream_id":"s1","user_id":"u2","count":1}""")).isNull()
        }
    }

    @Test
    fun `the subscribe and unsubscribe frames are the gateway's exact words`() {
        assertThat(subscribeLiveStreamFrame("s1")).isEqualTo("""{"type":"subscribe_live_stream","stream_id":"s1"}""")
        assertThat(unsubscribeLiveStreamFrame("s1")).isEqualTo("""{"type":"unsubscribe_live_stream","stream_id":"s1"}""")
    }

    @Test
    fun `a dropped socket waits longer each time, up to half a minute`() {
        assertThat(liveSocketRetryMillis(0)).isEqualTo(0L)
        assertThat(liveSocketRetryMillis(1)).isEqualTo(2_000L)
        assertThat(liveSocketRetryMillis(2)).isEqualTo(4_000L)
        assertThat(liveSocketRetryMillis(10)).isEqualTo(30_000L)
    }
}
