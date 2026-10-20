package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.MessageConsentApi
import com.us.android.core.chat.data.MessageConsentRepository
import com.us.android.core.chat.data.MessageDecision
import com.us.android.core.chat.data.MessageDenyReason
import com.us.android.core.chat.data.PermissionDecisionDto
import com.us.android.core.chat.data.RequestComposerRules
import com.us.android.core.chat.data.RequestDraftProblem
import com.us.android.core.chat.data.RequestCounts
import com.us.android.core.chat.data.RequestPageKey
import com.us.android.core.chat.data.requestCountLabel
import com.us.android.core.chat.data.RequestSender
import com.us.android.core.chat.data.toMessagePermission
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
 * DM consent (2026-10-20): the message decision as graph-service sends it,
 * the requests folder's paging and badge, and the rules an outgoing
 * request's composer enforces before the server does.
 */
class MessageConsentTest {

    /** NetworkModule.provideJson, exactly. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var consent: MessageConsentRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(MessageConsentApi::class.java)
        consent = MessageConsentRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun serve(body: String, code: Int = 200) =
        server.enqueue(MockResponse.Builder().code(code).body(body).build())

    // ── The decision ────────────────────────────────────────────────────

    @Test
    fun `the three message outcomes read as allow, request and deny with its reason`() {
        assertThat(PermissionDecisionDto(allowed = true).toMessagePermission().decision).isEqualTo(MessageDecision.Allow)
        assertThat(PermissionDecisionDto(fallback = "message_request").toMessagePermission().decision)
            .isEqualTo(MessageDecision.Request)
        val deny = PermissionDecisionDto(reason = "followers_only").toMessagePermission()
        assertThat(deny.decision).isEqualTo(MessageDecision.Deny)
        assertThat(deny.reason).isEqualTo(MessageDenyReason.FOLLOWERS_ONLY)
    }

    @Test
    fun `a deny without a reason, or with one this client does not know, has a null reason`() {
        assertThat(PermissionDecisionDto().toMessagePermission())
            .isEqualTo(com.us.android.core.chat.data.MessagePermission(MessageDecision.Deny, null))
        assertThat(PermissionDecisionDto(reason = "something_new").toMessagePermission().reason).isNull()
        assertThat(PermissionDecisionDto(reason = "Messages_Off").toMessagePermission().reason)
            .isEqualTo(MessageDenyReason.MESSAGES_OFF)
    }

    @Test
    fun `the check asks graph-service for the message action and reads its decision`() = runTest {
        serve(
            """{"data":{"target_user_id":"u2","decisions":{"message":{"allowed":false,"reason":"people_i_follow"}},""" +
                """"computed_at":"2026-10-20T10:00:00Z","ttl_seconds":3}}""",
        )

        val result = consent.messagePermission("u2")

        assertThat(server.takeRequest().target).isEqualTo("/v1/permissions/check?target_user_id=u2&actions=message")
        val permission = (result as AppResult.Success).data
        assertThat(permission.decision).isEqualTo(MessageDecision.Deny)
        assertThat(permission.reason).isEqualTo(MessageDenyReason.PEOPLE_I_FOLLOW)
    }

    @Test
    fun `a check that carries no message decision is a failure, not an allow`() = runTest {
        serve("""{"data":{"target_user_id":"u2","decisions":{}}}""")

        assertThat(consent.messagePermission("u2")).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── The requests folder ─────────────────────────────────────────────

    private fun rows(vararg ids: String, direction: String = "incoming") = ids.joinToString(",") {
        """{"id":"$it","type":"direct","is_request":true,"direction":"$direction","filtered":false,""" +
            """"preview":"hello from $it","requested_at":"2026-10-20T09:00:00Z","members":[]}"""
    }

    @Test
    fun `the first page asks for incoming, not-hidden requests and keeps the server's cursor`() = runTest {
        serve("""{"data":[${rows("r1", "r2")}],"meta":{"next_cursor":"opaque==","has_more":true}}""")

        val page = (consent.requestsPage(hidden = false) as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/requests?direction=incoming&filtered=false&limit=20")
        assertThat(page.items.map { it.id }).containsExactly("r1", "r2").inOrder()
        assertThat(page.next).isEqualTo(RequestPageKey("opaque=="))
        // The row's one message is its preview.
        assertThat(page.items.first().lastMessagePreview).isEqualTo("hello from r1")
    }

    @Test
    fun `the next page sends the cursor back untouched, and no cursor is the end`() = runTest {
        serve("""{"data":[${rows("r3")}],"meta":{"has_more":false}}""")

        val page = (consent.requestsPage(hidden = false, key = RequestPageKey("opaque==")) as AppResult.Success).data

        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/chat/requests?direction=incoming&filtered=false&limit=20&cursor=opaque%3D%3D")
        assertThat(page.next).isNull()
    }

    @Test
    fun `hidden requests are the filtered=true list`() = runTest {
        serve("""{"data":[]}""")

        consent.requestsPage(hidden = true)

        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/requests?direction=incoming&filtered=true&limit=20")
    }

    @Test
    fun `requests the viewer sent never enter the folder`() = runTest {
        serve("""{"data":[${rows("in1")},${rows("out1", direction = "outgoing")}]}""")

        val page = (consent.requestsPage(hidden = false) as AppResult.Success).data

        assertThat(page.items.map { it.id }).containsExactly("in1")
        assertThat(page.items.single().requestDirection).isEqualTo("incoming")
    }

    @Test
    fun `the counts carry the badge, the hidden number and the cap`() = runTest {
        serve("""{"data":{"count":7,"filtered_count":2,"capped":false}}""")
        assertThat((consent.requestCounts() as AppResult.Success).data).isEqualTo(RequestCounts(7, 2, capped = false))
        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/requests/count")

        serve("""{"data":{"count":999,"filtered_count":0,"capped":true}}""")
        val capped = (consent.requestCounts() as AppResult.Success).data!!
        assertThat(requestCountLabel(capped.count, capped.capped)).isEqualTo("999+")
        assertThat(requestCountLabel(7, capped = false)).isEqualTo("7")
    }

    @Test
    fun `a server without the count route answers null, so the cached rows count`() = runTest {
        serve("""{"error":{"code":"NOT_FOUND"}}""", code = 404)
        assertThat((consent.requestCounts() as AppResult.Success).data).isNull()

        serve("""{"error":{"code":"NOT_IMPLEMENTED"}}""", code = 501)
        assertThat((consent.requestCounts() as AppResult.Success).data).isNull()
    }

    @Test
    fun `the viewer's waiting request to someone is found among their outgoing requests`() = runTest {
        serve(
            """{"data":[{"id":"c9","type":"direct","is_request":true,"direction":"outgoing",""" +
                """"members":[{"user_id":"me"},{"user_id":"u2"}]}]}""",
        )

        val found = (consent.pendingRequestTo("u2") as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/chat/requests?direction=outgoing&filtered=false&limit=20")
        assertThat(found?.id).isEqualTo("c9")
    }

    @Test
    fun `an expired request's frame is read`() {
        val event = com.us.android.core.chat.data.parseChatFrame(
            json,
            """{"type":"message_request_removed","payload":{"conversation_id":"c7","reason":"expired"}}""",
        )
        assertThat(event).isEqualTo(com.us.android.core.chat.data.ChatSocketEvent.MessageRequestRemoved("c7", "expired"))
    }

    // ── The outgoing request's composer ─────────────────────────────────

    @Test
    fun `links are found the way the server finds them`() {
        assertThat(RequestComposerRules.containsLink("see https://x.y")).isTrue()
        assertThat(RequestComposerRules.containsLink("WWW.thing")).isTrue()
        assertThat(RequestComposerRules.containsLink("go to example.com now")).isTrue()
        assertThat(RequestComposerRules.containsLink("my-site.io")).isTrue()
        assertThat(RequestComposerRules.containsLink("Hi! Loved your post about Goa.")).isFalse()
        assertThat(RequestComposerRules.containsLink("the end.")).isFalse()
    }

    @Test
    fun `a request message is at most 500 characters as the server counts them`() {
        assertThat(RequestComposerRules.problem("a".repeat(500))).isNull()
        assertThat(RequestComposerRules.problem("a".repeat(501))).isEqualTo(RequestDraftProblem.TooLong)
        // 500 emoji are 1000 UTF-16 units but 500 runes: still allowed.
        assertThat(RequestComposerRules.problem("😀".repeat(500))).isNull()
        assertThat(RequestComposerRules.problem("visit example.com")).isEqualTo(RequestDraftProblem.HasLink)
        assertThat(RequestComposerRules.problem("   ")).isNull()
    }

    private fun request(direction: String? = null, sender: String? = null, createdBy: String = "", isRequest: Boolean = true) =
        Conversation(
            id = "c",
            type = "direct",
            title = null,
            isRequest = isRequest,
            members = listOf(ConversationMember("me", "member", "Me"), ConversationMember("u2", "member", "Bravo")),
            updatedAt = "",
            sender = sender?.let { RequestSender(it, "", "", null, false) },
            createdBy = createdBy,
            requestDirection = direction,
        )

    @Test
    fun `an outgoing pending request is the viewer's own unanswered request`() {
        assertThat(RequestComposerRules.isOutgoingPending(request(direction = "outgoing"), "me")).isTrue()
        assertThat(RequestComposerRules.isOutgoingPending(request(direction = "incoming"), "me")).isFalse()
        assertThat(RequestComposerRules.isOutgoingPending(request(sender = "me"), "me")).isTrue()
        assertThat(RequestComposerRules.isOutgoingPending(request(sender = "u2", createdBy = "me"), "me")).isFalse()
        assertThat(RequestComposerRules.isOutgoingPending(request(createdBy = "me"), "me")).isTrue()
        assertThat(RequestComposerRules.isOutgoingPending(request(createdBy = "u2"), "me")).isFalse()
        assertThat(RequestComposerRules.isOutgoingPending(request(createdBy = "me", isRequest = false), "me")).isFalse()
        assertThat(RequestComposerRules.isOutgoingPending(null, "me")).isFalse()
    }
}
