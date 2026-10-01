package com.us.android.feature.live.data

import com.us.android.core.common.error.AppError

/**
 * The stream's lifecycle as live-service-v2 reports it (live-fix contract,
 * 2026-10-01): scheduled → starting → live ⇄ reconnecting → ended | failed.
 *
 * The client never advances this on its own. A screen shows what the server
 * last said, so a lost webhook or a dead host can never leave a false LIVE
 * badge on a phone.
 */
enum class LiveStatus {
    Scheduled,
    Starting,
    Live,
    Reconnecting,
    Ended,
    Failed,

    /** A value this build does not know. Shown as nothing, never as Live. */
    Unknown,
    ;

    /** Ended and Failed are final: stop polling, drop the room. */
    val isOver: Boolean get() = this == Ended || this == Failed
}

fun liveStatusOf(wire: String): LiveStatus = when (wire.trim().lowercase()) {
    "scheduled" -> LiveStatus.Scheduled
    "starting" -> LiveStatus.Starting
    "live" -> LiveStatus.Live
    "reconnecting" -> LiveStatus.Reconnecting
    "ended" -> LiveStatus.Ended
    "failed" -> LiveStatus.Failed
    else -> LiveStatus.Unknown
}

/** Why an ended or failed stream stopped (`ended_reason`). */
enum class EndedReason { HostEnded, HostLost, RoomFinished, AdminStopped, NoMedia, Unknown }

fun endedReasonOf(wire: String): EndedReason = when (wire.trim().lowercase()) {
    "host_ended" -> EndedReason.HostEnded
    "host_lost" -> EndedReason.HostLost
    "room_finished" -> EndedReason.RoomFinished
    "admin_stopped" -> EndedReason.AdminStopped
    "no_media" -> EndedReason.NoMedia
    else -> EndedReason.Unknown
}

/**
 * The status the host's screen shows straight after POST /start.
 *
 * Start makes a stream `starting`, not `live`: only the host's first
 * published track does that. So anything but an explicit `live` (or a final
 * state) is Starting — a blank or unknown status must never read as on air.
 */
fun hostStatusAfterStart(wire: String): LiveStatus = when (val status = liveStatusOf(wire)) {
    LiveStatus.Live, LiveStatus.Reconnecting, LiveStatus.Ended, LiveStatus.Failed -> status
    else -> LiveStatus.Starting
}

/** The red LIVE badge is earned by the server's `live` and nothing else. */
fun showsLiveBadge(status: LiveStatus): Boolean = status == LiveStatus.Live

/** The viewer count is shown while there is a broadcast to count viewers of. */
fun showsViewerCount(status: LiveStatus): Boolean =
    status == LiveStatus.Live || status == LiveStatus.Reconnecting

/**
 * The chat as the screen shows it: newest first, as `GET …/chat` returns it,
 * minus every message removed while this screen was open.
 *
 * [removed] is a tombstone set, so a poll that left before a removal and
 * lands after it cannot bring the message back. Removal reaches this class
 * from the moderator's own action today and from a `chat.removed` room
 * event once the app subscribes to the live room over the socket.
 */
data class ChatLog(
    val messages: List<LiveChatMessageDto> = emptyList(),
    val removed: Set<String> = emptySet(),
) {
    /** A fresh `GET …/chat` page replaces the list. Blank ids are refused, duplicates collapsed. */
    fun withSnapshot(rows: List<LiveChatMessageDto>): ChatLog =
        copy(messages = rows.filter { it.id.isNotBlank() && it.id !in removed }.distinctBy { it.id })

    /** The viewer's own message, from the send response, ahead of the next poll. */
    fun withSent(message: LiveChatMessageDto): ChatLog = when {
        message.id.isBlank() || message.id in removed -> this
        messages.any { it.id == message.id } -> this
        else -> copy(messages = listOf(message) + messages)
    }

    /** `chat.removed`: gone for good on this screen. */
    fun withRemoved(messageId: String): ChatLog = if (messageId.isBlank()) {
        this
    } else {
        copy(messages = messages.filterNot { it.id == messageId }, removed = removed + messageId)
    }
}

/** The server's cap on a stream's moderators (`PUT …/moderators`, max 5). */
const val MAX_STREAM_MODERATORS = 5

/** Whether the host may add [candidate]: not themselves, not twice, and never a sixth. */
fun canAddModerator(current: List<String>, candidate: String, hostId: String): Boolean =
    candidate.isNotBlank() &&
        candidate != hostId &&
        candidate !in current &&
        current.size < MAX_STREAM_MODERATORS

/** What the host can do with one chat message. */
enum class HostMessageAction(val label: String) {
    Ban("Ban user"),
    Unban("Unban user"),
    MakeModerator("Make moderator"),
    RemoveModerator("Remove moderator"),
    RemoveMessage("Remove message"),
}

/**
 * The host's menu for [message], in ascending alphabetical order by label
 * (founder's rule for every menu). The host's own message can only be
 * removed: banning or promoting yourself is meaningless.
 */
fun hostMessageActions(
    message: LiveChatMessageDto,
    hostId: String,
    moderators: List<String>,
    banned: Set<String>,
): List<HostMessageAction> {
    val author = message.userId
    val actions = buildList {
        add(HostMessageAction.RemoveMessage)
        if (author.isNotBlank() && author != hostId) {
            add(if (author in banned) HostMessageAction.Unban else HostMessageAction.Ban)
            when {
                author in moderators -> add(HostMessageAction.RemoveModerator)
                canAddModerator(moderators, author, hostId) -> add(HostMessageAction.MakeModerator)
            }
        }
    }
    return actions.sortedBy { it.label.lowercase() }
}

/** What a viewer can do with one chat message. */
enum class ViewerMessageAction(val label: String) {
    Ban("Ban user"),
    RemoveMessage("Remove message"),
    Report("Report message"),
}

/**
 * A viewer's menu for [message], alphabetical. Everyone can report; a
 * stream moderator ([canModerate], from `moderator_user_ids` being present
 * on the stream) can also remove it and ban its author — never the host.
 */
fun viewerMessageActions(
    message: LiveChatMessageDto,
    hostId: String,
    canModerate: Boolean,
): List<ViewerMessageAction> {
    val actions = buildList {
        add(ViewerMessageAction.Report)
        if (canModerate) {
            add(ViewerMessageAction.RemoveMessage)
            if (message.userId.isNotBlank() && message.userId != hostId) add(ViewerMessageAction.Ban)
        }
    }
    return actions.sortedBy { it.label.lowercase() }
}

/**
 * The reasons a viewer can report a stream or a message for, with the token
 * live-service-v2 accepts. Labels match the post report sheet's wording.
 */
enum class LiveReportReason(val label: String, val wire: String) {
    Spam("Spam", "spam"),
    Harassment("Harassment", "harassment"),
    Hate("Hate speech", "hate"),
    Nudity("Nudity or sexual content", "nudity"),
    Violence("Violence", "violence"),
    Scam("Scam or fraud", "scam"),
    Other("Other", "other"),
}

/** The report sheet's rows, ascending alphabetical by label (founder's rule). */
fun liveReportReasons(): List<LiveReportReason> = LiveReportReason.entries.sortedBy { it.label.lowercase() }

/**
 * The server's contract code, from whichever path carried it: an HTTP 403
 * arrives as [AppError.Forbidden], a 2xx envelope with an error (or any
 * other status) as [AppError.Unknown] / [AppError.Server].
 */
fun AppError.liveCode(): String? = when (this) {
    is AppError.Forbidden -> code
    is AppError.Unknown -> code
    is AppError.Server -> code
    else -> null
}?.takeIf { it.isNotBlank() }

/** 403 on create/start for anyone outside `LIVE_PILOT_USER_IDS`. */
const val CODE_LIVE_NOT_ENABLED = "LIVE_NOT_ENABLED"
const val CODE_CHAT_MUTED = "CHAT_MUTED"
const val CODE_CHAT_BLOCKED_WORD = "CHAT_BLOCKED_WORD"

/** 403 on chat send for a user banned from this stream. */
const val CODE_CHAT_BANNED = "CHAT_BANNED"

/** 409 on a second report of the same target by the same reporter. */
const val CODE_ALREADY_REPORTED = "ALREADY_REPORTED"
