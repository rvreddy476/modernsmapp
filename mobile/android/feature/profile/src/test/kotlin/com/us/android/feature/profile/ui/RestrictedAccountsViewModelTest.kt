package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.HiddenAuthorsSource
import com.us.android.core.feed.data.HiddenChannel
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.BlockedAccountsSource
import com.us.android.core.profile.data.RestrictedAccount
import com.us.android.core.profile.data.RestrictedAccounts
import com.us.android.core.testing.MainDispatcherRule
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Settings › Privacy › Blocked accounts and Settings › Hidden channels
 * (2026-10-22): loading, empty, error with retry, success, and each undo —
 * the row goes at once and comes back where it was when the server refuses.
 */
class RestrictedAccountsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private fun account(id: String) = RestrictedAccount(userId = id, displayName = "Name $id", username = id, avatarMediaId = null)

    private class FakeBlocked : BlockedAccountsSource {
        val loads = ArrayDeque<AppResult<RestrictedAccounts>>()
        val viewers = mutableListOf<String>()
        var unblockAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var unmuteAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var gate: CompletableDeferred<Unit>? = null
        val calls = mutableListOf<String>()

        override suspend fun load(viewerId: String): AppResult<RestrictedAccounts> {
            viewers += viewerId
            return loads.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())
        }

        override suspend fun unblock(userId: String): AppResult<Unit> {
            calls += "unblock:$userId"
            gate?.await()
            return unblockAnswer
        }

        override suspend fun unmute(userId: String): AppResult<Unit> {
            calls += "unmute:$userId"
            gate?.await()
            return unmuteAnswer
        }
    }

    private val session = mockk<SessionStateProvider> {
        every { sessionState } returns MutableStateFlow(
            SessionState.Authenticated(userId = "me", sessionId = "s1"),
        )
    }

    private val blocked = FakeBlocked()

    private fun blockedVm() = BlockedAccountsViewModel(blocked, session)

    @Test
    fun `the list loads the viewer's blocks and mutes`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(listOf(account("a")), listOf(account("c"))))

        val state = blockedVm().state.value

        assertThat(blocked.viewers).containsExactly("me")
        assertThat(state.loading).isFalse()
        assertThat(state.blocked.map { it.userId }).containsExactly("a")
        assertThat(state.muted.map { it.userId }).containsExactly("c")
        assertThat(state.isEmpty).isFalse()
    }

    @Test
    fun `nobody blocked is the empty state`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(emptyList(), emptyList()))

        assertThat(blockedVm().state.value.isEmpty).isTrue()
    }

    @Test
    fun `a failed load is the error, and retry reads again`() = runTest {
        val vm = blockedVm()
        assertThat(vm.state.value.error).isNotNull()

        blocked.loads += AppResult.Success(RestrictedAccounts(listOf(account("a")), emptyList()))
        vm.refresh()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.blocked).hasSize(1)
    }

    @Test
    fun `unblock asks first, then takes the row out at once`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(listOf(account("a"), account("b")), emptyList()))
        blocked.gate = CompletableDeferred()
        val vm = blockedVm()

        vm.askUnblock(account("a"))
        assertThat(vm.state.value.confirmingUnblock?.userId).isEqualTo("a")
        assertThat(blocked.calls).isEmpty()
        vm.unblock()

        assertThat(vm.state.value.confirmingUnblock).isNull()
        assertThat(vm.state.value.blocked.map { it.userId }).containsExactly("b")
        blocked.gate!!.complete(Unit)
        assertThat(blocked.calls).containsExactly("unblock:a")
        assertThat(vm.state.value.blocked.map { it.userId }).containsExactly("b")
    }

    @Test
    fun `a refused unblock puts the row back where it was and says so`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(listOf(account("a"), account("b")), emptyList()))
        blocked.unblockAnswer = AppResult.Failure(AppError.RateLimited(retryAfterSeconds = 5))
        val vm = blockedVm()

        vm.askUnblock(account("a"))
        vm.unblock()

        assertThat(vm.state.value.blocked.map { it.userId }).containsExactly("a", "b").inOrder()
        assertThat(vm.message.value!!.text).contains("Too many")
    }

    @Test
    fun `cancelling the question unblocks nobody`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(listOf(account("a")), emptyList()))
        val vm = blockedVm()

        vm.askUnblock(account("a"))
        vm.cancelUnblock()

        assertThat(blocked.calls).isEmpty()
        assertThat(vm.state.value.blocked).hasSize(1)
    }

    @Test
    fun `unmute is optimistic and rolls back on a refusal`() = runTest {
        blocked.loads += AppResult.Success(RestrictedAccounts(emptyList(), listOf(account("c"), account("d"))))
        blocked.unmuteAnswer = AppResult.Failure(AppError.NoNetwork())
        blocked.gate = CompletableDeferred()
        val vm = blockedVm()

        vm.unmute(account("d"))
        assertThat(vm.state.value.muted.map { it.userId }).containsExactly("c")

        blocked.gate!!.complete(Unit)
        assertThat(vm.state.value.muted.map { it.userId }).containsExactly("c", "d").inOrder()
        assertThat(vm.message.value!!.text).contains("offline")
    }

    @Test
    fun `a row's label falls back from the name to the handle`() {
        assertThat(account("a").label()).isEqualTo("Name a")
        assertThat(RestrictedAccount("x", "", "xavi", null).label()).isEqualTo("@xavi")
        assertThat(RestrictedAccount("x", "", "", null).label()).isEqualTo("This account")
    }

    // ── Hidden channels ─────────────────────────────────────────────────

    private class FakeHidden : HiddenAuthorsSource {
        val loads = ArrayDeque<AppResult<List<HiddenChannel>>>()
        var answer: AppResult<Unit> = AppResult.Success(Unit)
        var gate: CompletableDeferred<Unit>? = null
        val shown = mutableListOf<String>()

        override suspend fun hidden(): AppResult<List<HiddenChannel>> =
            loads.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())

        override suspend fun showAgain(authorId: String): AppResult<Unit> {
            shown += authorId
            gate?.await()
            return answer
        }
    }

    private fun channel(id: String, name: String = "Channel $id") =
        HiddenChannel(authorId = id, name = name, username = "", avatarMediaId = null, hiddenAt = "2026-10-20T09:00:00Z")

    @Test
    fun `hidden channels load, and an empty list is the empty state`() = runTest {
        val hidden = FakeHidden()
        hidden.loads += AppResult.Success(listOf(channel("a")))
        assertThat(HiddenChannelsViewModel(hidden).state.value.channels.map { it.authorId }).containsExactly("a")

        hidden.loads += AppResult.Success(emptyList())
        assertThat(HiddenChannelsViewModel(hidden).state.value.isEmpty).isTrue()
    }

    @Test
    fun `a failed hidden list is the error, and retry reads again`() = runTest {
        val hidden = FakeHidden()
        val vm = HiddenChannelsViewModel(hidden)
        assertThat(vm.state.value.error).isNotNull()

        hidden.loads += AppResult.Success(listOf(channel("a")))
        vm.refresh()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.channels).hasSize(1)
    }

    @Test
    fun `show again takes the row out at once and says the channel is back`() = runTest {
        val hidden = FakeHidden()
        hidden.loads += AppResult.Success(listOf(channel("a", "Nonna"), channel("b")))
        hidden.gate = CompletableDeferred()
        val vm = HiddenChannelsViewModel(hidden)

        vm.showAgain(vm.state.value.channels.first())
        assertThat(vm.state.value.channels.map { it.authorId }).containsExactly("b")

        hidden.gate!!.complete(Unit)
        assertThat(hidden.shown).containsExactly("a")
        assertThat(vm.message.value!!.text).isEqualTo("Nonna will be recommended again")
    }

    @Test
    fun `a refused show again puts the row back where it was`() = runTest {
        val hidden = FakeHidden()
        hidden.loads += AppResult.Success(listOf(channel("a"), channel("b")))
        hidden.answer = AppResult.Failure(AppError.Server(statusCode = 502, code = "FEEDBACK_FAILED"))
        val vm = HiddenChannelsViewModel(hidden)

        vm.showAgain(vm.state.value.channels.first())

        assertThat(vm.state.value.channels.map { it.authorId }).containsExactly("a", "b").inOrder()
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `a nameless channel reads Channel, as on the web`() {
        assertThat(channel("a", name = "").label()).isEqualTo("Channel")
    }
}
