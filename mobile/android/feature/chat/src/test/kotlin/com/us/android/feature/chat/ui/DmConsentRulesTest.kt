package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatPeer
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageConsentApi
import com.us.android.core.chat.data.MessageConsentRepository
import com.us.android.core.chat.data.PendingSend
import com.us.android.core.chat.data.PermissionCheckDto
import com.us.android.core.chat.data.RequestCountDto
import com.us.android.core.chat.data.RequestDraftProblem
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * DM consent (2026-10-20): what an outgoing request's thread lets through
 * before the other person accepts, and the requests folder's paging.
 */
class DmConsentRulesTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    // ── The outgoing request's composer ─────────────────────────────────

    private fun pendingRequest() = Conversation(
        id = "c",
        type = "direct",
        title = null,
        isRequest = true,
        members = listOf(ConversationMember(VIEWER, "member", "Me"), ConversationMember("u2", "member", "Bravo")),
        updatedAt = "",
        peer = ChatPeer("u2", "Bravo", "bravo", null, null, null),
        createdBy = VIEWER,
    )

    private fun message(id: String, from: String) =
        Message(id = id, conversationId = "c", senderId = from, senderDisplayName = null, text = "hi", createdAt = "")

    private fun render(
        draft: String = "",
        messages: List<Message> = emptyList(),
        pending: List<PendingSend> = emptyList(),
        conversation: Conversation? = pendingRequest(),
    ) = ThreadRenderState(
        thread = ThreadUiState(conversationId = "c", messages = messages, draft = draft),
        pendingSends = pending,
        viewerId = VIEWER,
        conversation = conversation,
    )

    @Test
    fun `a fresh outgoing request takes one short text message`() {
        val state = render(draft = "Hi! Loved your talk.")
        assertThat(state.outgoingRequest).isTrue()
        assertThat(state.requestMessageSent).isFalse()
        assertThat(state.canSend).isTrue()
        assertThat(state.requestRecipient).isEqualTo("@bravo")
        assertThat(requestWaitingText(state.requestRecipient)).isEqualTo(
            "Your request is waiting. You can send one message (text only, no links) until @bravo accepts.",
        )
    }

    @Test
    fun `a link or more than 500 characters cannot go`() {
        val link = render(draft = "see example.com")
        assertThat(link.requestDraftProblem).isEqualTo(RequestDraftProblem.HasLink)
        assertThat(link.canSend).isFalse()
        val long = render(draft = "a".repeat(501))
        assertThat(long.requestDraftProblem).isEqualTo(RequestDraftProblem.TooLong)
        assertThat(long.canSend).isFalse()
    }

    @Test
    fun `once the one message went, nothing more can`() {
        val sent = render(draft = "and another", messages = listOf(message("m1", VIEWER)))
        assertThat(sent.requestMessageSent).isTrue()
        assertThat(sent.canSend).isFalse()
        val queued = render(
            draft = "and another",
            pending = listOf(PendingSend("k", "c", "hi", mediaId = null, createdAtMillis = 0, failed = false)),
        )
        assertThat(queued.requestMessageSent).isTrue()
        assertThat(queued.canSend).isFalse()
    }

    @Test
    fun `a refused message is said so, and may be fixed and sent again`() {
        val refused = render(
            draft = "fixed text",
            pending = listOf(PendingSend("k", "c", "see x.com", mediaId = null, createdAtMillis = 0, failed = true)),
        )
        assertThat(refused.requestMessageRefused).isTrue()
        assertThat(refused.requestMessageSent).isFalse()
        assertThat(refused.canSend).isTrue()
    }

    @Test
    fun `a reply from them ends the pending state, and an ordinary chat has no limits`() {
        val answered = render(draft = "great!", messages = listOf(message("m1", VIEWER), message("m2", "u2")))
        assertThat(answered.outgoingRequest).isFalse()
        assertThat(answered.canSend).isTrue()
        val chat = render(draft = "see example.com", conversation = pendingRequest().copy(isRequest = false))
        assertThat(chat.outgoingRequest).isFalse()
        assertThat(chat.canSend).isTrue()
    }

    @Test
    fun `an incoming request is not the viewer's to limit`() {
        val incoming = render(draft = "x", conversation = pendingRequest().copy(createdBy = "u2"))
        assertThat(incoming.outgoingRequest).isFalse()
    }

    // ── The requests folder ─────────────────────────────────────────────

    private class FakeConsentApi : MessageConsentApi {
        val pages = ArrayDeque<ApiEnvelope<List<ConversationDto>>>()
        val asked = mutableListOf<String>()

        override suspend fun checkPermission(targetUserId: String, actions: String): ApiEnvelope<PermissionCheckDto> =
            error("not used")

        override suspend fun requestsPage(
            direction: String,
            filtered: Boolean,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<ConversationDto>> {
            asked += "$filtered:${cursor ?: "-"}"
            return if (filtered) ApiEnvelope(data = listOf(row("h1"))) else pages.removeFirst()
        }

        override suspend fun requestsCount() = ApiEnvelope(data = RequestCountDto(count = 2, filteredCount = 1))
    }

    private val signedIn = object : SessionStateProvider {
        override val sessionState = MutableStateFlow<SessionState>(SessionState.Authenticated(userId = VIEWER, sessionId = "s"))
    }

    @Test
    fun `the folder pages on, never repeats a row, and counts the hidden ones`() = runTest {
        val api = FakeConsentApi().apply {
            pages += ApiEnvelope(data = listOf(row("r1"), row("r2")), meta = ApiMeta(nextCursor = "p2"))
            pages += ApiEnvelope(data = listOf(row("r2"), row("r3")))
        }
        val viewModel = MessageRequestsViewModel(MessageConsentRepository(api, ErrorMapper(Json)), signedIn)

        assertThat(viewModel.state.value.rows.map { it.id }).containsExactly("r1", "r2").inOrder()
        assertThat(viewModel.state.value.canLoadMore).isTrue()
        assertThat(viewModel.state.value.hiddenCount).isEqualTo(1)

        viewModel.loadMore()

        val state = viewModel.state.value
        assertThat(state.rows.map { it.id }).containsExactly("r1", "r2", "r3").inOrder()
        assertThat(state.next).isNull()
        // The server's cursor goes back untouched.
        assertThat(api.asked).contains("false:p2")
    }

    @Test
    fun `hidden requests are a list of their own`() = runTest {
        val api = FakeConsentApi().apply { pages += ApiEnvelope(data = listOf(row("r1"))) }
        val viewModel = MessageRequestsViewModel(MessageConsentRepository(api, ErrorMapper(Json)), signedIn)

        viewModel.showHidden()

        assertThat(viewModel.state.value.hidden).isTrue()
        assertThat(viewModel.state.value.rows.map { it.id }).containsExactly("h1")
    }

    @Test
    fun `an expired request leaves the list at once`() = runTest {
        val api = FakeConsentApi().apply { pages += ApiEnvelope(data = listOf(row("r1"), row("r2"))) }
        val viewModel = MessageRequestsViewModel(MessageConsentRepository(api, ErrorMapper(Json)), signedIn)

        viewModel.onRequestRemoved("r1")

        assertThat(viewModel.state.value.rows.map { it.id }).containsExactly("r2")
    }

    @Test
    fun `the hidden entry's count reads as a number, at least when the server stopped counting`() {
        assertThat(hiddenCountLabel(null, capped = false)).isNull()
        assertThat(hiddenCountLabel(0, capped = false)).isNull()
        assertThat(hiddenCountLabel(3, capped = false)).isEqualTo("3")
        assertThat(hiddenCountLabel(3, capped = true)).isEqualTo("3+")
    }

    @Test
    fun `the Requests badge reads the server count, 999+ when capped, the cached rows without one`() {
        val cached = com.us.android.feature.chat.ui.home.ChatHomeUiState(
            requests = listOf("a", "b").map { pendingRequest().copy(id = it, createdBy = "u2") },
        )
        assertThat(cached.requestCount).isEqualTo(2)
        assertThat(cached.copy(serverRequestCount = 40).requestCountLabel).isEqualTo("40")
        assertThat(cached.copy(serverRequestCount = 999, requestCountCapped = true).requestCountLabel).isEqualTo("999+")
    }

    private companion object {
        const val VIEWER = "me"

        fun row(id: String) = ConversationDto(id = id, type = "direct", isRequest = true, direction = "incoming")
    }
}
