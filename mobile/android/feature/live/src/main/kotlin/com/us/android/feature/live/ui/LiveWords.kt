package com.us.android.feature.live.ui

import com.us.android.core.common.error.AppError
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.formatCount
import com.us.android.feature.live.data.CODE_ALREADY_REPORTED
import com.us.android.feature.live.data.CODE_AUTHORITY_UNAVAILABLE
import com.us.android.feature.live.data.CODE_CHAT_BANNED
import com.us.android.feature.live.data.CODE_CHAT_BLOCKED_WORD
import com.us.android.feature.live.data.CODE_CHAT_MUTED
import com.us.android.feature.live.data.CODE_LIVE_NOT_ENABLED
import com.us.android.feature.live.data.CODE_STREAM_FULL
import com.us.android.feature.live.data.ChatRole
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveGateAction
import com.us.android.feature.live.data.LiveRequirementDto
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.REQ_ACCOUNT_AGE
import com.us.android.feature.live.data.REQ_ACTIVITY
import com.us.android.feature.live.data.REQ_ADULT
import com.us.android.feature.live.data.REQ_GOOD_STANDING
import com.us.android.feature.live.data.REQ_EMAIL_VERIFIED
import com.us.android.feature.live.data.REQ_PHONE_VERIFIED
import com.us.android.feature.live.data.RequirementState
import com.us.android.feature.live.data.chatAuthorName
import com.us.android.feature.live.data.liveCode
import com.us.android.feature.live.data.state

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
    // The server could not check a requirement and failed closed (503): nothing is wrong with the account.
    error.liveCode() == CODE_AUTHORITY_UNAVAILABLE ->
        GoLiveRefusal("We couldn't check your account just now. Try again in a moment.", canRetry = true)
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
fun watchJoinFailure(error: AppError): String = when {
    // A new streamer's viewer cap is reached (live-eligibility contract, 2026-10-02): not a ban, and it passes.
    error.liveCode() == CODE_STREAM_FULL -> "This stream is full right now. Try again in a little while."
    error is AppError.Forbidden -> "You can't watch this stream."
    error is AppError.NotFound -> "This stream isn't available."
    error is AppError.NoNetwork || error is AppError.Timeout -> "No connection. Check your network and try again."
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

// ── Who is speaking ─────────────────────────────────────────────────────

/** The small tag beside a host's or a moderator's name. A viewer has none. */
fun chatRoleLabel(role: ChatRole): String? = when (role) {
    ChatRole.Host -> "Host"
    ChatRole.Moderator -> "Mod"
    ChatRole.Viewer -> null
}

/** What the Founding creator badge is called, for the reader and the screen reader alike. */
const val FOUNDING_CREATOR_LABEL = "Founding creator"

/**
 * A person the moderation sheets list by user id (a moderator, a banned
 * user): the name their chat rows carried, else "Viewer". Never the id.
 */
fun personName(userId: String, people: Map<String, LiveChatAuthorDto>): String = chatAuthorName(people[userId])

// ── Not eligible yet ────────────────────────────────────────────────────

/** The "not yet" screen's heading and the line under it. Our own words, not YouTube's. */
const val LIVE_GATE_TITLE = "You're nearly ready to go live"
const val LIVE_GATE_DETAIL = "Finish what's left below and you can start streaming."

/** What "Learn more" unfolds, in place. */
const val LIVE_GATE_LEARN_MORE =
    "Live video reaches people as it happens, so we ask for a few things first: a verified email address, " +
        "being 18 or older, an account that has been around for a little while, and some posts or followers. " +
        "They keep live safe for the people watching. This list updates as soon as you meet them."

/** The words under the form while the new-streamer viewer cap applies. */
fun viewerCapNote(cap: Int): String? =
    if (cap > 0) "Your first streams can have up to ${formatCount(cap)} viewers at a time." else null

/** The primary button's label. */
fun gateActionLabel(action: LiveGateAction): String = when (action) {
    LiveGateAction.CreatePost -> "Create a post"
    LiveGateAction.VerifyEmail -> "Verify email"
    LiveGateAction.VerifyPhone -> "Verify phone number"
    LiveGateAction.CheckAgain -> "Check again"
}

/** One row of the "not yet" list: a tick, or what is still needed, in plain words. */
data class RequirementLine(val key: String, val state: RequirementState, val text: String)

/**
 * The rows, in the server's order. A requirement this build does not know is
 * still listed when it is not met — hiding it would leave a list of ticks
 * over a refusal — but says nothing once it is.
 */
fun requirementLines(requirements: List<LiveRequirementDto>): List<RequirementLine> =
    requirements.mapNotNull { requirement ->
        val text = requirementText(requirement) ?: return@mapNotNull null
        RequirementLine(key = requirement.key, state = requirement.state, text = text)
    }

private fun requirementText(requirement: LiveRequirementDto): String? = when (requirement.state) {
    RequirementState.Met -> metText(requirement.key)
    RequirementState.Unmet -> neededText(requirement)
    RequirementState.Unknown -> "${topicOf(requirement.key)}: we couldn't check this just now"
}

private fun metText(key: String): String? = when (key) {
    REQ_EMAIL_VERIFIED -> "Email verified"
    REQ_PHONE_VERIFIED -> "Phone number verified"
    REQ_ADULT -> "You're 18 or older"
    REQ_ACCOUNT_AGE -> "Your account is old enough"
    REQ_ACTIVITY -> "You have enough posts or followers"
    REQ_GOOD_STANDING -> "Your account is in good standing"
    else -> null
}

private fun neededText(requirement: LiveRequirementDto): String = when (requirement.key) {
    REQ_EMAIL_VERIFIED -> "Verify your email address"
    REQ_PHONE_VERIFIED -> "Verify your phone number"
    REQ_ADULT -> "You must be 18 or older to go live"
    REQ_ACCOUNT_AGE -> accountAgeText(requirement)
    REQ_ACTIVITY -> activityText(requirement)
    REQ_GOOD_STANDING -> "Your account can't go live right now"
    else -> "One more requirement isn't met yet"
}

private fun topicOf(key: String): String = when (key) {
    REQ_EMAIL_VERIFIED -> "Email address"
    REQ_PHONE_VERIFIED -> "Phone number"
    REQ_ADULT -> "Your age"
    REQ_ACCOUNT_AGE -> "Account age"
    REQ_ACTIVITY -> "Posts and followers"
    REQ_GOOD_STANDING -> "Account standing"
    else -> "One requirement"
}

/** "Your account must be 7 days old (5 days to go)". */
private fun accountAgeText(requirement: LiveRequirementDto): String {
    val needed = requirement.needed
    if (needed <= 0) return "Your account needs to be a little older"
    val unit = requirement.unit.trim().lowercase().ifEmpty { "days" }
    val left = needed - requirement.current.coerceAtLeast(0)
    val toGo = if (left > 0) " (${counted(left, unit)} to go)" else ""
    return "Your account must be ${counted(needed, unit)} old$toGo"
}

/** "Publish 3 posts or reach 10 followers (1 of 3 posts, 4 of 10 followers)". */
private fun activityText(requirement: LiveRequirementDto): String {
    val posts = requirement.posts?.takeIf { it.needed > 0 }
    val followers = requirement.followers?.takeIf { it.needed > 0 }
    val asks = listOfNotNull(
        posts?.let { "publish ${counted(it.needed, "posts")}" },
        followers?.let { "reach ${counted(it.needed, "followers")}" },
    )
    if (asks.isEmpty()) return "Publish a few posts or gain some followers"
    val progress = listOfNotNull(
        posts?.let { "${it.current.coerceAtLeast(0)} of ${counted(it.needed, "posts")}" },
        followers?.let { "${it.current.coerceAtLeast(0)} of ${counted(it.needed, "followers")}" },
    )
    val sentence = asks.joinToString(" or ").replaceFirstChar { it.uppercase() }
    return "$sentence (${progress.joinToString(", ")})"
}

/** "1 day", "7 days": [pluralUnit] loses its final "s" for one. */
private fun counted(count: Int, pluralUnit: String): String =
    if (count == 1) "1 ${pluralUnit.removeSuffix("s")}" else "$count $pluralUnit"
