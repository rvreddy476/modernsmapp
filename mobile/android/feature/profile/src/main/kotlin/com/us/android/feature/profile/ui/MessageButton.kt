package com.us.android.feature.profile.ui

import com.us.android.core.chat.data.MessageDecision
import com.us.android.core.chat.data.MessageDenyReason
import com.us.android.core.chat.data.MessagePermission
import com.us.android.core.common.error.AppError

/**
 * The Message button on someone else's profile (DM consent contract §4).
 *
 * graph-service decides; this only puts the decision into words:
 *
 *  - allow           -> "Message"
 *  - message_request -> "Send request", or "Requested" when the viewer's
 *                       request is already waiting (tapping opens it)
 *  - deny            -> a disabled "Message" with the reason, except
 *                       `unavailable` (a block, or an account that cannot be
 *                       reached), which hides the button so a block is
 *                       never revealed
 *
 * Until the decision is read — or when it cannot be read — the plain
 * "Message" stays, and the server answers the tap as it always has.
 */
sealed interface MessageButtonState {
    data object Hidden : MessageButtonState

    data class Enabled(val kind: MessageButtonKind) : MessageButtonState {
        val label: String get() = kind.label
    }

    data class Disabled(val explanation: String) : MessageButtonState {
        val label: String get() = MessageButtonKind.Message.label
    }
}

enum class MessageButtonKind(val label: String) {
    Message("Message"),
    SendRequest("Send request"),
    Requested("Requested"),
}

/** How a profile names its owner in these lines: @username, else their name. */
fun profileHandle(username: String, displayName: String): String =
    username.trim().removePrefix("@").takeIf { it.isNotEmpty() }?.let { "@$it" }
        ?: displayName.trim().ifEmpty { "this person" }

/** The button for [permission] (null = not read yet, or unreadable). */
fun messageButtonFor(
    permission: MessagePermission?,
    handle: String,
    hasPendingRequest: Boolean = false,
): MessageButtonState = when (permission?.decision) {
    null, MessageDecision.Allow -> MessageButtonState.Enabled(MessageButtonKind.Message)
    MessageDecision.Request -> MessageButtonState.Enabled(
        if (hasPendingRequest) MessageButtonKind.Requested else MessageButtonKind.SendRequest,
    )
    MessageDecision.Deny ->
        if (permission.reason == MessageDenyReason.UNAVAILABLE) {
            MessageButtonState.Hidden
        } else {
            MessageButtonState.Disabled(denyExplanation(permission.reason, handle))
        }
}

/** The line under a disabled Message button. A missing or unknown reason gets a plain one. */
fun denyExplanation(reason: String?, handle: String): String = when (reason) {
    MessageDenyReason.MESSAGES_OFF -> "$handle isn't taking messages"
    MessageDenyReason.NO_ONE -> "$handle doesn't accept messages"
    MessageDenyReason.FOLLOWERS_ONLY -> "Follow $handle to send a request"
    MessageDenyReason.PEOPLE_I_FOLLOW -> "$handle only accepts messages from people they follow"
    MessageDenyReason.YOU_PAUSED -> "Turn your messages on to send"
    else -> "You can't message $handle right now"
}

/**
 * Words for a refused start (create-conversation). The server's `reason`
 * in the error details explains a policy refusal the way the button would
 * have; the request rate limit and the 7-day cooldown after a deleted
 * request get their own lines. The cooldown line never says the request was
 * deleted: the requester is not told that.
 */
fun startRefusalText(error: AppError, handle: String): String =
    consentRefusalOrNull(error, handle) ?: "$handle isn't accepting messages from you."

/** [startRefusalText]'s specific lines, or null when [error] is not a consent refusal (a transient failure). */
fun consentRefusalOrNull(error: AppError, handle: String): String? {
    val code = when (error) {
        is AppError.Forbidden -> error.code
        is AppError.Unknown -> error.code
        is AppError.Server -> error.code
        else -> null
    }?.uppercase().orEmpty()
    val reason = (error as? AppError.Forbidden)?.details?.get("reason")?.trim()?.lowercase().orEmpty()
    val denyReason = MessageDenyReason.normalise(reason)
    return when {
        error is AppError.RateLimited || code == CODE_RATE_LIMITED ->
            "You've sent a lot of requests today. Try again tomorrow."
        code in COOLDOWN_CODES || reason in COOLDOWN_REASONS ->
            "You can't send $handle another request yet. Try again in a few days."
        denyReason != null -> denyExplanation(denyReason, handle)
        else -> null
    }
}

private const val CODE_RATE_LIMITED = "RATE_LIMITED"

/**
 * The cooldown, as message-service spells it. Today the server answers a
 * request inside the 7 days by returning the earlier request (so the sender
 * is never told); this line only covers a server that refuses instead.
 */
private val COOLDOWN_CODES = setOf("REQUEST_COOLDOWN")
private val COOLDOWN_REASONS = setOf("request_cooldown")

/** A start that failed for a passing reason: `503 RETRY_LATER` (the limiter is down) asks for a moment. */
fun startFailureText(error: AppError): String =
    if (error is AppError.Server && error.code.equals(CODE_RETRY_LATER, ignoreCase = true)) {
        "Try again in a moment."
    } else {
        "We couldn't open that conversation."
    }

private const val CODE_RETRY_LATER = "RETRY_LATER"
