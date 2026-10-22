package com.us.android.core.feed.ui.share

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.DATING_SOURCE_APP
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PostShareChats
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.RepostReads
import com.us.android.core.model.SessionState
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.ui.UsRepostRow
import com.us.android.core.ui.UsShareTarget
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.junit.Rule
import org.junit.Test

/**
 * The post Share sheet (2026-10-17): which chats the picker offers, what a
 * send puts on the wire and what it says, and the Repost row's state.
 */
class PostShareTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    // ── the picker ─────────────────────────────────────────────────────

    private fun member(id: String, name: String) = ConversationMember(id, "member", name, null)

    @Suppress("LongParameterList")
    private fun chat(
        id: String,
        type: String = "direct",
        title: String? = null,
        request: Boolean = false,
        sourceApp: String? = null,
        at: String = "2026-10-17T09:00:00Z",
        members: List<ConversationMember> = listOf(member("me", "Me"), member("u-$id", "Name $id")),
    ) = Conversation(
        id = id,
        type = type,
        title = title,
        isRequest = request,
        members = members,
        updatedAt = at,
        lastMessageAt = at,
        sourceApp = sourceApp,
    )

    @Test
    fun `the picker offers direct and group chats, never a request or a dating chat`() {
        val chats = listOf(
            chat("d1"),
            chat("g1", type = "group", title = "Trip"),
            chat("r1", request = true),
            chat("p1", sourceApp = DATING_SOURCE_APP),
            chat("b1", type = "broadcast", title = "News"),
        )

        assertThat(shareTargets(chats, viewerId = "me", query = "").map { it.id }).containsExactly("d1", "g1")
    }

    @Test
    fun `the picker lists the most recent first, named as the inbox names them`() {
        val chats = listOf(
            chat("old", at = "2026-10-01T09:00:00Z"),
            chat("new", type = "group", at = "2026-10-17T10:00:00Z", members = listOf(member("me", "Me"), member("a", "Ana"), member("b", "Ben"))),
        )

        val targets = shareTargets(chats, viewerId = "me", query = "")

        assertThat(targets.map { it.id }).containsExactly("new", "old").inOrder()
        assertThat(targets.first().name).isEqualTo("Ana, Ben")
        assertThat(targets.first().isGroup).isTrue()
    }

    @Test
    fun `search narrows by name, ignoring case`() {
        val chats = listOf(chat("d1"), chat("g1", type = "group", title = "Weekend trip"))

        assertThat(shareTargets(chats, viewerId = "me", query = "  TRIP ").map { it.id }).containsExactly("g1")
        assertThat(shareTargets(chats, viewerId = "me", query = "nobody")).isEmpty()
    }

    @Test
    fun `a refused send is said in the web's words`() {
        assertThat(sendPostErrorText(AppError.Unknown(code = "INVALID_REQUEST_MESSAGE", statusCode = 422)))
            .startsWith("A post can't start a message request")
        assertThat(sendPostErrorText(AppError.Forbidden(code = "MESSAGING_NOT_ALLOWED")))
            .isEqualTo("This chat isn't accepting messages from you.")
        assertThat(sendPostErrorText(AppError.RateLimited(retryAfterSeconds = null)))
            .isEqualTo("You're sending too fast. Wait a moment and try again.")
        assertThat(sendPostErrorText(AppError.Timeout())).isEqualTo("Couldn't send the post. Try again.")
    }

    // ── the ViewModel ──────────────────────────────────────────────────

    private class FakeChats : PostShareChats {
        var inbox: AppResult<List<Conversation>> = AppResult.Success(emptyList())
        var sendResult: AppResult<Message>? = null
        val sends = mutableListOf<Triple<String, String, String>>()

        override suspend fun conversations(): AppResult<List<Conversation>> = inbox

        override suspend fun sendPost(conversationId: String, postId: String, idempotencyKey: String): AppResult<Message> {
            sends += Triple(conversationId, postId, idempotencyKey)
            return sendResult ?: AppResult.Success(
                Message(
                    id = "m1",
                    conversationId = conversationId,
                    senderId = "me",
                    senderDisplayName = null,
                    text = "",
                    createdAt = "",
                    postId = postId,
                ),
            )
        }
    }

    private class FakeWrites : EngagementWrites {
        var repostFails = false
        val calls = mutableListOf<String>()

        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)

        override suspend fun repost(postId: String): AppResult<Unit> {
            calls += "repost:$postId"
            return if (repostFails) AppResult.Failure(AppError.NoNetwork()) else AppResult.Success(Unit)
        }

        override suspend fun removeRepost(postId: String): AppResult<Unit> {
            calls += "unrepost:$postId"
            return AppResult.Success(Unit)
        }
    }

    private val chats = FakeChats()
    private val writes = FakeWrites()
    private val store = EngagementStore(writes)
    private var serverReposted: AppResult<Boolean> = AppResult.Success(false)
    private val session = object : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> =
            MutableStateFlow(SessionState.Authenticated(userId = "me", sessionId = "s"))
    }

    private fun viewModel() = PostShareViewModel(chats, RepostReads { serverReposted }, store, session)

    private fun PostShareViewModel.ui() = state.value.toUiState(viewerId, overlays.value[state.value.postId] ?: EngagementOverlay())

    @Test
    fun `the repost row waits for the server, then says Repost or Remove repost`() {
        serverReposted = AppResult.Success(true)
        val vm = viewModel()
        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.HIDDEN)

        vm.open("p1", feedReposted = false)

        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.REMOVE)
    }

    @Test
    fun `a failed repost read falls back to the feed's own flag`() {
        serverReposted = AppResult.Failure(AppError.NoNetwork())
        val vm = viewModel()

        vm.open("p1", feedReposted = true)

        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.REMOVE)
    }

    @Test
    fun `repost flips the row and says so, and the second tap removes it`() {
        val vm = viewModel()
        vm.open("p1", feedReposted = false)

        vm.toggleRepost()

        assertThat(writes.calls).containsExactly("repost:p1")
        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.REMOVE)
        assertThat(vm.message.value?.text).isEqualTo(PostShareViewModel.REPOSTED)

        vm.toggleRepost()

        assertThat(writes.calls).containsExactly("repost:p1", "unrepost:p1").inOrder()
        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.REPOST)
        assertThat(vm.message.value?.text).isEqualTo(PostShareViewModel.UNREPOSTED)
    }

    @Test
    fun `a refused repost is put back and said, and leaves no banner for other screens`() {
        writes.repostFails = true
        val vm = viewModel()
        vm.open("p1", feedReposted = false)

        vm.toggleRepost()

        assertThat(vm.ui().repost).isEqualTo(UsRepostRow.REPOST)
        assertThat(vm.message.value?.text).isEqualTo(PostShareViewModel.REPOST_FAILED)
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Error)
        assertThat(store.failures.value).isEmpty()
        assertThat(vm.ui().repostBusy).isFalse()
    }

    @Test
    fun `the picker reads the inbox once and lists the allowed chats`() {
        chats.inbox = AppResult.Success(listOf(chat("d1"), chat("r1", request = true)))
        val vm = viewModel()
        vm.open("p1", feedReposted = false)

        vm.openPicker()

        assertThat(vm.ui().picker?.targets?.map { it.id }).containsExactly("d1")
        vm.onQueryChange("zzz")
        assertThat(vm.ui().picker?.targets).isEmpty()
        vm.closePicker()
        assertThat(vm.ui().picker).isNull()
    }

    @Test
    fun `an inbox that cannot be read offers Try again`() {
        chats.inbox = AppResult.Failure(AppError.NoNetwork())
        val vm = viewModel()
        vm.open("p1", feedReposted = false)

        vm.openPicker()

        assertThat(vm.ui().picker?.failed).isTrue()
        chats.inbox = AppResult.Success(listOf(chat("d1")))
        vm.retryConversations()
        assertThat(vm.ui().picker?.targets?.map { it.id }).containsExactly("d1")
    }

    @Test
    fun `sending posts the card for this post and says Sent`() {
        val vm = viewModel()
        vm.open("p1", feedReposted = false)

        vm.send(UsShareTarget(id = "d1", name = "Ana", isGroup = false))

        assertThat(chats.sends.single().first).isEqualTo("d1")
        assertThat(chats.sends.single().second).isEqualTo("p1")
        assertThat(chats.sends.single().third).isNotEmpty()
        assertThat(vm.message.value?.text).isEqualTo(PostShareViewModel.SENT)
        assertThat(vm.state.value.sending).isEmpty()
    }

    @Test
    fun `a refused send stays in the picker, said, and a retry keeps its idempotency key`() {
        chats.sendResult = AppResult.Failure(AppError.Forbidden(code = "MESSAGING_NOT_ALLOWED"))
        val vm = viewModel()
        vm.open("p1", feedReposted = false)
        vm.openPicker()
        val target = UsShareTarget(id = "d1", name = "Ana", isGroup = false)

        vm.send(target)
        vm.send(target)

        assertThat(vm.ui().picker?.error).isEqualTo("This chat isn't accepting messages from you.")
        assertThat(vm.message.value).isNull()
        assertThat(chats.sends.map { it.third }.distinct()).hasSize(1)
    }

    @Test
    fun `copy link says so`() {
        val vm = viewModel()

        vm.onLinkCopied()

        assertThat(vm.message.value?.text).isEqualTo(PostShareViewModel.LINK_COPIED)
        vm.dismissMessage()
        assertThat(vm.message.value).isNull()
    }

    @Test
    fun `the sheet's link is the post's permalink`() {
        val vm = viewModel()
        vm.open("p-42", feedReposted = false)

        // The permalink, then the web's share tail (`?ref=<id>&utm_source=share`, 2026-10-22).
        assertThat(vm.ui().link).matches("https://cleestudio.com/post/p-42\\?ref=[a-z0-9]{10}&utm_source=share")
    }
}
