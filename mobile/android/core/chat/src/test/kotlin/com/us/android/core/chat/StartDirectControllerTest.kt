package com.us.android.core.chat

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
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import com.us.android.core.chat.data.StatusDto
import com.us.android.core.chat.data.ToggleReactionRequest
import com.us.android.core.chat.data.TransferOwnerRequest
import com.us.android.core.chat.data.TypingRequest
import com.us.android.core.chat.data.UpdateGroupInfoRequest
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * Opening a direct conversation, and what happens when it is refused.
 *
 * The idempotency behaviour here is the point. `createDirect` used to mint a
 * fresh key inside the repository on every call, which meant a request that
 * reached the server and lost its response created a SECOND conversation with
 * the same person on retry — two threads, history split between them, and no
 * way for the user to merge them.
 */
class StartDirectControllerTest {

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeApi : ChatApi {
        val keys = mutableListOf<String>()
        val targets = mutableListOf<String>()
        val responses = ArrayDeque<() -> ApiEnvelope<ConversationDto>>()

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

        // Production chat pass surface — unused by these controller tests.
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
        override suspend fun conversation(conversationId: String): Nothing = error("not used")

        override suspend fun createDirect(
            idempotencyKey: String,
            body: CreateDirectRequest,
        ): ApiEnvelope<ConversationDto> {
            keys += idempotencyKey
            targets += body.otherUserId
            return responses.removeFirst().invoke()
        }

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

    private fun controller(api: FakeApi) =
        StartDirectController(ChatRepository(api, ErrorMapper(json)))

    // Named to avoid colliding with ChatApi.conversation, which is in scope
    // inside `FakeApi().apply { }` and would resolve to the suspend member.
    private fun directConversation(id: String) = ApiEnvelope(
        data = ConversationDto(
            id = id,
            type = "direct",
            title = null,
            createdBy = "u1",
            members = listOf(
                ConversationMemberDto("u1", "member", "2026-08-21T09:00:00Z", "Alpha Btest"),
                ConversationMemberDto("u2", "member", "2026-08-21T09:00:00Z", "Bravo Btest"),
            ),
        ),
    )

    private fun denied(): () -> Nothing = {
        throw HttpException(
            Response.error<ConversationDto>(
                403,
                """{"error":{"code":"MESSAGING_NOT_ALLOWED","message":"not allowed"}}"""
                    .toResponseBody("application/json".toMediaType()),
            ),
        )
    }

    @Test
    fun `an allowed target returns the conversation the server chose`() = runTest {
        val api = FakeApi().apply { responses += { directConversation("conv-1") } }

        val result = controller(api).open("u2")

        assertThat(result).isInstanceOf(StartDirectResult.Opened::class.java)
        assertThat((result as StartDirectResult.Opened).conversation.id).isEqualTo("conv-1")
        assertThat(api.targets).containsExactly("u2")
    }

    /**
     * ONE intent, ONE key.
     *
     * A lost response is indistinguishable from a rejected request at the
     * client. Retrying under the same key makes the server replay the original
     * creation; retrying under a new one makes it create a second thread.
     */
    @Test
    fun `a retry after a failure reuses the same idempotency key`() = runTest {
        val api = FakeApi().apply {
            responses += { throw IOException("response lost") }
            responses += { directConversation("conv-1") }
        }
        val controller = controller(api)

        assertThat(controller.open("u2")).isInstanceOf(StartDirectResult.Failed::class.java)
        assertThat(controller.open("u2")).isInstanceOf(StartDirectResult.Opened::class.java)

        assertThat(api.keys).hasSize(2)
        assertThat(api.keys[0]).isEqualTo(api.keys[1])
    }

    /** A different person is a different intent, and must not replay the first. */
    @Test
    fun `a different target gets a different key`() = runTest {
        val api = FakeApi().apply {
            responses += { throw IOException("response lost") }
            responses += { directConversation("conv-2") }
        }
        val controller = controller(api)

        controller.open("u2")
        controller.open("u3")

        assertThat(api.targets).containsExactly("u2", "u3").inOrder()
        assertThat(api.keys[0]).isNotEqualTo(api.keys[1])
    }

    /** Once the conversation exists the intent is spent; a later one is new. */
    @Test
    fun `a second intent after success gets a fresh key`() = runTest {
        val api = FakeApi().apply {
            responses += { directConversation("conv-1") }
            responses += { directConversation("conv-1") }
        }
        val controller = controller(api)

        controller.open("u2")
        controller.open("u2")

        assertThat(api.keys[0]).isNotEqualTo(api.keys[1])
    }

    /**
     * A policy denial is classified apart from a transient failure.
     *
     * The UI offers a retry for one and not the other. Treating
     * `MESSAGING_NOT_ALLOWED` as retryable gives the user a button that cannot
     * work no matter how many times they press it.
     */
    @Test
    fun `a policy denial is not reported as a retryable failure`() = runTest {
        val api = FakeApi().apply { responses += denied() }

        val result = controller(api).open("u2")

        assertThat(result).isInstanceOf(StartDirectResult.NotAllowed::class.java)
    }

    @Test
    fun `a transport failure is retryable, not a denial`() = runTest {
        val api = FakeApi().apply { responses += { throw IOException("offline") } }

        val result = controller(api).open("u2")

        assertThat(result).isInstanceOf(StartDirectResult.Failed::class.java)
    }

    /**
     * The client never pre-judges eligibility.
     *
     * graph-service owns the decision and re-evaluates it on every attempt, so
     * a denial must still reach the server rather than being predicted locally
     * from a cached setting that may already be stale.
     */
    @Test
    fun `a denial still reached the server`() = runTest {
        val api = FakeApi().apply { responses += denied() }

        controller(api).open("u2")

        assertThat(api.targets).containsExactly("u2")
    }
}
