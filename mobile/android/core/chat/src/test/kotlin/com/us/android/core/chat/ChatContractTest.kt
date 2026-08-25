package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Pins the chat DTOs to bytes the server actually sent.
 *
 * Every payload below was captured on 2026-08-21 through the gateway with a
 * real JWT — see `prompt/slice-b-chat-contracts.md`. They are here because the
 * field names are not the ones a reasonable person would guess: the message id
 * is `msg_id`, the group field is `title`, and the direct-conversation field is
 * `other_user_id`. Each of those cost a 400 during capture.
 */
class ChatContractTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `a conversation decodes with its members and display names`() {
        val envelope: ApiEnvelope<ConversationDto> = json.decodeFromString(CONVERSATION)

        val conversation = envelope.data!!
        assertThat(conversation.id).isEqualTo("4988558d-9914-41b8-9d74-e76cc7c94e96")
        assertThat(conversation.type).isEqualTo("group")
        assertThat(conversation.title).isEqualTo("Slice B capture")
        assertThat(conversation.isRequest).isFalse()
        assertThat(conversation.members).hasSize(2)
        // The load-bearing difference from comments: the name is on the wire,
        // so no surface needs a per-row profile call.
        assertThat(conversation.members.map { it.displayName })
            .containsExactly("Alpha Btest", "Bravo Btest")
        assertThat(conversation.members.first().role).isEqualTo("admin")
    }

    @Test
    fun `a message list decodes and exposes an opaque next cursor`() {
        val envelope: ApiEnvelope<List<MessageDto>> = json.decodeFromString(MESSAGE_PAGE)

        val message = envelope.data!!.single()
        // `msg_id`, not `id`. A DTO expecting `id` decodes to empty and every
        // row collides on a blank key.
        assertThat(message.msgId).isEqualTo("70efd586-df95-4bca-a63d-058ef9549bff")
        assertThat(message.senderDisplayName).isEqualTo("Bravo Btest")
        assertThat(message.text).isEqualTo("reply from Bravo")
        assertThat(message.type).isEqualTo("text")
        assertThat(message.bucket).isEqualTo("202608")

        assertThat(envelope.meta?.nextCursor).isEqualTo(EXPECTED_CURSOR)
    }

    /**
     * The SEND response omits `sender_display_name` while the list includes it.
     * A client that assumed symmetry would render a just-sent message with a
     * blank name.
     */
    @Test
    fun `the send response has no sender display name`() {
        val envelope: ApiEnvelope<MessageDto> = json.decodeFromString(SEND_RESPONSE)

        val message = envelope.data!!
        assertThat(message.msgId).isEqualTo("6d23c6d5-0509-4016-84f1-4219739ba450")
        assertThat(message.senderDisplayName).isNull()
        assertThat(message.text).isEqualTo("first message from Alpha")
    }

    @Test
    fun `an empty inbox decodes to an empty list, not null`() {
        val envelope: ApiEnvelope<List<ConversationDto>> = json.decodeFromString("""{"data":[]}""")
        assertThat(envelope.data).isEmpty()
    }

    private companion object {
        // Split only to satisfy the line-length rule; this is one opaque token.
        const val EXPECTED_CURSOR = "eyJiIjoiMjAyNjA4IiwidCI6IjIwMjYtMDgtMjFUMDk6" +
            "MDk6MTkuMDg0WiIsIm0iOiI3MGVmZDU4Ni1kZjk1LTRiY2EtYTYzZC0wNThlZjk1NDliZmYifQ"

        const val CONVERSATION = """
        {"data":{"id":"4988558d-9914-41b8-9d74-e76cc7c94e96","type":"group","title":"Slice B capture",
        "created_by":"ea606afa-f5da-468c-b093-247588c15d74","is_request":false,
        "members":[{"user_id":"ea606afa-f5da-468c-b093-247588c15d74","role":"admin","joined_at":"2026-08-21T09:08:48.942859Z","display_name":"Alpha Btest"},
                   {"user_id":"3922df6c-3661-41fb-a794-b646c17c299e","role":"member","joined_at":"2026-08-21T09:08:48.942859Z","display_name":"Bravo Btest"}],
        "created_at":"2026-08-21T09:08:48.942859Z","updated_at":"2026-08-21T09:08:48.942859Z"}}
        """

        const val MESSAGE_PAGE = """
        {"data":[{"conversation_id":"4988558d-9914-41b8-9d74-e76cc7c94e96","bucket":"202608",
        "ts":"2026-08-21T09:09:19.084Z","msg_id":"70efd586-df95-4bca-a63d-058ef9549bff",
        "sender_id":"3922df6c-3661-41fb-a794-b646c17c299e","sender_display_name":"Bravo Btest",
        "type":"text","text":"reply from Bravo","created_at":"2026-08-21T09:09:19.084Z"}],
        "meta":{"next_cursor":"eyJiIjoiMjAyNjA4IiwidCI6IjIwMjYtMDgtMjFUMDk6MDk6MTkuMDg0WiIsIm0iOiI3MGVmZDU4Ni1kZjk1LTRiY2EtYTYzZC0wNThlZjk1NDliZmYifQ"}}
        """

        const val SEND_RESPONSE = """
        {"data":{"conversation_id":"4988558d-9914-41b8-9d74-e76cc7c94e96","bucket":"202608",
        "ts":"2026-08-21T09:09:04.212084Z","msg_id":"6d23c6d5-0509-4016-84f1-4219739ba450",
        "sender_id":"ea606afa-f5da-468c-b093-247588c15d74","type":"text",
        "text":"first message from Alpha","created_at":"2026-08-21T09:09:04.212084Z"}}
        """
    }
}
