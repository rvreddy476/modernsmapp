package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.epochMillisOrNull
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.chat.data.toDomain
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Chat audit H2: a disappearing message's `expires_at` reaches the client on
 * every history row and every live frame, so the cache can expire it with no
 * socket listening.
 */
class MessageExpiryTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `a history row carries its expiry`() {
        val dto = json.decodeFromString(
            MessageDto.serializer(),
            """{"msg_id":"m1","conversation_id":"c1","sender_id":"u2","text":"hi",
               "created_at":"2026-11-07T10:00:00Z","expires_at":"2026-11-08T10:00:00Z"}""",
        )

        assertThat(dto.toDomain().expiresAtMillis).isEqualTo(1_794_132_000_000L)
    }

    @Test
    fun `a live frame carries its expiry`() {
        val frame = """
            {"type":"message","payload":{
              "conversation_id":"c1","message_id":"m1","sender_id":"u2","type":"text","text":"hi",
              "created_at":"2026-11-07T10:00:00Z","expires_at":"2026-11-08T10:00:00+00:00"}}
        """.trimIndent()

        val message = (parseChatFrame(json, frame) as ChatSocketEvent.MessageReceived).message

        assertThat(message.expiresAtMillis).isEqualTo(1_794_132_000_000L)
    }

    @Test
    fun `no timer, or an unreadable one, means the message never expires here`() {
        val dto = json.decodeFromString(MessageDto.serializer(), """{"msg_id":"m1","conversation_id":"c1"}""")

        assertThat(dto.toDomain().expiresAtMillis).isNull()
        assertThat(epochMillisOrNull("tomorrow")).isNull()
        assertThat(epochMillisOrNull(" 2026-11-08T10:00:00.250Z ")).isEqualTo(1_794_132_000_250L)
    }

    @Test
    fun `a message is expired from its expiry instant on`() {
        val message = Message("m1", "c1", "u2", null, "hi", "2026-11-07T10:00:00Z", expiresAtMillis = 1_000L)

        assertThat(message.isExpiredAt(999L)).isFalse()
        assertThat(message.isExpiredAt(1_000L)).isTrue()
        assertThat(message.copy(expiresAtMillis = null).isExpiredAt(Long.MAX_VALUE)).isFalse()
    }
}
