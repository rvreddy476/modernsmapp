package com.us.android.feature.live.ui

import com.us.android.core.common.error.AppError
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.formatCount
import com.us.android.feature.live.data.CODE_ALREADY_REPORTED
import com.us.android.feature.live.data.CODE_CHAT_BANNED
import com.us.android.feature.live.data.CODE_CHAT_BLOCKED_WORD
import com.us.android.feature.live.data.CODE_CHAT_MUTED
import com.us.android.feature.live.data.CODE_LIVE_NOT_ENABLED
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.liveCode

/*
 * Every sentence the live screens say about state and failure, in one place
 * per class of failure so the host screen and the viewer screen cannot drift.
 */

/** Why going live did not happen, and whether trying again could help. */
data class GoLiveRefusal(val message: String, val canRetry: Boolean)

/** Copy pinned by the live-fix contract (2026-10-01) for anyone outside the pilot allowlist. */
const val LIVE_PILOT_COPY = "Going live is in a closed pilot right now."

/** The one wording for a refused or failed create/start. */
fun goLiveRefusal(error: AppError): GoLiveRefusal = when {
    error.liveCode() == CODE_LIVE_NOT_ENABLED -> GoLiveRefusal(LIVE_PILOT_COPY, canRetry = false)
    error is AppError.Forbidden -> GoLiveRefusal("You can't go live right now.", canRetry = false)
    error is AppError.NoNetwork || error is AppError.Timeout ->
        GoLiveRefusal("No connection. Check your network and try again.", canRetry = true)
    error is AppError.RateLimited -> GoLiveRefusal("Too many tries. Wait a moment and try again.", canRetry = true)
    else -> GoLiveRefusal("Couldn't go live. Try again.", canRetry = true)
}

/** The host's LiveKit connection or camera failed after the server said yes. */
const val HOST_MEDIA_FAILED_COPY = "Couldn't connect your camera to the stream. Try again."

/** The small status badge. Null while there is nothing truthful to say. */
fun statusPillLabel(status: LiveStatus): String? = when (status) {
    LiveStatus.Scheduled -> "SCHEDULED"
    LiveStatus.Starting -> "STARTING"
    LiveStatus.Live -> "LIVE"
    LiveStatus.Reconnecting -> "RECONNECTING"
    LiveStatus.Ended -> "ENDED"
    LiveStatus.Failed -> "FAILED"
    LiveStatus.Unknown -> null
}

/** "12 watching" — the server's count, which already leaves the host out. */
fun viewerCountLabel(count: Int): String = "${formatCount(count.coerceAtLeast(0))} watching"

/** The banner while the stream is not (yet, or any more) on air. */
data class StatusCopy(val title: String, val detail: String)

/** The host's own screen, by status. */
fun hostStatusCopy(status: LiveStatus, reason: EndedReason): StatusCopy? = when (status) {
    LiveStatus.Starting, LiveStatus.Scheduled, LiveStatus.Unknown ->
        StatusCopy("Starting…", "You're not live yet. Viewers see you once your camera reaches the stream.")
    LiveStatus.Reconnecting ->
        StatusCopy("Reconnecting…", "Your connection dropped. Viewers are waiting; stay on this screen.")
    LiveStatus.Ended -> StatusCopy("Stream ended", hostEndedDetail(reason))
    LiveStatus.Failed -> StatusCopy("Stream didn't start", hostFailedDetail(reason))
    LiveStatus.Live -> null
}

/** A viewer's screen, by status. */
fun viewerStatusCopy(status: LiveStatus, reason: EndedReason): StatusCopy? = when (status) {
    LiveStatus.Scheduled -> StatusCopy("Not live yet", "This stream hasn't started.")
    LiveStatus.Starting, LiveStatus.Unknown -> StatusCopy("Starting soon", "The host is getting ready.")
    LiveStatus.Reconnecting -> StatusCopy("Reconnecting…", "The host's connection dropped. Hang on.")
    LiveStatus.Ended -> StatusCopy("Stream ended", viewerEndedDetail(reason))
    LiveStatus.Failed -> StatusCopy("Stream didn't start", "The host's video never arrived.")
    LiveStatus.Live -> null
}

private fun hostEndedDetail(reason: EndedReason): String = when (reason) {
    EndedReason.HostEnded -> "You ended the stream."
    EndedReason.HostLost -> "Your connection was lost for too long, so the stream ended."
    EndedReason.RoomFinished -> "The live room closed."
    EndedReason.AdminStopped -> "A moderator stopped your stream."
    EndedReason.NoMedia -> "Your camera never reached the stream."
    EndedReason.Unknown -> "Your broadcast has finished."
}

private fun hostFailedDetail(reason: EndedReason): String = when (reason) {
    EndedReason.AdminStopped -> "A moderator stopped your stream."
    else -> "Your camera never reached the stream, so it didn't go live."
}

private fun viewerEndedDetail(reason: EndedReason): String = when (reason) {
    EndedReason.HostEnded -> "The host ended the stream."
    EndedReason.HostLost -> "The host lost their connection."
    EndedReason.RoomFinished -> "The live room closed."
    EndedReason.AdminStopped -> "This stream was stopped by a moderator."
    EndedReason.NoMedia -> "The host's video never arrived."
    EndedReason.Unknown -> "This broadcast has finished."
}

/** Why a viewer could not join. */
fun watchJoinFailure(error: AppError): String = when (error) {
    is AppError.Forbidden -> "You can't watch this stream."
    is AppError.NotFound -> "This stream isn't available."
    is AppError.NoNetwork, is AppError.Timeout -> "No connection. Check your network and try again."
    else -> "Couldn't join the stream."
}

/** Why a chat message was not sent. */
fun chatSendRefusal(error: AppError): String = when {
    error.liveCode() == CODE_CHAT_MUTED -> "You've been muted in this chat."
    error.liveCode() == CODE_CHAT_BLOCKED_WORD -> "That message has a word the host has blocked."
    error.liveCode() == CODE_CHAT_BANNED -> "You've been banned from this chat."
    error is AppError.RateLimited -> "You're sending messages too fast. Slow down."
    // A ban from this stream (or from live everywhere) arrives as a 403.
    error is AppError.Forbidden -> "You can't chat in this stream."
    error is AppError.NoNetwork || error is AppError.Timeout -> "No connection. Your message wasn't sent."
    else -> "Couldn't send your message."
}

/** Why a host or moderator action did not go through. */
fun moderationFailure(error: AppError): String = when (error) {
    is AppError.Forbidden -> "Only the host and moderators can do that."
    is AppError.NoNetwork, is AppError.Timeout -> "No connection. Try again."
    else -> "That didn't go through. Try again."
}

/**
 * A refused report. One report per reporter per target: a repeat is
 * `409 ALREADY_REPORTED` and reads as done; everything else offers "Try again".
 */
fun reportStateFor(error: AppError): UsPostReportState =
    if (error.liveCode() == CODE_ALREADY_REPORTED) {
        UsPostReportState.AlreadyReported
    } else {
        UsPostReportState.Failed
    }

/** "user a1b2c3": chat rows carry only a user id; this is how the host tells people apart. */
fun shortUserLabel(userId: String): String = "user " + userId.replace("-", "").take(SHORT_ID_LENGTH)

private const val SHORT_ID_LENGTH = 6
