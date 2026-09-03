package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ErrorMapper
import com.us.android.core.notifications.data.NotificationPage
import com.us.android.core.notifications.data.NotificationsApi
import com.us.android.core.notifications.data.NotificationsRepository
import com.us.android.core.notifications.data.UnreadBadge
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * The inbox's EFFECTS — Slice D.
 *
 * The reducer tests cover the arithmetic. These cover what the ViewModel does
 * with it: the ordering between an optimistic edit and the request that
 * confirms it, and — the part no reducer can see — keeping the SHARED badge in
 * step so the feed and the inbox never disagree about the number.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class NotificationsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private fun row(
        id: String,
        isRead: Boolean = false,
        kind: NotificationKind = NotificationKind.Comment,
        entityType: String = "post",
        entityId: String = "post-1",
    ) = Notification(
        id = id,
        bucket = 202608,
        ts = "ts-$id",
        kind = kind,
        actorUserId = "actor",
        entityType = entityType,
        entityId = entityId,
        target = NotificationTarget.Post("post-1"),
        isRead = isRead,
        createdAt = "2026-08-22T17:10:21.526Z",
    )

    /** A repository whose every answer the test dictates. */
    private class FakeRepository : NotificationsRepository(
        UnusedApi(),
        ErrorMapper(Json { ignoreUnknownKeys = true }),
    ) {
        var pages = ArrayDeque<AppResult<NotificationPage>>()
        var count: AppResult<Int> = AppResult.Success(0)
        var markReadResult: AppResult<Unit> = AppResult.Success(Unit)
        var markAllResult: AppResult<Unit> = AppResult.Success(Unit)
        val markedRead = mutableListOf<NotificationAddress>()
        var markAllCalls = 0
        var pageCalls = 0

        override suspend fun page(limit: Int, cursor: String?): AppResult<NotificationPage> {
            pageCalls++
            return if (pages.isEmpty()) {
                AppResult.Success(NotificationPage(emptyList(), null))
            } else {
                pages.removeFirst()
            }
        }

        override suspend fun unreadCount(): AppResult<Int> = count

        override suspend fun markRead(address: NotificationAddress): AppResult<Unit> {
            markedRead += address
            return markReadResult
        }

        override suspend fun markAllRead(): AppResult<Unit> {
            markAllCalls++
            return markAllResult
        }
    }

    /** Never called: every method the ViewModel reaches is overridden above. */
    private class UnusedApi : NotificationsApi {
        override suspend fun list(limit: Int, cursor: String?) = error("not used")
        override suspend fun unreadCount() = error("not used")
        override suspend fun markRead(
            body: com.us.android.core.notifications.data.MarkReadRequest,
        ) = error("not used")

        override suspend fun markAllRead() = error("not used")
    }

    /** Row actions whose every answer the test dictates. */
    private class FakeActions : NotificationActions {
        var pending: AppResult<Set<String>> = AppResult.Success(emptySet())
        var following: Set<String> = emptySet()
        var result: AppResult<Unit> = AppResult.Success(Unit)
        val calls = mutableListOf<String>()
        val followLookups = mutableListOf<Set<String>>()

        override suspend fun pendingRequestIds(): AppResult<Set<String>> = pending

        override suspend fun alreadyFollowing(actorIds: Set<String>): Set<String> {
            followLookups += actorIds
            return following
        }

        override suspend fun follow(userId: String) = result.also { calls += "follow($userId)" }

        override suspend fun acceptRequest(conversationId: String) =
            result.also { calls += "accept($conversationId)" }

        override suspend fun declineRequest(conversationId: String) =
            result.also { calls += "decline($conversationId)" }

        override suspend fun blockRequest(conversationId: String) =
            result.also { calls += "block($conversationId)" }

        override suspend fun acceptFollowRequest(requesterId: String) =
            result.also { calls += "acceptFollow($requesterId)" }

        override suspend fun declineFollowRequest(requesterId: String) =
            result.also { calls += "declineFollow($requesterId)" }
    }

    private fun badge(repository: FakeRepository) = UnreadBadge(repository)

    private fun viewModel(
        repository: FakeRepository = FakeRepository(),
        badge: UnreadBadge = badge(repository),
        actions: FakeActions = FakeActions(),
    ) = NotificationsViewModel(repository, badge, actions)

    private fun request(id: String, conversationId: String) =
        row(id, kind = NotificationKind.MessageRequest, entityType = "conversation", entityId = conversationId)

    private fun failure() = AppResult.Failure(AppError.Unknown(code = "INTERNAL_ERROR", statusCode = null))

    // ── Loading ─────────────────────────────────────────────────────────

    @Test
    fun `the first load fetches rows and the count`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1"), row("n2")), "c1")))
            count = AppResult.Success(7)
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        assertThat(vm.state.value.items.map { it.id }).containsExactly("n1", "n2").inOrder()
        assertThat(vm.state.value.unreadCount).isEqualTo(7)
        assertThat(vm.state.value.hasMore).isTrue()
    }

    /**
     * The count is NOT derived from the loaded page.
     *
     * Two rows on screen, twelve unread in the inbox. A client that counted the
     * page would badge "2" and understate it for every user with more than one
     * page of unread notifications — which is every active user.
     */
    @Test
    fun `the badge counts the whole inbox and not the loaded page`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1"), row("n2")), "c1")))
            count = AppResult.Success(12)
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        assertThat(vm.state.value.items).hasSize(2)
        assertThat(vm.state.value.unreadCount).isEqualTo(12)
    }

    @Test
    fun `an empty inbox is empty rather than an error`() = runTest {
        val vm = viewModel(FakeRepository())
        advanceUntilIdle()

        assertThat(vm.state.value.isEmpty).isTrue()
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a failed first load surfaces an error`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Failure(AppError.NoNetwork()))
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        assertThat(vm.state.value.error).isNotNull()
    }

    @Test
    fun `load-more appends the next page`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), "c1")))
            pages.add(AppResult.Success(NotificationPage(listOf(row("n2")), null)))
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()

        assertThat(vm.state.value.items.map { it.id }).containsExactly("n1", "n2").inOrder()
        assertThat(vm.state.value.hasMore).isFalse()
    }

    /** No cursor means no request, however often the list is scrolled. */
    @Test
    fun `load-more at the end of the inbox issues no request`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
        }
        val vm = viewModel(repo)
        advanceUntilIdle()
        val callsAfterLoad = repo.pageCalls

        vm.loadMore()
        vm.loadMore()
        advanceUntilIdle()

        assertThat(repo.pageCalls).isEqualTo(callsAfterLoad)
    }

    // ── Read state ──────────────────────────────────────────────────────

    @Test
    fun `opening a notification marks it read immediately and tells the server`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(1)
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.onNotificationOpened(vm.state.value.items.single())

        // Optimistic: true BEFORE the request is allowed to complete.
        assertThat(vm.state.value.items.single().isRead).isTrue()
        assertThat(vm.state.value.unreadCount).isEqualTo(0)

        advanceUntilIdle()
        assertThat(repo.markedRead).containsExactly(NotificationAddress(202608, "ts-n1"))
    }

    @Test
    fun `a failed mark-read is reverted`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(1)
            markReadResult = AppResult.Failure(AppError.NoNetwork())
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.markRead(NotificationAddress(202608, "ts-n1"))
        advanceUntilIdle()

        assertThat(vm.state.value.items.single().isRead).isFalse()
        assertThat(vm.state.value.unreadCount).isEqualTo(1)
    }

    /** Tapping an already-read row issues no request at all. */
    @Test
    fun `marking an already-read row sends nothing`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1", isRead = true)), null)))
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.markRead(NotificationAddress(202608, "ts-n1"))
        advanceUntilIdle()

        assertThat(repo.markedRead).isEmpty()
    }

    @Test
    fun `mark-all-read zeroes the count and reads every row`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1"), row("n2")), null)))
            count = AppResult.Success(9)
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.markAllRead()
        advanceUntilIdle()

        assertThat(vm.state.value.items.all { it.isRead }).isTrue()
        assertThat(vm.state.value.unreadCount).isEqualTo(0)
        assertThat(repo.markAllCalls).isEqualTo(1)
    }

    @Test
    fun `a failed mark-all restores the original count`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(9)
            markAllResult = AppResult.Failure(AppError.NoNetwork())
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.markAllRead()
        advanceUntilIdle()

        assertThat(vm.state.value.unreadCount).isEqualTo(9)
        assertThat(vm.state.value.items.single().isRead).isFalse()
    }

    @Test
    fun `mark-all with nothing unread sends nothing`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1", isRead = true)), null)))
            count = AppResult.Success(0)
        }
        val vm = viewModel(repo)
        advanceUntilIdle()

        vm.markAllRead()
        advanceUntilIdle()

        assertThat(repo.markAllCalls).isEqualTo(0)
    }

    // ── The shared badge ────────────────────────────────────────────────

    /**
     * The feed's badge follows the inbox.
     *
     * This is the whole reason [UnreadBadge] is a singleton. Without it, a user
     * who clears the inbox and returns to the feed still sees the old count —
     * the app looks broken in the exact moment it did the right thing.
     */
    @Test
    fun `clearing the inbox updates the shared badge the feed renders`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1"), row("n2")), null)))
            count = AppResult.Success(2)
        }
        val badge = badge(repo)
        val vm = viewModel(repo, badge)
        advanceUntilIdle()
        assertThat(badge.count.value).isEqualTo(2)

        vm.markAllRead()
        advanceUntilIdle()

        assertThat(badge.count.value).isEqualTo(0)
    }

    @Test
    fun `marking one row read decrements the shared badge`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(5)
        }
        val badge = badge(repo)
        val vm = viewModel(repo, badge)
        advanceUntilIdle()

        vm.markRead(NotificationAddress(202608, "ts-n1"))
        advanceUntilIdle()

        assertThat(badge.count.value).isEqualTo(4)
    }

    /** A reverted mark-read puts the shared badge back too. */
    @Test
    fun `a failed mark-read restores the shared badge`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(5)
            markReadResult = AppResult.Failure(AppError.NoNetwork())
        }
        val badge = badge(repo)
        val vm = viewModel(repo, badge)
        advanceUntilIdle()

        vm.markRead(NotificationAddress(202608, "ts-n1"))
        advanceUntilIdle()

        assertThat(badge.count.value).isEqualTo(5)
    }

    /**
     * A failed count leaves the previous badge rather than zeroing it.
     *
     * Showing "nothing new" because a request failed is wrong in the one
     * direction users notice.
     */
    @Test
    fun `a failed count refresh keeps the previous badge`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
            count = AppResult.Success(4)
        }
        val badge = badge(repo)
        viewModel(repo, badge)
        advanceUntilIdle()
        assertThat(badge.count.value).isEqualTo(4)

        repo.count = AppResult.Failure(AppError.NoNetwork())
        badge.refresh()
        advanceUntilIdle()

        assertThat(badge.count.value).isEqualTo(4)
    }

    // ── Inline row actions ──────────────────────────────────────────────

    /**
     * The request buttons are offered from the SERVER's pending set, not from
     * the notification's existence: a request accepted from the messages inbox
     * must not still offer Accept here.
     */
    @Test
    fun `a message-request row learns from the server whether it is still pending`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(
                AppResult.Success(NotificationPage(listOf(request("n1", "conv-1"), request("n2", "conv-2")), null)),
            )
        }
        val actions = FakeActions().apply { pending = AppResult.Success(setOf("conv-2")) }

        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        assertThat(vm.state.value.pendingRequestIds).containsExactly("conv-2")
    }

    @Test
    fun `a page without requests never asks for the pending set`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1")), null)))
        }
        val actions = FakeActions().apply { pending = failure() }

        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        assertThat(vm.state.value.pendingRequestIds).isEmpty()
    }

    /** Accept is NOT optimistic: the row waits for the server, then settles. */
    @Test
    fun `accepting a request calls the server, reads the row and drops it from pending`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(request("n1", "conv-1")), null)))
            count = AppResult.Success(1)
        }
        val actions = FakeActions().apply { pending = AppResult.Success(setOf("conv-1")) }
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.acceptRequest(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(actions.calls).containsExactly("accept(conv-1)")
        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Accepted)
        assertThat(vm.state.value.pendingRequestIds).isEmpty()
        assertThat(repo.markedRead).containsExactly(NotificationAddress(202608, "ts-n1"))
        assertThat(vm.state.value.unreadCount).isEqualTo(0)
    }

    @Test
    fun `a failed decline leaves the request pending and says so on the row`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(request("n1", "conv-1")), null)))
        }
        val actions = FakeActions().apply {
            pending = AppResult.Success(setOf("conv-1"))
            result = failure()
        }
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.declineRequest(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Failed)
        assertThat(vm.state.value.pendingRequestIds).containsExactly("conv-1")
    }

    @Test
    fun `blocking from a request row calls block, not decline`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(request("n1", "conv-1")), null)))
        }
        val actions = FakeActions().apply { pending = AppResult.Success(setOf("conv-1")) }
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.blockRequest(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(actions.calls).containsExactly("block(conv-1)")
        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Blocked)
    }

    /** Follow rows look up the graph so a follower already followed back shows "Following". */
    @Test
    fun `follow rows learn which actors are already followed`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1", kind = NotificationKind.Follow)), null)))
        }
        val actions = FakeActions().apply { following = setOf("actor") }

        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        assertThat(actions.followLookups).containsExactly(setOf("actor"))
        assertThat(vm.state.value.followingIds).containsExactly("actor")
    }

    @Test
    fun `following back settles the row on the server's answer`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(AppResult.Success(NotificationPage(listOf(row("n1", kind = NotificationKind.Follow)), null)))
        }
        val actions = FakeActions()
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.follow(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(actions.calls).containsExactly("follow(actor)")
        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Followed)
        assertThat(vm.state.value.followingIds).contains("actor")
    }

    // ── Follow requests (private accounts) ─────────────────────────────

    /** The requester is the notification's actor, not its entity. */
    @Test
    fun `accepting a follow request calls the server with the requester's id`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(
                AppResult.Success(NotificationPage(listOf(row("n1", kind = NotificationKind.FollowRequest)), null)),
            )
            count = AppResult.Success(1)
        }
        val actions = FakeActions()
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.acceptFollowRequest(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(actions.calls).containsExactly("acceptFollow(actor)")
        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Accepted)
        assertThat(repo.markedRead).containsExactly(NotificationAddress(202608, "ts-n1"))
    }

    @Test
    fun `a failed decline on a follow request row says so and can be retried`() = runTest {
        val repo = FakeRepository().apply {
            pages.add(
                AppResult.Success(NotificationPage(listOf(row("n1", kind = NotificationKind.FollowRequest)), null)),
            )
        }
        val actions = FakeActions().apply { result = failure() }
        val vm = viewModel(repo, actions = actions)
        advanceUntilIdle()

        vm.declineFollowRequest(vm.state.value.items.single())
        advanceUntilIdle()

        assertThat(actions.calls).containsExactly("declineFollow(actor)")
        assertThat(vm.state.value.rowActions["n1"]).isEqualTo(RowActionState.Failed)
    }
}
