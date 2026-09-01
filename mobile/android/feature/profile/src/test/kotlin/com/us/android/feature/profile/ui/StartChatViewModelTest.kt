package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.AddMemberRequest
import com.us.android.core.chat.data.BulkPresenceRequest
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.ConversationMemberDto
import com.us.android.core.chat.data.ConversationSettingsDto
import com.us.android.core.chat.data.CreateDirectRequest
import com.us.android.core.chat.data.CreateGroupRequest
import com.us.android.core.chat.data.DeleteMessageRequest
import com.us.android.core.chat.data.MarkReadRequest
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.SetRoleRequest
import com.us.android.core.chat.data.StatusDto
import com.us.android.core.chat.data.ToggleReactionRequest
import com.us.android.core.chat.data.TransferOwnerRequest
import com.us.android.core.chat.data.TypingRequest
import com.us.android.core.chat.data.UpdateGroupInfoRequest
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * Whether pressing Message on someone's profile navigates anywhere.
 *
 * This is the other half of B-LB-4 criterion 6's "denied start causes no
 * navigation". `ChatNavigationTest` in `:feature:chat` proves the host does
 * nothing when handed no conversation; this proves the ViewModel never hands it
 * one after a refusal.
 *
 * The failure being forbidden is navigating to an INVENTED thread: a
 * `403 MESSAGING_NOT_ALLOWED` carries no conversation, so any id the client
 * pushed would be one it made up, and the thread would open onto a conversation
 * that does not exist.
 */
class StartChatViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeChatApi : ChatApi {
        val responses = ArrayDeque<() -> ApiEnvelope<ConversationDto>>()
        var createDirectCalls = 0

        override suspend fun createDirect(
            idempotencyKey: String,
            body: CreateDirectRequest,
        ): ApiEnvelope<ConversationDto> {
            createDirectCalls++
            return responses.removeFirst().invoke()
        }

        override suspend fun conversations(limit: Int, cursor: String?): Nothing = error("not used")

        override suspend fun toggleReaction(
            conversationId: String,
            messageId: String,
            body: ToggleReactionRequest,
        ): Nothing = error("not used")
        override suspend fun deleteMessage(
            conversationId: String,
            messageId: String,
            body: DeleteMessageRequest,
        ): Nothing = error("not used")
        override suspend fun conversationSettings(conversationId: String): Nothing = error("not used")
        override suspend fun updateConversationSettings(
            conversationId: String,
            body: ConversationSettingsDto,
        ): Nothing = error("not used")
        override suspend fun subscriptionEntitlement(conversationId: String): Nothing = error("not used")
        override suspend fun conversation(conversationId: String): Nothing = error("not used")

        // Production chat pass surface — unused by this navigation test.
        override suspend fun requests(): Nothing = error("not used")
        override suspend fun acceptRequest(conversationId: String): Nothing = error("not used")
        override suspend fun declineRequest(conversationId: String): Nothing = error("not used")
        override suspend fun blockRequest(conversationId: String): Nothing = error("not used")
        override suspend fun reportRequest(conversationId: String): Nothing = error("not used")
        override suspend fun leave(conversationId: String): Nothing = error("not used")
        override suspend fun transferOwner(
            conversationId: String,
            body: TransferOwnerRequest,
        ): Nothing = error("not used")
        override suspend fun setMemberRole(
            conversationId: String,
            userId: String,
            body: SetRoleRequest,
        ): Nothing = error("not used")
        override suspend fun addMember(conversationId: String, body: AddMemberRequest): Nothing = error("not used")
        override suspend fun removeMember(conversationId: String, userId: String): Nothing = error("not used")
        override suspend fun updateGroupInfo(
            conversationId: String,
            body: UpdateGroupInfoRequest,
        ): Nothing = error("not used")
        override suspend fun invitations(): Nothing = error("not used")
        override suspend fun acceptInvitation(invitationId: String): Nothing = error("not used")
        override suspend fun declineInvitation(invitationId: String): Nothing = error("not used")
        override suspend fun connections(userId: String, limit: Int): Nothing = error("not used")
        override suspend fun createGroup(
            idempotencyKey: String,
            body: CreateGroupRequest,
        ): Nothing = error("not used")

        override suspend fun messages(
            conversationId: String,
            limit: Int,
            cursor: String?,
        ): Nothing = error("not used")

        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): Nothing = error("not used")

        override suspend fun markRead(
            conversationId: String,
            body: MarkReadRequest,
        ): ApiEnvelope<StatusDto> = error("not used")

        override suspend fun setTyping(
            conversationId: String,
            body: TypingRequest,
        ): Nothing = error("not used")

        override suspend fun presence(conversationId: String): Nothing = error("not used")
        override suspend fun bulkPresence(body: BulkPresenceRequest): Nothing = error("not used")
    }

    private fun viewModel(api: FakeChatApi) =
        StartChatViewModel(ChatRepository(api, ErrorMapper(json)))

    private fun opened(id: String) = ApiEnvelope(
        data = ConversationDto(
            id = id,
            type = "direct",
            createdBy = "me",
            members = listOf(
                ConversationMemberDto("me", "member", "2026-08-21T09:00:00Z", "Alpha"),
                ConversationMemberDto("u2", "member", "2026-08-21T09:00:00Z", "Bravo"),
            ),
        ),
    )

    private fun denied(): () -> Nothing = {
        throw HttpException(
            Response.error<ConversationDto>(
                403,
                """{"error":{"code":"MESSAGING_NOT_ALLOWED","message":"not permitted"}}"""
                    .toResponseBody("application/json".toMediaType()),
            ),
        )
    }

    /** A denial produces a reason and NO destination. */
    @Test
    fun `a policy denial never yields a conversation to navigate to`() = runTest {
        val api = FakeChatApi().apply { responses += denied() }
        val viewModel = viewModel(api)

        viewModel.open("u2", "Bravo")

        val state = viewModel.state.value
        assertThat(state.openConversation).isNull()
        assertThat(state.notAllowed).isNotNull()
        assertThat(state.busy).isFalse()
        // Held apart from `error` so the UI offers no retry: the answer will not
        // change until the other person changes their settings.
        assertThat(state.error).isNull()
    }

    /** The refusal still reached the server — eligibility is never pre-judged. */
    @Test
    fun `a denial was decided by the server, not locally`() = runTest {
        val api = FakeChatApi().apply { responses += denied() }

        viewModel(api).open("u2", "Bravo")

        assertThat(api.createDirectCalls).isEqualTo(1)
    }

    /** Success yields exactly one destination, carrying the SERVER's id. */
    @Test
    fun `an allowed start yields the conversation the server returned`() = runTest {
        val api = FakeChatApi().apply { responses += { opened("conv-99") } }
        val viewModel = viewModel(api)

        viewModel.open("u2", "Bravo")

        val open = viewModel.state.value.openConversation
        assertThat(open).isNotNull()
        assertThat(open!!.conversationId).isEqualTo("conv-99")
        // The title comes from the profile being viewed; the thread has no
        // viewer id with which to name a direct conversation itself.
        assertThat(open.title).isEqualTo("Bravo")
    }

    /**
     * The destination is consumed ONCE.
     *
     * Without this the state would re-emit on every recomposition and the
     * thread would be pushed onto the back stack again each time the profile
     * resumed — a stack of identical threads behind Back.
     */
    @Test
    fun `the destination is cleared once the host has acted on it`() = runTest {
        val api = FakeChatApi().apply { responses += { opened("conv-99") } }
        val viewModel = viewModel(api)
        viewModel.open("u2", "Bravo")

        viewModel.onConversationOpened()

        assertThat(viewModel.state.value.openConversation).isNull()
    }

    /** A transient failure is retryable and, equally, navigates nowhere. */
    @Test
    fun `a transport failure offers a retry and no destination`() = runTest {
        val api = FakeChatApi().apply { responses += { throw IOException("offline") } }
        val viewModel = viewModel(api)

        viewModel.open("u2", "Bravo")

        val state = viewModel.state.value
        assertThat(state.openConversation).isNull()
        assertThat(state.error).isNotNull()
        assertThat(state.notAllowed).isNull()
    }

    /** Dismissing clears both messages without inventing a destination. */
    @Test
    fun `dismissing an error leaves the user where they were`() = runTest {
        val api = FakeChatApi().apply { responses += denied() }
        val viewModel = viewModel(api)
        viewModel.open("u2", "Bravo")

        viewModel.dismissError()

        val state = viewModel.state.value
        assertThat(state.notAllowed).isNull()
        assertThat(state.error).isNull()
        assertThat(state.openConversation).isNull()
    }
}
