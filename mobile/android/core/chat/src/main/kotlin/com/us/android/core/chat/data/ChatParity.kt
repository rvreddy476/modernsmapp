package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.network.pagedApiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.time.Duration
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

// ── Domain (CONTRACT §1, §2.2, §5) ───────────────────────────────────────

data class ChatPeer(
    val userId: String,
    val displayName: String,
    val username: String,
    val avatarUrl: String?,
    /** Null when the peer's privacy settings deny it. */
    val isOnline: Boolean?,
    val lastActiveAt: String?,
)

data class ChatStreak(val days: Int, val atRisk: Boolean, val expiresAt: String?)

data class LastMessageInfo(
    val type: String,
    val preview: String,
    val senderId: String,
    val at: String,
    /** `sent`, `read`, or null. */
    val ownStatus: String?,
)

data class VoiceInfo(val durationMs: Long, val waveform: List<Int>)

data class StickerInfo(val id: String, val url: String, val width: Int, val height: Int)

data class GifInfo(
    val provider: String,
    val id: String,
    val url: String,
    val previewUrl: String?,
    val width: Int,
    val height: Int,
    /** The still frame (Giphy `still_url`), for a reduced-motion or loading state. */
    val stillUrl: String? = null,
    /** A Giphy sticker (fix pass 2): drawn on its own, with no bubble. */
    val isSticker: Boolean = false,
)

/**
 * Who sent a message request (fix pass 2). [deleted] when the account is
 * gone; the name is then blank.
 */
data class RequestSender(
    val userId: String,
    val displayName: String,
    val username: String,
    val avatarUrl: String?,
    val deleted: Boolean,
)

/**
 * A request row's name (fix pass 2): "Deleted account" for a deleted
 * sender, else their name, else @username — never "Conversation". Without a
 * sender block (an older server) the members decide, and a nameless request
 * still says who it might be rather than the placeholder.
 */
fun Conversation.requestTitle(viewerId: String): String {
    sender?.let { who ->
        if (who.deleted) return DELETED_ACCOUNT
        who.displayName.trim().takeIf { it.isNotEmpty() }?.let { return it }
        who.username.trim().removePrefix("@").takeIf { it.isNotEmpty() }?.let { return "@$it" }
    }
    peer?.let { who ->
        who.displayName.trim().takeIf { it.isNotEmpty() }?.let { return it }
        who.username.trim().removePrefix("@").takeIf { it.isNotEmpty() }?.let { return "@$it" }
    }
    val fromMembers = displayTitle(viewerId)
    return if (fromMembers == UNNAMED_CONVERSATION) UNNAMED_REQUEST_SENDER else fromMembers
}

/** What a request from a deleted account is called. */
const val DELETED_ACCOUNT = "Deleted account"

/** A request whose sender nobody named (an older server, no sender block). */
const val UNNAMED_REQUEST_SENDER = "Someone"

/** What the server says of an attachment. Every field null when it did not say. */
data class MediaInfo(
    val kind: String?,
    val width: Int?,
    val height: Int?,
    val durationMs: Long?,
    val sensitive: Boolean?,
) {
    val isVideo: Boolean get() = kind == MEDIA_KIND_VIDEO
    val isAudio: Boolean get() = kind == MEDIA_KIND_AUDIO
}

data class SharedItem(
    val messageId: String,
    val postId: String?,
    val mediaId: String?,
    val kind: String?,
    val senderId: String,
    val at: String,
)

enum class ForwardOutcome { SENT, BLOCKED, UNAVAILABLE, UNKNOWN }

data class ForwardResult(val conversationId: String, val outcome: ForwardOutcome, val messageId: String?)

data class ReactionGroup(val emoji: String, val count: Int, val users: List<ReactionUser>)

data class ReactionUser(val userId: String, val displayName: String, val avatarUrl: String?)

data class StickerPack(val id: String, val name: String, val stickers: List<StickerInfo>)

data class SeenBy(val userId: String, val displayName: String, val avatarUrl: String?, val at: String)

data class ReadSummary(val seenBy: List<SeenBy>, val count: Int)

data class Note(
    val id: String,
    val userId: String,
    val text: String,
    val audience: String,
    val createdAt: String,
    val expiresAt: String,
)

data class OthersNote(
    val note: Note,
    val userId: String,
    val displayName: String,
    val avatarUrl: String?,
    val isOnline: Boolean?,
)

data class Notes(val mine: Note?, val others: List<OthersNote>)

data class MessageHit(
    val conversationId: String,
    val messageId: String,
    val snippet: String,
    val senderId: String,
    val at: String,
)

data class ChatSearchResults(
    val people: List<PersonHit>,
    val conversations: List<Conversation>,
    val messages: List<MessageHit>,
)

/** One user's presence as `POST /v1/chat/presence` reports it. */
data class PresenceInfo(val isOnline: Boolean, val lastActiveAt: String?)

/** The reasons a message can be reported for (CONTRACT §2.6), with our words. */
enum class MessageReportReason(val wire: String, val label: String) {
    SPAM("spam", "Spam"),
    HARASSMENT("harassment", "Bullying or harassment"),
    HATE("hate", "Hate speech"),
    SEXUAL("sexual", "Nudity or sexual content"),
    VIOLENCE("violence", "Violence or threats"),
    SCAM("scam", "Scam or fraud"),
    OTHER("other", "Something else"),
}

/** The per-conversation alert tones (CONTRACT §1.1). */
enum class AlertTone(val wire: String, val label: String) {
    DEFAULT("default", "Default"),
    CHIME("chime", "Chime"),
    POP("pop", "Pop"),
    BELL("bell", "Bell"),
    SILENT("silent", "Silent"),
    ;

    companion object {
        /** Anything unknown (a newer server's tone, or nothing at all) reads as [DEFAULT]. */
        fun fromWire(value: String?): AlertTone = entries.firstOrNull { it.wire == value } ?: DEFAULT
    }
}

/** The viewer's full per-conversation switches, as the chat settings page edits them. */
data class ConversationPrefs(
    val isMuted: Boolean,
    val isPinned: Boolean,
    val muteCalls: Boolean,
    val alertTone: AlertTone,
    /**
     * The conversation's disappearing-messages timer, shared by every member.
     * Null when the server was not asked (a fallback built from the inbox
     * row), which is not the same as [DisappearTimer.OFF].
     */
    val disappearing: DisappearTimer? = null,
)

/**
 * Disappearing messages: how long after sending a message is removed for
 * everyone. The server accepts exactly these four values (milliseconds).
 */
enum class DisappearTimer(val millis: Long, val label: String) {
    OFF(0L, "Off"),
    DAY(86_400_000L, "24 hours"),
    WEEK(604_800_000L, "7 days"),
    NINETY_DAYS(7_776_000_000L, "90 days"),
    ;

    val isOn: Boolean get() = this != OFF

    companion object {
        /**
         * Absent, null and 0 are all off. A value outside the four is read
         * as the nearest longer setting rather than off — a timer the server
         * says is on must never be shown as off.
         */
        fun fromMillis(millis: Long?): DisappearTimer = when {
            millis == null || millis <= 0L -> OFF
            else -> entries.firstOrNull { it.isOn && it.millis >= millis } ?: NINETY_DAYS
        }
    }
}

/** The one-line note a thread shows while its timer is on; null when off. */
fun disappearingNote(timer: DisappearTimer?): String? =
    timer?.takeIf { it.isOn }?.let { "New messages disappear ${it.label} after they're sent." }

// ── Mapping ──────────────────────────────────────────────────────────────

internal fun PeerDto.toDomain() = ChatPeer(
    userId = userId,
    displayName = displayName,
    username = username,
    avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
    isOnline = isOnline,
    lastActiveAt = lastActiveAt?.takeIf { it.isNotBlank() },
)

internal fun GifDto.toInfo() = GifInfo(
    provider = provider,
    id = id,
    url = url,
    previewUrl = previewUrl?.takeIf { it.isNotBlank() },
    width = width,
    height = height,
    stillUrl = stillUrl?.takeIf { it.isNotBlank() },
    isSticker = isSticker,
)

/**
 * `media` from the wire, whatever shape it came in: an object is read field
 * by field, and anything else (absent, a bare id string, null) is no info.
 */
fun mediaInfoOf(element: JsonElement?): MediaInfo? {
    val obj = element as? JsonObject ?: return null
    fun prim(key: String) = runCatching { obj[key]?.jsonPrimitive }.getOrNull()
    val info = MediaInfo(
        kind = prim("kind")?.contentOrNull?.takeIf { it.isNotBlank() },
        width = prim("width")?.intOrNull,
        height = prim("height")?.intOrNull,
        durationMs = prim("duration_ms")?.longOrNull,
        sensitive = prim("sensitive")?.booleanOrNull,
    )
    return info.takeIf { it != MediaInfo(null, null, null, null, null) }
}

internal fun forwardOutcomeOf(wire: String): ForwardOutcome = when (wire) {
    "sent" -> ForwardOutcome.SENT
    "blocked" -> ForwardOutcome.BLOCKED
    "unavailable" -> ForwardOutcome.UNAVAILABLE
    else -> ForwardOutcome.UNKNOWN
}

/** Bulk presence, either shape per user: a bare boolean, or `{is_online, last_active_at}`. */
fun presenceOf(element: JsonElement?): Map<String, PresenceInfo> {
    val obj = element as? JsonObject ?: return emptyMap()
    return obj.mapNotNull { (userId, value) ->
        val info = when (value) {
            is JsonObject -> PresenceInfo(
                isOnline = runCatching { value["is_online"]?.jsonPrimitive?.booleanOrNull }.getOrNull() ?: false,
                lastActiveAt = runCatching { value["last_active_at"]?.jsonPrimitive?.contentOrNull }
                    .getOrNull()?.takeIf { it.isNotBlank() },
            )
            else -> runCatching { value.jsonPrimitive.booleanOrNull }.getOrNull()?.let { PresenceInfo(it, null) }
        }
        info?.let { userId to it }
    }.toMap()
}

// ── Pure presentation rules (unit-tested) ────────────────────────────────

/**
 * The thread header's presence line: "Active now", "Active 5m ago", "Active
 * 3h ago", "Active 2d ago" — or null when there is nothing honest to say
 * (privacy denied, never seen, or more than a week ago).
 */
fun presenceLabel(isOnline: Boolean?, lastActiveAt: String?, nowMillis: Long): String? {
    if (isOnline == true) return "Active now"
    val at = lastActiveAt?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: return null
    val minutes = Duration.ofMillis((nowMillis - at).coerceAtLeast(0)).toMinutes()
    return when {
        minutes < 1 -> "Active now"
        minutes < MINUTES_PER_HOUR -> "Active ${minutes}m ago"
        minutes < MINUTES_PER_DAY -> "Active ${minutes / MINUTES_PER_HOUR}h ago"
        minutes < MINUTES_PER_WEEK -> "Active ${minutes / MINUTES_PER_DAY}d ago"
        else -> null
    }
}

/** The thread header's word for a peer who is online right now (WhatsApp's). */
const val ONLINE_LABEL = "online"

/**
 * The thread header's presence line, WhatsApp style (founder, 23 Oct 2026):
 * "online" while the person has a live connection, otherwise "last seen today
 * at 14:05", "last seen yesterday at 14:05", "last seen 12 Oct at 14:05", and
 * "last seen 12 Oct 2025" from an earlier year. Null when privacy denies it or
 * nothing was ever recorded. Typing replaces it at the call site.
 */
fun lastSeenLabel(
    isOnline: Boolean?,
    lastActiveAt: String?,
    nowMillis: Long,
    zone: java.time.ZoneId = java.time.ZoneId.systemDefault(),
): String? {
    if (isOnline == true) return ONLINE_LABEL
    val at = lastActiveAt?.let { runCatching { Instant.parse(it) }.getOrNull() } ?: return null
    val seen = at.atZone(zone)
    val today = Instant.ofEpochMilli(nowMillis).atZone(zone).toLocalDate()
    val day = seen.toLocalDate()
    val time = LAST_SEEN_TIME.format(seen)
    return when {
        // A clock a little ahead of the device's still reads as today.
        !day.isBefore(today) -> "last seen today at $time"
        day == today.minusDays(1) -> "last seen yesterday at $time"
        day.year == today.year -> "last seen ${LAST_SEEN_DAY.format(seen)} at $time"
        else -> "last seen ${LAST_SEEN_DAY_YEAR.format(seen)}"
    }
}

private val LAST_SEEN_TIME = java.time.format.DateTimeFormatter.ofPattern("HH:mm", java.util.Locale.ENGLISH)
private val LAST_SEEN_DAY = java.time.format.DateTimeFormatter.ofPattern("d MMM", java.util.Locale.ENGLISH)
private val LAST_SEEN_DAY_YEAR = java.time.format.DateTimeFormatter.ofPattern("d MMM yyyy", java.util.Locale.ENGLISH)

/**
 * Whether a direct row's peer is online right now. A live answer (a
 * `presence_update` frame or the presence read, in [live]) wins over the
 * row's own `peer.is_online`, which was true when the inbox was fetched and
 * kept the dot lit after the person went offline. [onlineIds] is the bulk
 * presence read; absence there is not an answer (privacy and "not asked"
 * look the same), so it can only add a dot.
 */
fun peerOnline(
    peerId: String?,
    rowOnline: Boolean?,
    live: Map<String, PresenceInfo>,
    onlineIds: Set<String> = emptySet(),
): Boolean {
    if (peerId.isNullOrBlank()) return rowOnline == true
    live[peerId]?.let { return it.isOnline }
    return peerId in onlineIds || rowOnline == true
}

/** The streak count beside the flame, or null when there is no streak worth showing (days < 3). */
fun streakLabel(streak: ChatStreak?): String? =
    streak?.days?.takeIf { it >= STREAK_MIN_DAYS }?.toString()

/** The unread pill: the count, capped at "99+"; null when nothing is unread. */
fun unreadPillText(unreadCount: Int, hasUnread: Boolean): String? = when {
    unreadCount > UNREAD_CAP -> "$UNREAD_CAP+"
    unreadCount > 0 -> unreadCount.toString()
    hasUnread -> ""
    else -> null
}

/** Under the inbox row: "Seen" once the peer read the viewer's last message, "Sent" otherwise. */
fun ownStatusLabel(lastMessage: LastMessageInfo?, viewerId: String): String? {
    val last = lastMessage ?: return null
    if (viewerId.isBlank() || last.senderId != viewerId) return null
    return when (last.ownStatus) {
        "read" -> "Seen"
        "sent" -> "Sent"
        else -> null
    }
}

/**
 * The inbox's order (CONTRACT §1): pinned first, then the most recent
 * conversation. The cache has no pinned-at, so pinned rows keep recency
 * order among themselves.
 */
fun orderInbox(rows: List<Conversation>): List<Conversation> =
    rows.sortedWith(
        compareByDescending<Conversation> { it.isPinned }
            .thenByDescending { it.lastMessage?.at?.takeIf { at -> at.isNotBlank() } ?: it.lastMessageAt ?: it.updatedAt },
    )

/**
 * Downsamples raw recorder amplitudes (0..32767) into at most [buckets]
 * values 0..255 — CONTRACT §2.1's waveform. Each bucket keeps its LOUDEST
 * sample, and the whole is scaled to the recording's own peak so a quiet
 * voice still draws a readable shape.
 */
fun downsampleWaveform(samples: List<Int>, buckets: Int = MAX_WAVEFORM_POINTS): List<Int> {
    if (samples.isEmpty() || buckets <= 0) return emptyList()
    val clean = samples.map { it.coerceIn(0, MAX_AMPLITUDE) }
    val count = minOf(buckets, clean.size)
    val peaks = (0 until count).map { bucket ->
        val from = bucket * clean.size / count
        val to = ((bucket + 1) * clean.size / count).coerceAtLeast(from + 1)
        clean.subList(from, to).max()
    }
    val peak = peaks.max().coerceAtLeast(1)
    return peaks.map { (it.toLong() * MAX_WAVEFORM_VALUE / peak).toInt().coerceIn(0, MAX_WAVEFORM_VALUE) }
}

/** "0:07", "1:23": a voice message's length. */
fun formatVoiceDuration(durationMs: Long): String {
    val totalSeconds = (durationMs.coerceAtLeast(0) + MILLIS_ROUNDING) / MILLIS_PER_SECOND
    return "%d:%02d".format(totalSeconds / SECONDS_PER_MINUTE, totalSeconds % SECONDS_PER_MINUTE)
}

/** What a forward to several chats says when it lands (CONTRACT §2.4). */
fun forwardSummary(results: List<ForwardResult>): String {
    val sent = results.count { it.outcome == ForwardOutcome.SENT }
    val total = results.size
    return when {
        total == 0 -> "Couldn't forward. Try again."
        sent == total -> if (total == 1) "Sent" else "Sent to $total chats"
        sent == 0 -> if (total == 1) "This chat can't receive it." else "None of those chats can receive it."
        else -> "Sent to $sent of $total chats. ${total - sent} couldn't receive it."
    }
}

/** True when the server answered "this route does not exist (yet)": the feature hides. */
fun AppError.isMissingRoute(): Boolean = this is AppError.NotFound ||
    (this is AppError.Unknown && statusCode == HTTP_NOT_FOUND)

// ── Repository ───────────────────────────────────────────────────────────

/**
 * The chat-parity routes in [AppResult]. Thin, like [ChatRepository]; the
 * screens decide what a refusal means, and a 404 means "not shipped here
 * yet" ([isMissingRoute]) so the feature hides rather than errors.
 */
@Singleton
@Suppress("TooManyFunctions") // One function per contract route.
class ChatParityRepository @Inject constructor(
    private val api: ChatParityApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun lane(lane: String, cursor: String? = null): AppResult<Paged<Conversation>> =
        pagedApiCall(errorMapper) { api.conversationsInLane(lane = lane, cursor = cursor) }
            .mapPaged { it.toDomain() }

    suspend fun requests(filter: String): AppResult<List<Conversation>> =
        listApiCall(errorMapper) { api.requestsFiltered(filter) }.mapOk { rows -> rows.map { it.toDomain() } }

    suspend fun deleteForMe(conversationId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.deleteForMe(conversationId) }

    suspend fun clear(conversationId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.clear(conversationId) }

    suspend fun shared(conversationId: String, kind: String, cursor: String? = null): AppResult<Paged<SharedItem>> =
        pagedApiCall(errorMapper) { api.shared(conversationId, kind, cursor) }.mapPaged {
            SharedItem(
                messageId = it.messageId,
                postId = it.postId?.takeIf { id -> id.isNotBlank() },
                mediaId = it.mediaId?.takeIf { id -> id.isNotBlank() },
                kind = it.kind,
                senderId = it.senderId,
                at = it.at,
            )
        }

    suspend fun forward(
        conversationId: String,
        messageId: String,
        targets: List<String>,
        idempotencyKey: String,
    ): AppResult<List<ForwardResult>> =
        apiCall(errorMapper) {
            api.forward(
                conversationId,
                messageId,
                ForwardRequest(targets.distinct().take(MAX_FORWARD_TARGETS), idempotencyKey),
            )
        }.mapOk { body ->
            body.results.map { ForwardResult(it.conversationId, forwardOutcomeOf(it.outcome), it.messageId) }
        }

    /** Edits the viewer's own text message; the server refuses anyone else's. */
    suspend fun edit(conversationId: String, message: Message, text: String): AppResult<Message> =
        apiCall(errorMapper) {
            api.edit(conversationId, message.id, EditMessageRequest(text.trim(), message.bucket, message.ts))
        }.mapOk { it.toDomain() }

    suspend fun reactions(conversationId: String, messageId: String): AppResult<List<ReactionGroup>> =
        listApiCall(errorMapper) { api.reactions(conversationId, messageId) }.mapOk { groups ->
            groups.filter { it.emoji.isNotBlank() }.map { group ->
                ReactionGroup(
                    emoji = group.emoji,
                    count = maxOf(group.count, group.users.size),
                    users = group.users.map { ReactionUser(it.userId, it.displayName, it.avatarUrl) },
                )
            }
        }

    suspend fun report(
        conversationId: String,
        messageId: String,
        reason: MessageReportReason,
        note: String? = null,
    ): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.report(conversationId, messageId, ReportMessageRequest(reason.wire, note?.takeIf { it.isNotBlank() }))
        }

    /** Fix pass 2: report the whole conversation (`POST .../report`, 202). */
    suspend fun reportConversation(
        conversationId: String,
        reason: MessageReportReason,
        note: String? = null,
    ): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.reportConversation(conversationId, ReportMessageRequest(reason.wire, note?.takeIf { it.isNotBlank() }))
        }

    /** Fix pass 2: mark unread on the server (`true`), or clear that mark (`false`). */
    suspend fun setUnread(conversationId: String, unread: Boolean): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.setUnread(conversationId, UnreadRequest(unread)) }

    suspend fun stickers(): AppResult<List<StickerPack>> =
        apiCall(errorMapper) { api.stickers() }.mapOk { catalogue ->
            catalogue.packs.map { pack ->
                StickerPack(
                    id = pack.id,
                    name = pack.name,
                    stickers = pack.stickers.filter { it.url.isNotBlank() }
                        .map { StickerInfo(it.id, it.url, it.width, it.height) },
                )
            }.filter { it.stickers.isNotEmpty() }
        }

    suspend fun readSummary(conversationId: String, messageId: String): AppResult<ReadSummary> =
        apiCall(errorMapper) { api.readSummary(conversationId, messageId) }.mapOk { body ->
            ReadSummary(
                seenBy = body.seenBy.map { SeenBy(it.userId, it.displayName, it.avatarUrl, it.at) },
                count = maxOf(body.count, body.seenBy.size),
            )
        }

    suspend fun notes(): AppResult<Notes> =
        apiCall(errorMapper) { api.notes() }.mapOk { body ->
            Notes(
                mine = body.mine?.takeIf { it.text.isNotBlank() }?.toDomain(),
                others = body.others.filter { it.note.text.isNotBlank() && it.user.userId.isNotBlank() }.map {
                    OthersNote(it.note.toDomain(), it.user.userId, it.user.displayName, it.user.avatarUrl, it.user.isOnline)
                },
            )
        }

    suspend fun putNote(text: String, audience: String): AppResult<Note> =
        apiCall(errorMapper) { api.putNote(PutNoteRequest(text.trim(), audience)) }.mapOk { it.toDomain() }

    suspend fun deleteNote(): AppResult<Unit> = noContentApiCall(errorMapper) { api.deleteNote() }

    suspend fun search(query: String): AppResult<ChatSearchResults> =
        apiCall(errorMapper) { api.search(query.trim()) }.mapOk { body ->
            ChatSearchResults(
                people = body.people.filter { it.key.isNotBlank() }
                    .map { PersonHit(it.key, it.username, it.displayName, it.avatarUrl) },
                conversations = body.conversations.filter { it.id.isNotBlank() }.map { it.toDomain() }.withoutDatingChats(),
                messages = body.messages.filter { it.conversationId.isNotBlank() && it.messageId.isNotBlank() }
                    .map { MessageHit(it.conversationId, it.messageId, plainSnippet(it.snippet), it.senderId, it.at) },
            )
        }

    suspend fun presence(userIds: List<String>): AppResult<Map<String, PresenceInfo>> =
        apiCall(errorMapper) { api.presenceDetails(BulkPresenceRequest(userIds.distinct().take(PRESENCE_CAP))) }
            .mapOk { presenceOf(it) }

    private val _disappearing = MutableStateFlow<Map<String, DisappearTimer>>(emptyMap())

    /**
     * Conversation id → its disappearing timer, as last read or saved on this
     * device. The settings page writes through [setDisappearing]; an open
     * thread watches this to draw its timer icon without a second read.
     */
    val disappearing: StateFlow<Map<String, DisappearTimer>> = _disappearing.asStateFlow()

    suspend fun prefs(conversationId: String): AppResult<ConversationPrefs> =
        apiCall(errorMapper) { api.settings(conversationId) }.mapOk { it.toPrefs() }
            .also { result -> (result as? AppResult.Success)?.data?.disappearing?.let { remember(conversationId, it) } }

    /**
     * Sets the conversation's disappearing timer (Off is sent as 0). The
     * server's echo wins when it carries the field; an echo without it is
     * taken to mean the value sent was stored.
     */
    suspend fun setDisappearing(conversationId: String, timer: DisappearTimer): AppResult<DisappearTimer> =
        apiCall(errorMapper) { api.updateDisappearing(conversationId, DisappearingRequest(timer.millis)) }
            .mapOk { echo -> echo.disappearAfterMs?.let { DisappearTimer.fromMillis(it) } ?: timer }
            .also { result -> (result as? AppResult.Success)?.data?.let { remember(conversationId, it) } }

    /** A `disappearing_timer_updated` frame: another member (or device) changed the shared timer. */
    fun applyRemoteDisappearing(conversationId: String, disappearAfterMs: Long) {
        if (conversationId.isNotBlank()) remember(conversationId, DisappearTimer.fromMillis(disappearAfterMs))
    }

    private fun remember(conversationId: String, timer: DisappearTimer) =
        _disappearing.update { it + (conversationId to timer) }

    /** Writes every switch at once — the server's echo is what the page then shows. */
    suspend fun savePrefs(conversationId: String, prefs: ConversationPrefs): AppResult<ConversationPrefs> =
        apiCall(errorMapper) {
            api.updateSettings(
                conversationId,
                ConversationSettingsDto(
                    isMuted = prefs.isMuted,
                    isPinned = prefs.isPinned,
                    muteCalls = prefs.muteCalls,
                    alertTone = prefs.alertTone.wire,
                ),
            )
        }.mapOk { it.toPrefs(fallback = prefs) }

    /** Archive (`archived`) or bring back (`none`); the pin/mute switches ride along unchanged. */
    suspend fun setLabel(conversation: Conversation, label: String): AppResult<Unit> =
        apiCall(errorMapper) {
            api.updateSettings(
                conversation.id,
                ConversationSettingsDto(isMuted = conversation.isMuted, isPinned = conversation.isPinned, label = label),
            )
        }.mapOk { }

    /** One typed send (voice, sticker, gif, profile, sound, live) under the caller's key. */
    suspend fun send(conversationId: String, idempotencyKey: String, body: SendMessageRequest): AppResult<Message> =
        apiCall(errorMapper) { api.send(conversationId, idempotencyKey, body) }.mapOk { it.toDomain() }

    private companion object {
        const val PRESENCE_CAP = 50
    }
}

private fun NoteDto.toDomain() = Note(id, userId, text, audience, createdAt, expiresAt)

private fun ConversationSettingsDto.toPrefs(fallback: ConversationPrefs? = null) = ConversationPrefs(
    isMuted = isMuted,
    isPinned = isPinned,
    muteCalls = muteCalls ?: fallback?.muteCalls ?: false,
    alertTone = alertTone?.let { AlertTone.fromWire(it) } ?: fallback?.alertTone ?: AlertTone.DEFAULT,
    // The savePrefs PUT never carries the timer, so its echo is no news about
    // it: keep what the page already knew. A plain read is the answer.
    disappearing = if (fallback != null) {
        fallback.disappearing ?: DisappearTimer.fromMillis(disappearAfterMs)
    } else {
        DisappearTimer.fromMillis(disappearAfterMs)
    },
)

private inline fun <T, R> AppResult<T>.mapOk(block: (T) -> R): AppResult<R> = when (this) {
    is AppResult.Success -> AppResult.Success(block(data))
    is AppResult.Failure -> this
}

private inline fun <T, R> AppResult<Paged<T>>.mapPaged(block: (T) -> R): AppResult<Paged<R>> =
    mapOk { page -> Paged(page.items.map(block), page.nextCursor) }

const val MEDIA_KIND_IMAGE = "image"
const val MEDIA_KIND_VIDEO = "video"
const val MEDIA_KIND_AUDIO = "audio"

/** A streak shows from three counted days (CONTRACT §3). */
const val STREAK_MIN_DAYS = 3

/** CONTRACT §2.1: at most 64 waveform points, each 0..255. */
const val MAX_WAVEFORM_POINTS = 64
const val MAX_WAVEFORM_VALUE = 255

/** CONTRACT §2.1: a voice message is 1 ms to 5 minutes. */
const val MAX_VOICE_MILLIS = 300_000L

private const val MAX_AMPLITUDE = 32_767
private const val UNREAD_CAP = 99
private const val HTTP_NOT_FOUND = 404
private const val MINUTES_PER_HOUR = 60L
private const val MINUTES_PER_DAY = 1_440L
private const val MINUTES_PER_WEEK = 10_080L
private const val MILLIS_PER_SECOND = 1_000L
private const val MILLIS_ROUNDING = 500L
private const val SECONDS_PER_MINUTE = 60L

/**
 * A search snippet as text: the index marks matches with `<em>…</em>`
 * (search_result.json); the client draws plain text, so the tags go.
 */
fun plainSnippet(snippet: String): String = snippet.replace(SNIPPET_TAG, "")

private val SNIPPET_TAG = Regex("</?[a-zA-Z][^>]*>")
