package com.us.android.core.common.chat

/**
 * A product's say over the tone of messages in ITS OWN conversations.
 *
 * ## WHY THIS INVERSION EXISTS
 *
 * Pulse (dating) asks its server whether a message might come across as
 * unkind — before one is sent, and for one received — but the messages live in
 * chat, and `:feature:dating` may never depend on `:feature:chat` (nor the
 * other way round). So chat depends on this interface, which lives in
 * `:core:common` (both already depend on it), and `:app` binds the dating
 * implementation. Chat knows there may be a check; it never learns whose.
 *
 * ## NOTHING TO DO IS THE DEFAULT
 *
 * Every member answers "nothing to do" for a conversation the implementation
 * does not own, and for every failure. A check never stops a message from
 * being sent and never hides one by mistake. Nothing here throws, except
 * cancellation.
 */
interface ConversationKindness {

    /**
     * Chat loaded [conversationId], and its server said which product it
     * belongs to — [sourceApp] ("dating" for a Pulse match's chat) and that
     * product's own id for it — or nothing, for every other conversation.
     * Called before [appliesTo] is asked; a hint, never a requirement.
     */
    fun conversationSource(conversationId: String, sourceApp: String?, sourceId: String?) = Unit

    /** Whether [conversationId] gets kindness checks at all. False for every other conversation. */
    suspend fun appliesTo(conversationId: String): Boolean

    /** Before a send: true only when [text] might come across as unkind. Any doubt or failure: false. */
    suspend fun mightBeUnkind(conversationId: String, text: String): Boolean

    /**
     * A text someone ELSE sent: true when it should stay covered until tapped.
     * Asked once per [messageId]; the implementation keeps the answer.
     */
    suspend fun shouldCover(conversationId: String, messageId: String, text: String): Boolean

    /**
     * The viewer answered "did this bother you?" about [messageId] in
     * [conversationId]. True when the report flow should be offered next. A
     * "no" uncovers that message for good.
     */
    suspend fun bothered(conversationId: String, messageId: String, bothered: Boolean): Boolean

    /** For every conversation: nothing to check. */
    object None : ConversationKindness {
        override suspend fun appliesTo(conversationId: String): Boolean = false

        override suspend fun mightBeUnkind(conversationId: String, text: String): Boolean = false

        override suspend fun shouldCover(conversationId: String, messageId: String, text: String): Boolean = false

        override suspend fun bothered(conversationId: String, messageId: String, bothered: Boolean): Boolean = false
    }
}
