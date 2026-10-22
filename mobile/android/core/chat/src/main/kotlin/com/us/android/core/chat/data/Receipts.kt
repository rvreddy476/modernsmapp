package com.us.android.core.chat.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.POST
import retrofit2.http.Path
import java.time.Instant
import java.time.OffsetDateTime
import javax.inject.Inject
import javax.inject.Singleton

/**
 * WhatsApp-style message ticks (founder, 2026-10-22): one grey tick when the
 * server has it, two grey when the recipient's device received it (every
 * other member's, in a group), two coloured when they read it.
 *
 * The server decides what is disclosed: `read` is withheld when either side
 * keeps read receipts off and while a request is pending; `delivered` is
 * always shown. The client renders what it is told and infers nothing.
 */
enum class Tick {
    /** Not the viewer's message, or nothing to show (failed, unsent). */
    NONE,

    /** Still sending: no server copy yet. */
    PENDING,
    SENT,
    DELIVERED,
    READ,
    ;

    companion object {
        /** A history row's `status`; anything unknown (or absent) is SENT. */
        fun fromWire(status: String?): Tick = when (status?.trim()?.lowercase()) {
            "read" -> READ
            "delivered" -> DELIVERED
            else -> SENT
        }
    }
}

/**
 * The viewer-scoped aggregate watermarks: every message the viewer sent at or
 * before [deliveredUpTo] reached every other member's device; at or before
 * [readUpTo], every other member read it. RFC 3339 strings as the server sent
 * them; null when unknown.
 */
data class ReceiptWatermarks(
    val deliveredUpTo: String? = null,
    val readUpTo: String? = null,
) {
    /** Watermarks only move forward: a stale or replayed frame never lowers one. */
    fun advance(delivered: String? = null, read: String? = null): ReceiptWatermarks {
        val nextDelivered = later(deliveredUpTo, delivered)
        val nextRead = later(readUpTo, read)
        return if (nextDelivered == deliveredUpTo && nextRead == readUpTo) {
            this
        } else {
            copy(deliveredUpTo = nextDelivered, readUpTo = nextRead)
        }
    }
}

/**
 * Parses a receipt timestamp. Blank, unparseable and Go's zero time
 * (`0001-01-01T00:00:00Z`) are all absent.
 */
fun receiptInstant(raw: String?): Instant? {
    val text = raw?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    val instant = runCatching { OffsetDateTime.parse(text).toInstant() }.getOrNull()
        ?: runCatching { Instant.parse(text) }.getOrNull()
        ?: return null
    return instant.takeIf { it.isAfter(GO_ZERO_TIME_CEILING) }
}

/** The later of two receipt timestamps; an absent or unparseable one never wins. */
private fun later(current: String?, incoming: String?): String? {
    val incomingAt = receiptInstant(incoming) ?: return current
    val currentAt = receiptInstant(current) ?: return incoming
    return if (incomingAt.isAfter(currentAt)) incoming else current
}

// Anything at or before year 1 is Go's zero value, not a receipt.
private val GO_ZERO_TIME_CEILING: Instant = Instant.parse("0001-12-31T23:59:59Z")

/**
 * The tick for one message, as the VIEWER sees it.
 *
 * Only the viewer's own messages carry ticks. The result is the furthest of
 * the server's per-message [Message.receipt] (history), the live watermarks
 * and [readHint] (the legacy direct `read_receipt` naming this message): a
 * tick never moves backwards because one source is behind another.
 */
fun tickState(
    message: Message,
    viewerId: String,
    receipts: ReceiptWatermarks = ReceiptWatermarks(),
    readHint: Boolean = false,
): Tick {
    if (message.pending) return Tick.PENDING
    if (viewerId.isBlank() || message.senderId != viewerId) return Tick.NONE
    if (message.failed || message.deleted) return Tick.NONE
    val server = Tick.fromWire(message.receipt)
    val at = receiptInstant(message.ts) ?: receiptInstant(message.createdAt)
    val derived = when {
        readHint -> Tick.READ
        at == null -> Tick.SENT
        receiptInstant(receipts.readUpTo)?.let { !at.isAfter(it) } == true -> Tick.READ
        receiptInstant(receipts.deliveredUpTo)?.let { !at.isAfter(it) } == true -> Tick.DELIVERED
        else -> Tick.SENT
    }
    return maxOf(server, derived)
}

// ── Delivered acknowledgements ───────────────────────────────────────────

/** `POST v1/chat/conversations/{id}/delivered`: both fields when known; ts is the message's server ts. */
@Serializable
data class DeliveredRequest(
    @SerialName("up_to_message_id") val upToMessageId: String? = null,
    val ts: String? = null,
)

@Serializable
data class DeliveredDto(
    @SerialName("delivered_up_to") val deliveredUpTo: String? = null,
    val advanced: Boolean = false,
)

/** `receipts` on `GET /conversations/{id}`: the viewer-scoped aggregate watermarks. */
@Serializable
data class ReceiptsDto(
    @SerialName("delivered_up_to") val deliveredUpTo: String? = null,
    @SerialName("read_up_to") val readUpTo: String? = null,
) {
    fun toDomain() = ReceiptWatermarks(
        deliveredUpTo = deliveredUpTo?.takeIf { receiptInstant(it) != null },
        readUpTo = readUpTo?.takeIf { receiptInstant(it) != null },
    )
}

/** Its own interface, so existing [ChatApi] fakes stay valid. */
interface ReceiptsApi {
    @POST("v1/chat/conversations/{id}/delivered")
    suspend fun delivered(
        @Path("id") conversationId: String,
        @Body body: DeliveredRequest,
    ): com.us.android.core.network.ApiEnvelope<DeliveredDto>
}

/**
 * Batches delivered acks: per conversation, the newest message offered wins,
 * and a conversation is flushed at most once per [minIntervalMs].
 *
 * Pure (time is a parameter), so the batching rule is tested without a
 * scheduler. [offer] answers how long to wait before draining, or null when
 * a drain for that conversation is already scheduled.
 */
class DeliveredAckCoalescer(private val minIntervalMs: Long = DELIVERED_ACK_INTERVAL_MS) {

    data class Ack(val conversationId: String, val messageId: String?, val ts: String?)

    private val pending = HashMap<String, Ack>()
    private val lastFlush = HashMap<String, Long>()
    private val scheduled = HashSet<String>()

    @Synchronized
    fun offer(conversationId: String, messageId: String?, ts: String?, nowMs: Long): Long? {
        if (conversationId.isBlank()) return null
        val id = messageId?.takeIf { it.isNotBlank() }
        val at = ts?.takeIf { receiptInstant(it) != null }
        if (id == null && at == null) return null
        val incoming = Ack(conversationId, id, at)
        pending[conversationId] = newer(pending[conversationId], incoming)
        if (!scheduled.add(conversationId)) return null
        val last = lastFlush[conversationId] ?: return 0L
        return (last + minIntervalMs - nowMs).coerceAtLeast(0L)
    }

    /** The ack to send now for [conversationId], or null when nothing is owed. */
    @Synchronized
    fun drain(conversationId: String, nowMs: Long): Ack? {
        scheduled.remove(conversationId)
        val ack = pending.remove(conversationId) ?: return null
        lastFlush[conversationId] = nowMs
        if (lastFlush.size > LAST_FLUSH_PRUNE_AT) {
            lastFlush.entries.removeAll { nowMs - it.value > minIntervalMs }
        }
        return ack
    }

    /**
     * Two timestamps compare; otherwise the later ARRIVAL wins (a push carries
     * an id but no ts, and arrives after what it announces).
     */
    private fun newer(current: Ack?, incoming: Ack): Ack {
        if (current == null) return incoming
        val currentAt = receiptInstant(current.ts)
        val incomingAt = receiptInstant(incoming.ts)
        return if (currentAt != null && incomingAt != null && !incomingAt.isAfter(currentAt)) current else incoming
    }
}

/** At most one delivered ack per conversation per second. */
const val DELIVERED_ACK_INTERVAL_MS = 1_000L
private const val LAST_FLUSH_PRUNE_AT = 256

/**
 * Sends delivered acks: from the socket (a message arrived), from history
 * (the newest message from someone else) and from a chat push. Fire and
 * forget on its own scope — a failed or rate-limited ack is simply dropped;
 * the next message, history load or reconnect acks again, and the server's
 * cursor only moves forward.
 */
@Singleton
open class DeliveredAcker @Inject constructor(private val api: ReceiptsApi) {
    internal var clock: () -> Long = System::currentTimeMillis
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val coalescer = DeliveredAckCoalescer()

    open fun offer(conversationId: String, messageId: String?, ts: String?) {
        val wait = coalescer.offer(conversationId, messageId, ts, clock()) ?: return
        scope.launch {
            if (wait > 0) delay(wait)
            val ack = coalescer.drain(conversationId, clock()) ?: return@launch
            runCatching { api.delivered(ack.conversationId, DeliveredRequest(ack.messageId, ack.ts)) }
        }
    }
}

/** History is newest-first: the newest confirmed message somebody else sent. */
fun newestFromOthers(messages: List<Message>, viewerId: String): Message? =
    messages.firstOrNull { !it.pending && it.senderId.isNotBlank() && it.senderId != viewerId && it.id.isNotBlank() }
