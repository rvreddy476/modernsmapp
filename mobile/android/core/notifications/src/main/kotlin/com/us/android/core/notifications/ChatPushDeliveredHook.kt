package com.us.android.core.notifications

/**
 * Message ticks (2026-10-22): a chat push reaching this device is what turns
 * the sender's one tick into two. Defined here, where pushes arrive, and
 * bound by `:feature:chat` (which depends on both this module and
 * `:core:chat`), so the dependency arrow never inverts. Optional: an app
 * without chat binds nothing and the push path is unchanged.
 *
 * Must return at once; the implementation batches and sends on its own scope.
 */
fun interface ChatPushDeliveredHook {
    fun onChatPush(conversationId: String, messageId: String?)
}

/** The push `type`s that announce a chat message (see [NotificationChannelSpec]). */
val CHAT_MESSAGE_PUSH_TYPES: Set<String> = setOf("message", "chat", "dm", "message_request")

/**
 * The (conversation, message) a chat push announces, or null when it is not
 * a chat message push. A push with a conversation id and no type is a chat
 * push too: chat-service's data map always names the conversation.
 */
fun chatPushTarget(data: Map<String, String>): Pair<String, String?>? {
    val conversationId = data[NotificationPresenter.KEY_CONVERSATION_ID]?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    val type = data[NotificationPresenter.KEY_TYPE]?.trim()?.lowercase()
    if (type != null && type.isNotEmpty() && type !in CHAT_MESSAGE_PUSH_TYPES) return null
    return conversationId to data[KEY_MESSAGE_ID]?.trim()?.takeIf { it.isNotEmpty() }
}

/** chat-service's push data carries the message id under this key. */
private const val KEY_MESSAGE_ID = "message_id"

/** Optional: only an app that ships chat binds a [ChatPushDeliveredHook]. */
@dagger.Module
@dagger.hilt.InstallIn(dagger.hilt.components.SingletonComponent::class)
abstract class ChatPushDeliveredHookModule {
    @dagger.BindsOptionalOf
    abstract fun optionalChatPushDeliveredHook(): ChatPushDeliveredHook
}
