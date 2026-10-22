package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Message ticks (2026-10-22): which pushes ack delivery, and what they name. */
class ChatPushDeliveredTest {

    @Test
    fun `a dm push names its conversation and message`() {
        val target = chatPushTarget(mapOf("type" to "dm", "conversation_id" to "c1", "message_id" to "m1"))
        assertThat(target).isEqualTo("c1" to "m1")
    }

    @Test
    fun `a chat push without a message id still acks the conversation`() {
        assertThat(chatPushTarget(mapOf("type" to "message_request", "conversation_id" to "c1"))).isEqualTo("c1" to null)
        assertThat(chatPushTarget(mapOf("conversation_id" to "c1", "message_id" to " "))).isEqualTo("c1" to null)
    }

    @Test
    fun `other pushes and pushes without a conversation ack nothing`() {
        assertThat(chatPushTarget(mapOf("type" to "incoming_call", "conversation_id" to "c1"))).isNull()
        assertThat(chatPushTarget(mapOf("type" to "like", "conversation_id" to "c1"))).isNull()
        assertThat(chatPushTarget(mapOf("type" to "dm"))).isNull()
        assertThat(chatPushTarget(mapOf("type" to "dm", "conversation_id" to " "))).isNull()
    }
}
