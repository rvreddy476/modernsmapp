package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.SHARED_POST_PREVIEW
import com.us.android.core.chat.data.UNNAMED_CONVERSATION
import com.us.android.core.chat.data.inboxPreview
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * A post shared into a chat (2026-10-17): the card the app sends, the card
 * it reads back, and the name a conversation goes by in the picker and the
 * inbox.
 */
class PostShareChatTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: ChatRepository

    /** Mirrors NetworkModule.provideJson(): no encodeDefaults. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChatApi::class.java)
        repository = ChatRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(MockResponse.Builder().code(200).body(body).build())
    }

    /** The body verified live against the dev gateway, with the caption under its real name, `text`. */
    @Test
    fun `sending a post posts the card body under the caller's key`() = runTest {
        enqueue(
            """{"data":{"conversation_id":"c1","bucket":"202610","ts":"2026-10-17T09:00:00Z","msg_id":"m1",""" +
                """"sender_id":"me","type":"post","post_id":"p-1","created_at":"2026-10-17T09:00:00Z"}}""",
        )

        val result = repository.sendPost(conversationId = "c1", postId = "p-1", idempotencyKey = "k-1")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c1/messages")
        assertThat(request.headers["Idempotency-Key"]).isEqualTo("k-1")
        assertThat(request.body?.utf8()).isEqualTo("""{"type":"post","text":"","post_id":"p-1"}""")
        assertThat((result as AppResult.Success).data.postId).isEqualTo("p-1")
    }

    @Test
    fun `a post card read from history keeps its post id, and a text message has none`() = runTest {
        enqueue(
            """{"data":[""" +
                """{"conversation_id":"c1","bucket":"202610","ts":"t2","msg_id":"m2","sender_id":"u2",""" +
                """"sender_display_name":"Ana","type":"post","post_id":"p-9","created_at":"t2"},""" +
                """{"conversation_id":"c1","bucket":"202610","ts":"t1","msg_id":"m1","sender_id":"u2",""" +
                """"type":"text","text":"hi","created_at":"t1"}]}""",
        )

        val page = (repository.messages("c1") as AppResult.Success).data

        assertThat(page.items.map { it.postId }).containsExactly("p-9", null).inOrder()
    }

    @Test
    fun `a captionless post card says Shared a post in the inbox, a captioned one its caption`() {
        val card = Message(
            id = "m1",
            conversationId = "c1",
            senderId = "u",
            senderDisplayName = null,
            text = "",
            createdAt = "",
            postId = "p",
        )

        assertThat(inboxPreview(card)).isEqualTo(SHARED_POST_PREVIEW)
        assertThat(inboxPreview(card.copy(text = "look"))).isEqualTo("look")
        assertThat(inboxPreview(card.copy(postId = null, text = "hi"))).isEqualTo("hi")
    }

    private fun member(id: String, name: String) = ConversationMember(id, "member", name, null)

    private fun conversation(title: String?, vararg members: ConversationMember) = Conversation(
        id = "c1",
        type = "group",
        title = title,
        isRequest = false,
        members = members.toList(),
        updatedAt = "",
    )

    @Test
    fun `an unnamed chat is called after every other member with a name`() {
        val chat = conversation(null, member("me", "Me"), member("a", ""), member("b", "Ana"), member("c", " Ben "))

        assertThat(chat.displayTitle("me")).isEqualTo("Ana, Ben")
    }

    @Test
    fun `a title wins, and Conversation is only for a chat with no name known`() {
        assertThat(conversation("Trip", member("a", "Ana")).displayTitle("me")).isEqualTo("Trip")
        assertThat(conversation("  ", member("a", "Ana")).displayTitle("me")).isEqualTo("Ana")
        assertThat(conversation(null, member("me", "Me"), member("a", "")).displayTitle("me"))
            .isEqualTo(UNNAMED_CONVERSATION)
        assertThat(conversation(null).displayTitle("me")).isEqualTo("Conversation")
    }
}
