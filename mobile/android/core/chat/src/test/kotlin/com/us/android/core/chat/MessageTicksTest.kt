package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.DeliveredAckCoalescer
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.ReceiptWatermarks
import com.us.android.core.chat.data.ReceiptsDto
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.Tick
import com.us.android.core.chat.data.newestFromOthers
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.chat.data.receiptInstant
import com.us.android.core.chat.data.tickState
import kotlinx.serialization.json.Json
import org.junit.Test

/** Message ticks (2026-10-22): tick state, watermarks, frames and the delivered-ack batching. */
class MessageTicksTest {

    private val json = Json { ignoreUnknownKeys = true }

    private fun mine(ts: String, receipt: String? = null) = Message(
        id = "m-$ts",
        conversationId = "c1",
        senderId = "me",
        senderDisplayName = null,
        text = "hi",
        createdAt = ts,
        ts = ts,
        bucket = "202610",
        receipt = receipt,
    )

    // ── tickState ───────────────────────────────────────────────────────

    @Test
    fun `server status alone gives sent, delivered and read`() {
        assertThat(tickState(mine("2026-10-22T10:00:00Z"), "me")).isEqualTo(Tick.SENT)
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "sent"), "me")).isEqualTo(Tick.SENT)
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "delivered"), "me")).isEqualTo(Tick.DELIVERED)
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "read"), "me")).isEqualTo(Tick.READ)
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "bogus"), "me")).isEqualTo(Tick.SENT)
    }

    @Test
    fun `watermarks decide by message time, inclusive`() {
        val marks = ReceiptWatermarks(deliveredUpTo = "2026-10-22T10:05:00Z", readUpTo = "2026-10-22T10:01:00Z")
        assertThat(tickState(mine("2026-10-22T10:00:00Z"), "me", marks)).isEqualTo(Tick.READ)
        assertThat(tickState(mine("2026-10-22T10:01:00Z"), "me", marks)).isEqualTo(Tick.READ)
        assertThat(tickState(mine("2026-10-22T10:03:00Z"), "me", marks)).isEqualTo(Tick.DELIVERED)
        assertThat(tickState(mine("2026-10-22T10:05:00.000001Z"), "me", marks)).isEqualTo(Tick.SENT)
    }

    @Test
    fun `a tick never moves backwards behind a lagging source`() {
        // History said read; the live watermark has not caught up.
        val marks = ReceiptWatermarks(deliveredUpTo = "2026-10-22T09:00:00Z")
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "read"), "me", marks)).isEqualTo(Tick.READ)
        // The legacy direct read_receipt naming this message is a read too.
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "delivered"), "me", readHint = true)).isEqualTo(Tick.READ)
    }

    @Test
    fun `only my confirmed messages carry ticks`() {
        val theirs = mine("2026-10-22T10:00:00Z", "read").copy(senderId = "peer")
        assertThat(tickState(theirs, "me")).isEqualTo(Tick.NONE)
        assertThat(tickState(mine("2026-10-22T10:00:00Z"), "")).isEqualTo(Tick.NONE)
        assertThat(tickState(mine("2026-10-22T10:00:00Z").copy(pending = true, senderId = ""), "me")).isEqualTo(Tick.PENDING)
        assertThat(tickState(mine("2026-10-22T10:00:00Z", "read").copy(deleted = true), "me")).isEqualTo(Tick.NONE)
        assertThat(tickState(mine("2026-10-22T10:00:00Z").copy(failed = true), "me")).isEqualTo(Tick.NONE)
    }

    // ── watermarks ──────────────────────────────────────────────────────

    @Test
    fun `watermarks only move forward and ignore zero or garbage times`() {
        val start = ReceiptWatermarks(deliveredUpTo = "2026-10-22T10:00:00Z", readUpTo = "2026-10-22T09:00:00Z")
        assertThat(start.advance(delivered = "2026-10-22T09:59:00Z")).isSameInstanceAs(start)
        assertThat(start.advance(read = "0001-01-01T00:00:00Z")).isSameInstanceAs(start)
        assertThat(start.advance(read = "not a time")).isSameInstanceAs(start)
        assertThat(start.advance(delivered = "2026-10-22T10:00:01+00:00").deliveredUpTo).isEqualTo("2026-10-22T10:00:01+00:00")
        assertThat(ReceiptWatermarks().advance(read = "2026-10-22T09:00:00Z").readUpTo).isEqualTo("2026-10-22T09:00:00Z")
        assertThat(receiptInstant("")).isNull()
        assertThat(receiptInstant("0001-01-01T00:00:00Z")).isNull()
    }

    @Test
    fun `the conversation receipts and the history status decode`() {
        val dto = json.decodeFromString(
            ReceiptsDto.serializer(),
            """{"delivered_up_to":"2026-10-22T10:00:00Z","read_up_to":"0001-01-01T00:00:00Z"}""",
        )
        assertThat(dto.toDomain()).isEqualTo(ReceiptWatermarks(deliveredUpTo = "2026-10-22T10:00:00Z", readUpTo = null))
        val message = json.decodeFromString(MessageDto.serializer(), """{"msg_id":"m1","sender_id":"me","status":"delivered"}""")
        assertThat(message.status).isEqualTo("delivered")
    }

    // ── frames ──────────────────────────────────────────────────────────

    @Test
    fun `delivery_receipt and a group's aggregate read_receipt decode to watermarks`() {
        val delivered = parseChatFrame(
            json,
            """{"type":"delivery_receipt","payload":{"conversation_id":"c1","user_id":"u2","message_id":"",""" +
                """"delivered_at":"2026-10-22T10:00:00Z","delivered_up_to":"2026-10-22T10:00:00Z"}}""",
        )
        assertThat(delivered).isEqualTo(ChatSocketEvent.ReceiptWatermark("c1", deliveredUpTo = "2026-10-22T10:00:00Z"))

        val groupRead = parseChatFrame(
            json,
            """{"type":"read_receipt","payload":{"conversation_id":"g1","aggregate":true,"read_up_to":"2026-10-22T10:02:00Z"}}""",
        )
        assertThat(groupRead).isEqualTo(ChatSocketEvent.ReceiptWatermark("g1", readUpTo = "2026-10-22T10:02:00Z"))

        // No watermark (Go's "" or zero time), no conversation: refused, never guessed.
        val blank = parseChatFrame(json, """{"type":"delivery_receipt","payload":{"conversation_id":"c1","delivered_up_to":""}}""")
        assertThat(blank).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        val noConversation = parseChatFrame(json, """{"type":"read_receipt","payload":{"aggregate":true,"read_up_to":"2026-10-22T10:02:00Z"}}""")
        assertThat(noConversation).isInstanceOf(ChatSocketEvent.Malformed::class.java)
    }

    @Test
    fun `the thread advances its watermarks from frames for THIS conversation only`() {
        val controller = ThreadController("c1", repositoryUnused(), viewerId = "me")
        assertThat(controller.onSocketEvent(ChatSocketEvent.ReceiptWatermark("other", deliveredUpTo = "2026-10-22T10:00:00Z"))).isNull()
        val delivered = controller.onSocketEvent(ChatSocketEvent.ReceiptWatermark("c1", deliveredUpTo = "2026-10-22T10:00:00Z"))
        assertThat(delivered?.receipts?.deliveredUpTo).isEqualTo("2026-10-22T10:00:00Z")
        // A stale frame changes nothing.
        assertThat(controller.onSocketEvent(ChatSocketEvent.ReceiptWatermark("c1", deliveredUpTo = "2026-10-22T09:00:00Z"))).isNull()
        val read = controller.onSocketEvent(ChatSocketEvent.ReadReceipt("c1", "peer", "m5", "2026-10-22T10:01:00Z"))
        assertThat(read?.receipts?.readUpTo).isEqualTo("2026-10-22T10:01:00Z")
        assertThat(read?.peerLastReadMessageId).isEqualTo("m5")
    }

    @Test
    fun `history acks the newest message somebody else sent`() {
        val rows = listOf(
            mine("2026-10-22T10:03:00Z"),
            mine("2026-10-22T10:02:00Z").copy(id = "p2", senderId = "peer"),
            mine("2026-10-22T10:01:00Z").copy(id = "p1", senderId = "peer"),
        )
        assertThat(newestFromOthers(rows, "me")?.id).isEqualTo("p2")
        assertThat(newestFromOthers(rows.take(1), "me")).isNull()
        val pendingFirst = listOf(mine("x").copy(id = "pending:1", senderId = "", pending = true)) + rows
        assertThat(newestFromOthers(pendingFirst, "me")?.id).isEqualTo("p2")
    }

    // ── DeliveredAckCoalescer ───────────────────────────────────────────

    @Test
    fun `the first ack goes at once and a burst inside a second becomes one`() {
        val c = DeliveredAckCoalescer(minIntervalMs = 1_000)
        assertThat(c.offer("c1", "m1", "2026-10-22T10:00:00Z", nowMs = 0)).isEqualTo(0L)
        // Already scheduled: no second timer.
        assertThat(c.offer("c1", "m2", "2026-10-22T10:00:01Z", nowMs = 10)).isNull()
        assertThat(c.drain("c1", nowMs = 20)).isEqualTo(DeliveredAckCoalescer.Ack("c1", "m2", "2026-10-22T10:00:01Z"))
        // Within the second after a flush: wait out the rest of it.
        assertThat(c.offer("c1", "m3", "2026-10-22T10:00:02Z", nowMs = 300)).isEqualTo(720L)
        assertThat(c.offer("c1", "m4", "2026-10-22T10:00:03Z", nowMs = 400)).isNull()
        assertThat(c.drain("c1", nowMs = 1_020)?.messageId).isEqualTo("m4")
        // Nothing owed: nothing sent.
        assertThat(c.drain("c1", nowMs = 1_030)).isNull()
    }

    @Test
    fun `conversations batch independently and the newest message wins`() {
        val c = DeliveredAckCoalescer(minIntervalMs = 1_000)
        assertThat(c.offer("c1", "m2", "2026-10-22T10:00:02Z", nowMs = 0)).isEqualTo(0L)
        assertThat(c.offer("c2", "x1", "2026-10-22T10:00:00Z", nowMs = 0)).isEqualTo(0L)
        // An older message arriving late does not lower the ack.
        c.offer("c1", "m1", "2026-10-22T10:00:01Z", nowMs = 1)
        assertThat(c.drain("c1", nowMs = 2)?.messageId).isEqualTo("m2")
        // A push (id, no ts) arriving after replaces: it announces what came later.
        c.offer("c2", "x2", null, nowMs = 3)
        assertThat(c.drain("c2", nowMs = 4)).isEqualTo(DeliveredAckCoalescer.Ack("c2", "x2", null))
    }

    @Test
    fun `an ack with nothing to name is dropped`() {
        val c = DeliveredAckCoalescer()
        assertThat(c.offer("", "m1", null, nowMs = 0)).isNull()
        assertThat(c.offer("c1", "", "", nowMs = 0)).isNull()
        assertThat(c.offer("c1", null, "garbage", nowMs = 0)).isNull()
        assertThat(c.drain("c1", nowMs = 0)).isNull()
    }

    private fun repositoryUnused() = com.us.android.core.chat.data.ChatRepository(
        java.lang.reflect.Proxy.newProxyInstance(
            com.us.android.core.chat.data.ChatApi::class.java.classLoader,
            arrayOf(com.us.android.core.chat.data.ChatApi::class.java),
        ) { _, method, _ -> error("not used: ${method.name}") }
            as com.us.android.core.chat.data.ChatApi,
        com.us.android.core.network.ErrorMapper(kotlinx.serialization.json.Json),
    )
}
