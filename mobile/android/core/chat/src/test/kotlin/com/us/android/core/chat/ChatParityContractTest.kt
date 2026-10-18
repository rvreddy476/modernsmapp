package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatParityApi
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.ForwardOutcome
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.MessageReportReason
import com.us.android.core.chat.data.SHARED_MEDIA
import com.us.android.core.chat.data.SHARED_POSTS
import com.us.android.core.chat.data.toDomain
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.chat.data.ownStatusLabel
import com.us.android.core.chat.data.requestTitle
import com.us.android.core.chat.data.streakLabel
import com.us.android.core.chat.data.unreadPillText
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
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
 * The chat-parity DTOs against the GOLDEN fixtures message-service commits
 * (c8ab1de7, `internal/http/testdata/contracts/chat/`), copied byte for byte
 * into `src/test/resources/contracts/chat/`. Decoded with the production
 * `Json` settings (NetworkModule) and, for the routes, through Retrofit
 * against a MockWebServer serving the fixture — the bytes the app will see.
 */
class ChatParityContractTest {

    /** NetworkModule.provideJson, exactly. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var parity: ChatParityRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChatParityApi::class.java)
        parity = ChatParityRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun fixture(name: String): String =
        requireNotNull(javaClass.classLoader?.getResource("contracts/chat/$name.json")) { "missing fixture $name" }.readText()

    private fun serve(name: String) = server.enqueue(MockResponse.Builder().code(200).body(fixture(name)).build())

    private fun message(name: String) =
        json.decodeFromString<ApiEnvelope<MessageDto>>(fixture(name)).data!!.toDomain()

    @Test
    fun `the inbox list carries unread counts, peer, streak and last-message status`() {
        val rows = json.decodeFromString<ApiEnvelope<List<ConversationDto>>>(fixture("conversations_list")).data!!
            .map { it.toDomain() }
        val group = rows.first { it.type == "group" }
        val direct = rows.first { it.type == "direct" }

        assertThat(group.peer).isNull()
        assertThat(group.streak).isNull()
        assertThat(group.lastMessage?.ownStatus).isEqualTo("sent")

        assertThat(direct.unreadCount).isEqualTo(9)
        assertThat(unreadPillText(direct.unreadCount, direct.hasUnread)).isEqualTo("9")
        assertThat(direct.peer?.username).isEqualTo("asha")
        assertThat(direct.peer?.avatarUrl).startsWith("https://cdn.atpost.test/")
        assertThat(direct.peer?.isOnline).isFalse()
        assertThat(direct.peer?.lastActiveAt).isNull()
        assertThat(streakLabel(direct.streak)).isEqualTo("5")
        assertThat(direct.alertTone).isEqualTo("default")
        assertThat(direct.muteCalls).isFalse()
    }

    @Test
    fun `the conversation detail reads Seen for the viewer's own read message`() {
        val detail = json.decodeFromString<ApiEnvelope<ConversationDto>>(fixture("conversation_detail")).data!!.toDomain()
        assertThat(ownStatusLabel(detail.lastMessage, viewerId = "0a000000-0000-4000-8000-00000000000a")).isEqualTo("Seen")
        assertThat(ownStatusLabel(detail.lastMessage, viewerId = "0b000000-0000-4000-8000-00000000000b")).isNull()
        assertThat(detail.peer?.displayName).isEqualTo("Bilal")
    }

    @Test
    fun `rich messages decode every typed field`() {
        val voice = message("message_voice")
        assertThat(voice.type).isEqualTo("voice")
        assertThat(voice.voice?.durationMs).isEqualTo(4200)
        assertThat(voice.voice?.waveform).containsExactly(0, 40, 255, 12).inOrder()
        assertThat(voice.media?.isAudio).isTrue()
        // CONTRACT deviation honoured: sensitive is always null for now.
        assertThat(voice.media?.sensitive).isNull()
        assertThat(voice.text).isEmpty()

        val gif = message("message_gif")
        assertThat(gif.gif?.provider).isEqualTo("giphy")
        // The provider's url exactly — query string and all.
        assertThat(gif.gif?.url).isEqualTo(
            "https://media2.giphy.com/media/v1.Y2lkPTc5/3o7aD2saalBwwftBIY/giphy.gif?cid=79&rid=giphy.gif&ct=g",
        )
        assertThat(gif.gif?.stillUrl).endsWith("giphy_s.gif")
        assertThat(gif.gif?.width).isEqualTo(480)

        val media = message("message_media")
        assertThat(media.media?.kind).isEqualTo("image")
        assertThat(media.media?.width).isEqualTo(1080)
        assertThat(media.media?.height).isEqualTo(1350)

        val note = message("message_note_reply")
        assertThat(note.replyKind).isEqualTo("note")
        assertThat(note.replyToPreview).isEqualTo("On a train 🚆")

        assertThat(message("message_sticker").sticker?.width).isEqualTo(512)
        assertThat(message("message_profile").profileUserId).isEqualTo("0d000000-0000-4000-8000-00000000000d")
        assertThat(message("message_sound").soundId).isEqualTo("00000000-0000-4000-8000-000000000003")
        assertThat(message("message_live").streamId).isEqualTo("00000000-0000-4000-8000-000000000003")
        assertThat(message("message_live").forwarded).isFalse()
    }

    @Test
    fun `forward maps each target's outcome`() = runTest {
        serve("forward_result")
        val results = (parity.forward("c", "m", listOf("a", "b"), "k") as AppResult.Success).data
        assertThat(results.map { it.outcome }).containsExactly(ForwardOutcome.SENT, ForwardOutcome.UNAVAILABLE).inOrder()
        assertThat(results.first().messageId).isEqualTo("00000000-0000-4000-8000-000000000002")
        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c/messages/m/forward")
        assertThat(request.body?.utf8()).isEqualTo("""{"conversation_ids":["a","b"],"idempotency_key":"k"}""")
    }

    @Test
    fun `reactions, read summary and stickers decode`() = runTest {
        serve("reactions_list")
        val groups = (parity.reactions("c", "m") as AppResult.Success).data
        assertThat(groups.map { it.emoji }).containsExactly("❤️", "👍🏽").inOrder()
        assertThat(groups.first().users.map { it.displayName }).containsExactly("Asha", "Bilal")

        serve("read_summary")
        val seen = (parity.readSummary("c", "m") as AppResult.Success).data
        assertThat(seen.count).isEqualTo(1)
        assertThat(seen.seenBy.single().displayName).isEqualTo("Dev")

        serve("stickers")
        val packs = (parity.stickers() as AppResult.Success).data
        assertThat(packs.single().name).isEqualTo("Momentum basics")
        assertThat(packs.single().stickers.single().width).isEqualTo(512)
    }

    @Test
    fun `notes feed decodes mine and others`() = runTest {
        serve("notes_feed")
        val notes = (parity.notes() as AppResult.Success).data
        assertThat(notes.mine?.text).isEqualTo("Back at 6")
        assertThat(notes.others.single().displayName).isEqualTo("Asha")
        assertThat(notes.others.single().note.audience).isEqualTo("friends")
    }

    @Test
    fun `search returns people, chats and plain-text snippets`() = runTest {
        serve("search_result")
        val results = (parity.search("station") as AppResult.Success).data
        assertThat(results.people.single().userId).isEqualTo("0a000000-0000-4000-8000-00000000000a")
        assertThat(results.conversations.single().peer?.username).isEqualTo("asha")
        assertThat(results.messages.single().snippet).isEqualTo("see you at the station")
    }

    @Test
    fun `shared posts and media decode, and filtered requests stay requests`() = runTest {
        serve("shared_posts")
        val posts = (parity.shared("c", SHARED_POSTS) as AppResult.Success).data.items
        assertThat(posts.single().postId).isEqualTo("00000000-0000-4000-8000-000000000002")
        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/conversations/c/shared?kind=posts")

        serve("shared_media")
        val media = (parity.shared("c", SHARED_MEDIA) as AppResult.Success).data.items
        assertThat(media.single().kind).isEqualTo("image")
        assertThat(media.single().mediaId).isEqualTo("00000000-0000-4000-8000-000000000001")
        server.takeRequest()

        serve("requests_filtered")
        val filtered = (parity.requests("filtered") as AppResult.Success).data
        assertThat(filtered.single().isRequest).isTrue()
        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/requests?filter=filtered")
    }

    @Test
    fun `report sends the contract's reason enum and entity`() = runTest {
        server.enqueue(MockResponse.Builder().code(202).build())
        val result = parity.report("c", "m", MessageReportReason.HARASSMENT, note = " ")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c/messages/m/report")
        // A blank note is omitted, not sent as whitespace.
        assertThat(request.body?.utf8()).isEqualTo("""{"reason":"harassment"}""")
    }

    @Test
    fun `a missing route reads as not shipped`() = runTest {
        server.enqueue(MockResponse.Builder().code(404).body("""{"error":{"code":"NOT_FOUND"}}""").build())
        val result = parity.notes() as AppResult.Failure
        assertThat(result.error.isMissingRoute()).isTrue()
    }

    // ── fix pass 2 fixtures (lane M) ────────────────────────────────────

    @Test
    fun `a marked-unread conversation reads unread with its mark`() {
        val row = json.decodeFromString<ApiEnvelope<ConversationDto>>(fixture("conversation_marked_unread")).data!!.toDomain()
        assertThat(row.markedUnread).isTrue()
        assertThat(row.hasUnread).isTrue()
        assertThat(unreadPillText(row.unreadCount, row.hasUnread)).isEqualTo("1")
    }

    @Test
    fun `requests name their sender, never the placeholder`() = runTest {
        val detail = json.decodeFromString<ApiEnvelope<ConversationDto>>(fixture("request_detail")).data!!.toDomain()
        assertThat(detail.sender?.displayName).isEqualTo("Chen")
        assertThat(detail.sender?.deleted).isFalse()
        assertThat(detail.requestTitle(viewerId = "0b000000-0000-4000-8000-00000000000b")).isEqualTo("Chen")

        // The list row has no members at all: the sender block is the only name there is.
        serve("requests_list")
        val rows = (parity.requests("all") as AppResult.Success).data
        assertThat(rows.single().members).isEmpty()
        assertThat(rows.single().sender?.avatarUrl).startsWith("https://cdn.atpost.test/")
        assertThat(rows.single().requestTitle(viewerId = "0b000000-0000-4000-8000-00000000000b")).isEqualTo("Chen")
    }

    @Test
    fun `a Giphy sticker decodes as a GIF marked is_sticker, and a plain GIF is not one`() {
        val sticker = message("message_gif_sticker")
        assertThat(sticker.type).isEqualTo("gif")
        assertThat(sticker.gif?.isSticker).isTrue()
        assertThat(sticker.gif?.url).isEqualTo("https://media1.giphy.com/media/l0HlNQ03J5JxX6lva/giphy.gif")
        assertThat(com.us.android.core.chat.data.typedPreview(sticker)).isEqualTo("Sticker")
        assertThat(message("message_gif").gif?.isSticker).isFalse()
    }

    @Test
    fun `media sensitivity is read when the server knows it`() {
        assertThat(message("message_media_sensitive").media?.sensitive).isFalse()
    }

    @Test
    fun `mark unread and conversation report hit the fix-pass routes`() = runTest {
        server.enqueue(MockResponse.Builder().code(204).build())
        assertThat(parity.setUnread("c", unread = true)).isInstanceOf(AppResult.Success::class.java)
        val mark = server.takeRequest()
        assertThat(mark.method).isEqualTo("PUT")
        assertThat(mark.target).isEqualTo("/v1/chat/conversations/c/unread")
        assertThat(mark.body?.utf8()).isEqualTo("""{"unread":true}""")

        server.enqueue(MockResponse.Builder().code(204).build())
        parity.setUnread("c", unread = false)
        // `false` is the clear, so it must be on the wire, not omitted.
        assertThat(server.takeRequest().body?.utf8()).isEqualTo("""{"unread":false}""")

        server.enqueue(MockResponse.Builder().code(202).build())
        assertThat(parity.reportConversation("c", MessageReportReason.SCAM, note = "asked for money"))
            .isInstanceOf(AppResult.Success::class.java)
        val report = server.takeRequest()
        assertThat(report.method).isEqualTo("POST")
        assertThat(report.target).isEqualTo("/v1/chat/conversations/c/report")
        assertThat(report.body?.utf8()).isEqualTo("""{"reason":"scam","note":"asked for money"}""")
    }

    @Test
    fun `a Giphy sticker send says is_sticker and a GIF send stays byte-identical`() {
        val sticker = com.us.android.core.chat.data.TypedSend.gif(
            com.us.android.core.chat.data.GifInfo("giphy", "abc", "https://media.giphy.com/a.gif", null, 200, 200, isSticker = true),
        ).toRequest()
        val gif = com.us.android.core.chat.data.TypedSend.gif(
            com.us.android.core.chat.data.GifInfo("giphy", "abc", "https://media.giphy.com/a.gif", null, 200, 200),
        ).toRequest()
        val wire = Json { explicitNulls = false }
        assertThat(wire.encodeToString(com.us.android.core.chat.data.SendMessageRequest.serializer(), sticker))
            .contains(""""is_sticker":true""")
        assertThat(wire.encodeToString(com.us.android.core.chat.data.SendMessageRequest.serializer(), gif))
            .doesNotContain("is_sticker")
    }

    @Test
    fun `the committed fixtures carry the fix-pass fields everywhere they appear`() = runTest {
        val rows = json.decodeFromString<ApiEnvelope<List<ConversationDto>>>(fixture("conversations_list")).data!!.map { it.toDomain() }
        assertThat(rows.map { it.markedUnread }).containsExactly(false, false)
        val detail = json.decodeFromString<ApiEnvelope<ConversationDto>>(fixture("conversation_detail")).data!!.toDomain()
        assertThat(detail.markedUnread).isFalse()
        assertThat(detail.sender).isNull()

        serve("requests_filtered")
        val filtered = (parity.requests("filtered") as AppResult.Success).data.single()
        assertThat(filtered.sender?.userId).isEqualTo("0c000000-0000-4000-8000-00000000000c")
        assertThat(filtered.sender?.username).isEqualTo("chen")
        assertThat(filtered.requestTitle(viewerId = "0b000000-0000-4000-8000-00000000000b")).isEqualTo("Chen")

        serve("search_result")
        val search = (parity.search("station") as AppResult.Success).data
        assertThat(search.conversations.single().markedUnread).isFalse()
    }
}
