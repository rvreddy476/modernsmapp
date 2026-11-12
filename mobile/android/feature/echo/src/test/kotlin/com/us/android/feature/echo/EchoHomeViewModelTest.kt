package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.NotePage
import com.us.android.feature.echo.data.Timeline
import com.us.android.feature.echo.ui.ComposeGate
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.ComposeRequest
import com.us.android.feature.echo.ui.EchoHomeViewModel
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Echo's home: Following first, For You read when opened, cursor paging,
 * pull to refresh, the heart and repost, and the compose gate.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class EchoHomeViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeEchoRepository()
    private val media = mockk<EchoMediaUrls>(relaxed = true)

    private fun model() = EchoHomeViewModel(repo, media)

    @Test
    fun `Following is first and read at once, For You only when opened`() = runTest(dispatcher) {
        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Success(NotePage(listOf(note("a")), null))
        val vm = model()
        advanceUntilIdle()
        assertThat(vm.state.value.timeline).isEqualTo(Timeline.FOLLOWING)
        assertThat(vm.following.value.items.map { it.id }).containsExactly("a")
        assertThat(repo.timelineCalls).containsExactly(Timeline.FOLLOWING to null)

        vm.select(Timeline.FOR_YOU)
        advanceUntilIdle()
        assertThat(repo.timelineCalls).contains(Timeline.FOR_YOU to null)
        vm.select(Timeline.FOLLOWING)
        advanceUntilIdle()
        assertThat(repo.timelineCalls.count { it.first == Timeline.FOLLOWING }).isEqualTo(1)
    }

    @Test
    fun `pages follow the cursor and never repeat a note`() = runTest(dispatcher) {
        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Success(NotePage(listOf(note("a"), note("b")), "c2"))
        repo.timelines[Timeline.FOLLOWING to "c2"] = AppResult.Success(NotePage(listOf(note("b"), note("c")), null))
        val vm = model()
        advanceUntilIdle()
        assertThat(vm.following.value.canLoadMore).isTrue()
        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.following.value.items.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.following.value.canLoadMore).isFalse()
        vm.loadMore()
        advanceUntilIdle()
        assertThat(repo.timelineCalls.count { it == (Timeline.FOLLOWING to "c2") }).isEqualTo(1)
    }

    @Test
    fun `a failed later page keeps what is shown, a refresh replaces it`() = runTest(dispatcher) {
        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Success(NotePage(listOf(note("a")), "c2"))
        repo.timelines[Timeline.FOLLOWING to "c2"] = AppResult.Failure(AppError.NoNetwork())
        val vm = model()
        advanceUntilIdle()
        vm.loadMore()
        advanceUntilIdle()
        assertThat(vm.following.value.items.map { it.id }).containsExactly("a")
        assertThat(vm.following.value.moreError).isEqualTo(EchoWords.NO_NETWORK)

        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Success(NotePage(listOf(note("z")), null))
        vm.refresh()
        advanceUntilIdle()
        assertThat(vm.following.value.items.map { it.id }).containsExactly("z")
        assertThat(vm.following.value.refreshing).isFalse()
    }

    @Test
    fun `a first page that fails says why in the contract's words`() = runTest(dispatcher) {
        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Failure(AppError.Server(503, "ECHO_NOT_ENABLED"))
        val vm = model()
        advanceUntilIdle()
        assertThat(vm.following.value.error).isEqualTo("Echo isn't available right now.")
        assertThat(vm.following.value.loaded).isFalse()
    }

    @Test
    fun `the heart moves at once and goes back when the server refuses`() = runTest(dispatcher) {
        repo.timelines[Timeline.FOLLOWING to null] = AppResult.Success(NotePage(listOf(note("a", likes = 3)), null))
        val vm = model()
        advanceUntilIdle()
        vm.toggleLike(vm.following.value.items.first())
        advanceUntilIdle()
        assertThat(vm.following.value.items.first().liked).isTrue()
        assertThat(vm.following.value.items.first().likeCount).isEqualTo(4)
        assertThat(repo.likes).containsExactly("a" to true)

        repo.repostResult = AppResult.Failure(AppError.RateLimited(retryAfterSeconds = 30))
        vm.toggleRepost(vm.following.value.items.first())
        advanceUntilIdle()
        assertThat(vm.following.value.items.first().reposted).isFalse()
        assertThat(vm.following.value.items.first().repostCount).isEqualTo(0)
        assertThat(vm.state.value.message).isEqualTo("You're posting too fast. Try again in 30 seconds.")
    }

    @Test
    fun `compose opens the composer for Premium only`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.compose()
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isEqualTo(ComposeRequest(ComposeMode.NEW))
        assertThat(vm.state.value.blocked).isNull()
    }

    @Test
    fun `premium required shows the sheet and never the composer`() = runTest(dispatcher) {
        repo.eligibility = AppResult.Success(NOT_PREMIUM)
        val vm = model()
        advanceUntilIdle()
        vm.compose()
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isNull()
        assertThat(
            vm.state.value.blocked
        ).isEqualTo(ComposeGate.Blocked(EchoWords.PREMIUM_REQUIRED, EchoWords.PREMIUM_REQUIRED_DETAIL))

        vm.dismissBlocked()
        vm.quote(note("q"))
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isNull()
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.PREMIUM_REQUIRED)
    }

    @Test
    fun `too new and suspended say so, a failed read keeps the composer shut`() = runTest(dispatcher) {
        repo.eligibility = AppResult.Success(TOO_NEW)
        val vm = model()
        advanceUntilIdle()
        vm.compose()
        advanceUntilIdle()
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.ACCOUNT_TOO_NEW)

        vm.dismissBlocked()
        repo.eligibility = AppResult.Success(SUSPENDED)
        vm.compose()
        advanceUntilIdle()
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.SUSPENDED)

        vm.dismissBlocked()
        repo.eligibility = AppResult.Failure(AppError.Server(503, "PREMIUM_CHECK_UNAVAILABLE"))
        vm.compose()
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isNull()
        assertThat(
            vm.state.value.message
        ).isEqualTo("We couldn't check your Premium membership. Try again in a moment.")
    }
}
