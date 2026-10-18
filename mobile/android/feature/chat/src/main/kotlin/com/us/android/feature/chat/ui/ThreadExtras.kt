package com.us.android.feature.chat.ui

import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ForwardResult
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.ReactionGroup
import com.us.android.core.chat.data.ReadSummary
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.profile.data.PrivacySettingsRepository
import com.us.android.core.profile.data.ProfileRepository
import javax.inject.Inject

/**
 * The thread's chat-parity collaborators, bundled (2026-10-18) so the
 * ViewModel's constructor — which tests call by name — gains ONE optional
 * argument instead of five. Null (the tests' default) turns every parity
 * feature off and leaves the thread exactly as it was.
 */
class ThreadExtras @Inject constructor(
    val parity: ChatParityRepository,
    val privacy: PrivacySettingsRepository,
    val profiles: ProfileRepository,
    val followGraph: FollowGraph,
)

/** The intro card over an empty direct thread: who this is, before anything is said. */
data class ThreadIntro(
    val userId: String,
    val name: String,
    val username: String,
    val avatarUrl: String?,
    val followers: Int?,
    val posts: Int?,
)

/** "Who reacted" for one message. */
data class ReactionSheetState(val messageId: String, val groups: List<ReactionGroup>? = null, val failed: Boolean = false)

/** The message a sheet is acting on: forward, report or edit. */
sealed interface MessageSheet {
    data class Forward(val message: Message, val targets: List<Conversation>? = null, val sending: Boolean = false) : MessageSheet
    data class Report(val message: Message, val sending: Boolean = false) : MessageSheet
    data class Edit(val message: Message, val text: String, val saving: Boolean = false) : MessageSheet
    data class SeenBy(val summary: ReadSummary? = null, val failed: Boolean = false) : MessageSheet
}

/**
 * The conversations a message may be forwarded into: accepted direct and
 * group chats other than this one, never a request or a Pulse chat, most
 * recent first.
 */
fun forwardTargets(conversations: List<Conversation>, currentId: String): List<Conversation> =
    com.us.android.core.chat.data.orderInbox(
        conversations.filter {
            it.id != currentId && !it.isRequest && !it.isDatingChat && (it.type == "direct" || it.type == "group")
        },
    )

/** True when [message] can be forwarded: something to carry, and not unsent or still sending. */
fun Message.canForward(): Boolean = !deleted && !pending && id.isNotBlank() &&
    (text.isNotBlank() || mediaId != null || postId != null || voice != null || sticker != null || gif != null)

/** True when the viewer may edit [message]: their own, a plain text, addressable. */
fun Message.canEdit(viewerId: String): Boolean =
    senderId == viewerId && viewerId.isNotBlank() && !deleted && addressable &&
        type == com.us.android.core.chat.data.TEXT_MESSAGE && text.isNotBlank() && mediaId == null && postId == null

/** Blur rule (CONTRACT §7): sensitive media is covered when the viewer asked, and ALWAYS inside a request. */
fun shouldBlur(message: Message, sensitiveFilterOn: Boolean, isRequest: Boolean, revealed: Boolean): Boolean =
    !revealed && message.media?.sensitive == true && (sensitiveFilterOn || isRequest)

/** The idempotent forward call result for a ViewModel; a pure wrapper so the test can pin the summary. */
suspend fun forwardOnce(
    parity: ChatParityRepository,
    conversationId: String,
    message: Message,
    targets: List<String>,
): AppResult<List<ForwardResult>> = parity.forward(conversationId, message.id, targets, ChatRepository.newIdempotencyKey())
