package com.us.android.feature.live.data

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

/*
 * The live room's realtime frames (T-30, 2026-10-15).
 *
 * live-service-v2 publishes every room event as ONE JSON object on Redis
 * `live:stream:{id}` (internal/service/realtime.go):
 *
 *   {"type", "stream_id", "at", <payload fields>..., "payload": {...}}
 *
 * The payload's fields sit at the top level AND again under "payload"; this
 * parser reads the top level first and falls back to "payload", so either
 * shape (or an older server that only nests) decodes the same. ws-gateway
 * forwards the object verbatim to sockets it admitted through
 * {"type":"subscribe_live_stream","stream_id"} (liverooms.go). A refused
 * subscribe is {"type":"error","code":"LIVE_ROOM_REFUSED","stream_id"}; a
 * seat dropped by the gateway's 30 s re-check is
 * {"type":"subscription_revoked","stream_id"}. Either one sends the screen
 * back to polling alone.
 *
 * A frame missing what it needs (a join without user_id, a chat row without
 * an id) is IGNORED, never repaired: there is no right value to invent.
 *
 * moderation.word_filter_* parse to null: the word list is the host's alone.
 * There is nothing here for gifts, coins or guests — none exist (founder).
 */

/** One room event, reduced to what a screen acts on. */
sealed interface LiveFrame {
    val streamId: String

    /** `status.changed`. Counts are null when the frame did not carry them. */
    data class Status(
        override val streamId: String,
        val status: String,
        val endedReason: String,
        val viewerCount: Int?,
        val viewerPeak: Int?,
    ) : LiveFrame

    /** `chat.message`: exactly the row `GET …/chat` returns. */
    data class Chat(override val streamId: String, val message: LiveChatMessageDto) : LiveFrame

    /** `chat.removed`. */
    data class Removed(override val streamId: String, val messageId: String) : LiveFrame

    /** `viewer.count`. */
    data class Viewers(override val streamId: String, val viewerCount: Int, val viewerPeak: Int?) : LiveFrame

    /**
     * `viewer.joined` (T-29): a signed-in viewer joined. A system row only —
     * there is no HTTP read of joins, so it is never stored with the chat.
     * [author] is the chat author card (`user`), or null when absent.
     */
    data class Joined(
        override val streamId: String,
        val userId: String,
        val author: LiveChatAuthorDto?,
        val joinedAt: String,
    ) : LiveFrame

    /** `moderation.mute`: [userId] can no longer chat here. */
    data class Muted(override val streamId: String, val userId: String) : LiveFrame

    /** `moderation.unmute`. */
    data class Unmuted(override val streamId: String, val userId: String) : LiveFrame

    /** `moderation.pin`: carries the text but no author card. */
    data class Pinned(
        override val streamId: String,
        val messageId: String,
        val userId: String,
        val text: String,
    ) : LiveFrame

    /** `moderation.unpin`. */
    data class Unpinned(override val streamId: String, val messageId: String) : LiveFrame

    /** `moderation.ban`. */
    data class Banned(override val streamId: String, val userId: String) : LiveFrame

    /** `moderation.unban`. */
    data class Unbanned(override val streamId: String, val userId: String) : LiveFrame

    /** `moderation.moderators`: the whole set. */
    data class Moderators(override val streamId: String, val userIds: List<String>) : LiveFrame

    /** `hearts`: free hearts since the last frame (names nobody) and the stream total (0 when absent). */
    data class Hearts(override val streamId: String, val count: Int, val heartCount: Long) : LiveFrame

    /** The gateway refused the room, or revoked the seat: poll instead. */
    data class Refused(override val streamId: String) : LiveFrame
}

const val FRAME_STATUS_CHANGED = "status.changed"
const val FRAME_CHAT_MESSAGE = "chat.message"
const val FRAME_CHAT_REMOVED = "chat.removed"
const val FRAME_VIEWER_COUNT = "viewer.count"
const val FRAME_VIEWER_JOINED = "viewer.joined"
const val FRAME_MUTE = "moderation.mute"
const val FRAME_UNMUTE = "moderation.unmute"
const val FRAME_PIN = "moderation.pin"
const val FRAME_UNPIN = "moderation.unpin"
const val FRAME_BAN = "moderation.ban"
const val FRAME_UNBAN = "moderation.unban"
const val FRAME_MODERATORS = "moderation.moderators"
const val FRAME_HEARTS = "hearts"

private const val FRAME_ERROR = "error"
private const val FRAME_REVOKED = "subscription_revoked"
private const val CODE_LIVE_ROOM_REFUSED = "LIVE_ROOM_REFUSED"

/** Reads one field: the top level first (live-service-v2's reading contract), then "payload". */
private class FrameFields(private val frame: JsonObject) {
    private val payload: JsonObject? = runCatching { frame["payload"]?.jsonObject }.getOrNull()

    fun raw(key: String): JsonElement? {
        val top = frame[key]
        if (top != null && top !is JsonNull) return top
        return payload?.get(key)?.takeIf { it !is JsonNull }
    }

    fun str(key: String): String = (raw(key) as? JsonPrimitive)?.takeIf { it.isString }?.contentOrNull.orEmpty()

    /** An identifier or a token: surrounding space never counts. */
    fun id(key: String): String = str(key).trim()

    /** A whole, non-negative number; null when absent or not a number. */
    fun count(key: String): Long? {
        val value = (raw(key) as? JsonPrimitive)?.takeIf { !it.isString }?.doubleOrNull ?: return null
        return if (value.isNaN() || value < 0) null else value.toLong()
    }

    fun bool(key: String): Boolean = (raw(key) as? JsonPrimitive)?.booleanOrNull ?: false
}

/**
 * One socket text frame as a [LiveFrame], or null when it is not a live-room
 * frame this client acts on (another product's frame, an unknown type, a
 * frame missing a required field, or not JSON at all).
 */
@Suppress("CyclomaticComplexMethod", "ReturnCount") // one branch per wire type, each guarded
fun parseLiveFrame(json: Json, text: String): LiveFrame? {
    val frame = runCatching { json.parseToJsonElement(text).jsonObject }.getOrNull() ?: return null
    val type = (frame["type"] as? JsonPrimitive)?.contentOrNull?.trim().orEmpty()
    if (type.isEmpty()) return null
    val f = FrameFields(frame)
    val streamId = f.id("stream_id")
    if (streamId.isEmpty()) return null

    if ((type == FRAME_ERROR && f.id("code") == CODE_LIVE_ROOM_REFUSED) || type == FRAME_REVOKED) {
        return LiveFrame.Refused(streamId)
    }
    return when (type) {
        FRAME_STATUS_CHANGED -> f.id("status").takeIf { it.isNotEmpty() }?.let { status ->
            LiveFrame.Status(
                streamId = streamId,
                status = status,
                endedReason = f.id("ended_reason"),
                viewerCount = f.count("viewer_count")?.toInt(),
                viewerPeak = f.count("viewer_peak")?.toInt(),
            )
        }
        FRAME_CHAT_MESSAGE -> parseChatFrame(json, f, streamId)
        FRAME_CHAT_REMOVED -> f.id("message_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Removed(streamId, it) }
        FRAME_VIEWER_COUNT -> f.count("viewer_count")?.let { count ->
            LiveFrame.Viewers(streamId, count.toInt(), f.count("viewer_peak")?.toInt())
        }
        FRAME_VIEWER_JOINED -> f.id("user_id").takeIf { it.isNotEmpty() }?.let { userId ->
            LiveFrame.Joined(
                streamId = streamId,
                userId = userId,
                author = authorOf(json, f.raw("user"), userId),
                joinedAt = f.id("joined_at").ifEmpty { f.id("at") },
            )
        }
        FRAME_MUTE -> f.id("user_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Muted(streamId, it) }
        FRAME_UNMUTE -> f.id("user_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Unmuted(streamId, it) }
        FRAME_PIN -> f.id("message_id").takeIf { it.isNotEmpty() }?.let { id ->
            LiveFrame.Pinned(streamId, id, f.id("user_id"), f.str("text"))
        }
        FRAME_UNPIN -> f.id("message_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Unpinned(streamId, it) }
        FRAME_BAN -> f.id("user_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Banned(streamId, it) }
        FRAME_UNBAN -> f.id("user_id").takeIf { it.isNotEmpty() }?.let { LiveFrame.Unbanned(streamId, it) }
        FRAME_MODERATORS -> (f.raw("user_ids") as? JsonArray)?.let { ids ->
            LiveFrame.Moderators(
                streamId,
                ids.mapNotNull { (it as? JsonPrimitive)?.contentOrNull?.trim()?.takeIf(String::isNotEmpty) }.distinct(),
            )
        }
        FRAME_HEARTS -> {
            val count = f.count("count") ?: 0
            val total = f.count("heart_count") ?: 0
            if (count == 0L && total == 0L) null else LiveFrame.Hearts(streamId, count.toInt(), total)
        }
        else -> null
    }
}

private fun parseChatFrame(json: Json, f: FrameFields, streamId: String): LiveFrame? {
    val id = f.id("id")
    val userId = f.id("user_id")
    if (id.isEmpty() || userId.isEmpty()) return null
    return LiveFrame.Chat(
        streamId,
        LiveChatMessageDto(
            id = id,
            userId = userId,
            text = f.str("text"),
            isPinned = f.bool("is_pinned"),
            createdAt = f.id("created_at"),
            author = authorOf(json, f.raw("author"), userId),
        ),
    )
}

/** The author card (`author` on a chat row, `user` on a join); its id falls back to [userId]. */
private fun authorOf(json: Json, raw: JsonElement?, userId: String): LiveChatAuthorDto? {
    val obj = raw as? JsonObject ?: return null
    val card = runCatching { json.decodeFromJsonElement(LiveChatAuthorDto.serializer(), obj) }.getOrNull()
        ?: return null
    return if (card.userId.isBlank()) card.copy(userId = userId) else card
}

/** `{"type":"subscribe_live_stream","stream_id":"<id>"}`: what a viewer sends to join the room's frames. */
fun subscribeLiveStreamFrame(streamId: String): String =
    buildJsonObject {
        put("type", "subscribe_live_stream")
        put("stream_id", streamId)
    }.toString()

/** `{"type":"unsubscribe_live_stream","stream_id":"<id>"}`. */
fun unsubscribeLiveStreamFrame(streamId: String): String =
    buildJsonObject {
        put("type", "unsubscribe_live_stream")
        put("stream_id", streamId)
    }.toString()
