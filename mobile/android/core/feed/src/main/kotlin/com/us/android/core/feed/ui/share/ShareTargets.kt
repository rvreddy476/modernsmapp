package com.us.android.core.feed.ui.share

import com.us.android.core.chat.data.Conversation
import com.us.android.core.common.error.AppError
import com.us.android.core.ui.UsShareTarget

/*
  The share sheet's "Send in Messages" picker (2026-10-17): which
  conversations a post may be sent into, what each is called, and what a
  refused send says. Pure, so every rule is a table test.
*/

/**
 * The conversations a post can be sent into, most recent first, narrowed by
 * [query] (a name match, case-insensitive):
 *
 *  - direct and group chats only — a broadcast channel or anything newer is
 *    not offered;
 *  - never a message REQUEST: message-service refuses a post as the first
 *    message of one ("posts are not allowed until the request is accepted");
 *  - never a Pulse match's chat (`source_app` "dating"): dating keeps its own
 *    inbox, and a post from the social app has no place in it.
 *
 * Named as the inbox names them ([Conversation.displayTitle]). With no query
 * the list is the [RECENT_TARGETS] most recent; a search looks at all of them.
 */
fun shareTargets(conversations: List<Conversation>, viewerId: String, query: String): List<UsShareTarget> {
    val needle = query.trim()
    val targets = conversations
        .asSequence()
        .filter { it.id.isNotBlank() && !it.isRequest && it.sourceApp != DATING_SOURCE_APP }
        .filter { it.type == DIRECT || it.type == GROUP }
        .distinctBy { it.id }
        .sortedByDescending { it.lastMessageAt?.takeIf { at -> at.isNotBlank() } ?: it.updatedAt }
        .map { conversation ->
            UsShareTarget(
                id = conversation.id,
                name = conversation.displayTitle(viewerId),
                isGroup = conversation.type == GROUP,
                avatarUrl = conversation.avatarUrl?.takeIf { it.isNotBlank() },
            )
        }
        .filter { needle.isEmpty() || it.name.contains(needle, ignoreCase = true) }
    return if (needle.isEmpty()) targets.take(RECENT_TARGETS).toList() else targets.toList()
}

/**
 * What a refused send says, by message-service's code (handler.go
 * writeMessagingError) — the web share sheet's words, so both read the same.
 */
fun sendPostErrorText(error: AppError): String {
    val code = when (error) {
        is AppError.Forbidden -> error.code
        is AppError.Server -> error.code
        is AppError.Unknown -> error.code
        else -> null
    }
    val status = (error as? AppError.Unknown)?.statusCode ?: (error as? AppError.Server)?.statusCode
    return when {
        code == "INVALID_REQUEST_MESSAGE" || status == HTTP_UNPROCESSABLE ->
            "A post can't start a message request. Send a message first — you can share posts once they accept."
        code == "AWAITING_REQUEST_ACCEPTANCE" ->
            "They haven't accepted your message request yet. You can share posts once they do."
        code == "MESSAGING_NOT_ALLOWED" || code == "FIRST_MOVE_PENDING" || error is AppError.Forbidden ->
            "This chat isn't accepting messages from you."
        error is AppError.RateLimited -> "You're sending too fast. Wait a moment and try again."
        error is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        else -> "Couldn't send the post. Try again."
    }
}

/** `source_app` on a Pulse match's conversation. */
const val DATING_SOURCE_APP = "dating"

/** How many conversations the picker lists before the viewer searches. */
const val RECENT_TARGETS = 50

private const val DIRECT = "direct"
private const val GROUP = "group"
private const val HTTP_UNPROCESSABLE = 422
