package com.us.android.feature.tube.ui.credits

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.credits.CreditRequest
import com.us.android.core.model.CreditedUser
import com.us.android.core.network.Paged
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.tube.data.CreditRequestsSource
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The credit requests screen (2026-10-23): loading, empty, error with
 * retry, paging, and its three optimistic answers — Accept, Decline and
 * "Remove me from this video" — each rolled back when refused.
 */
class CreditRequestsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeSource : CreditRequestsSource {
        val pages = ArrayDeque<AppResult<Paged<CreditRequest>>>()
        val cursors = mutableListOf<String?>()
        var answer: AppResult<Unit> = AppResult.Success(Unit)
        var gate: CompletableDeferred<Unit>? = null
        val calls = mutableListOf<String>()

        override suspend fun requests(cursor: String?): AppResult<Paged<CreditRequest>> {
            cursors += cursor
            return pages.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())
        }

        private suspend fun answer(call: String): AppResult<Unit> {
            calls += call
            gate?.await()
            return answer
        }

        override suspend fun accept(creditId: String) = answer("accept:$creditId")
        override suspend fun decline(creditId: String) = answer("decline:$creditId")
        override suspend fun removeMe(creditId: String) = answer("remove:$creditId")
    }

    private fun request(id: String) = CreditRequest(
        id = id,
        role = "Actor",
        name = "Ravi",
        postId = "p-$id",
        postTitle = "Video $id",
        coverUrl = null,
        channelName = "Blue Hour",
        from = CreditedUser(id = "u0", username = "maker", displayName = "The Maker"),
        createdAt = "2026-10-23T10:00:00Z",
    )

    private fun page(vararg ids: String, next: String? = null): AppResult<Paged<CreditRequest>> =
        AppResult.Success(Paged(ids.map(::request), next))

    private fun loaded(vararg ids: String, next: String? = null): Pair<FakeSource, CreditRequestsViewModel> {
        val source = FakeSource().apply { pages += page(*ids, next = next) }
        return source to CreditRequestsViewModel(source)
    }

    private fun CreditRequestsViewModel.ids() = state.value.rows.map { it.request.id }

    @Test
    fun `the first page shows its rows`() = runTest {
        val (_, vm) = loaded("a", "b")

        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.ids()).containsExactly("a", "b").inOrder()
        assertThat(vm.state.value.empty).isFalse()
    }

    @Test
    fun `nothing pending is the empty state, not an error`() = runTest {
        val (_, vm) = loaded()

        assertThat(vm.state.value.empty).isTrue()
        assertThat(vm.state.value.loadError).isNull()
    }

    @Test
    fun `a failed first page offers retry, and retry loads`() = runTest {
        val source = FakeSource()
        val vm = CreditRequestsViewModel(source)
        assertThat(vm.state.value.loadError).isEqualTo("You're offline. Check your connection.")

        source.pages += page("a")
        vm.retry()

        assertThat(vm.state.value.loadError).isNull()
        assertThat(vm.ids()).containsExactly("a")
    }

    @Test
    fun `the next page is asked for with the cursor and appended`() = runTest {
        val (source, vm) = loaded("a", next = "n2")
        source.pages += page("b", "a")

        vm.loadMore()

        assertThat(source.cursors).containsExactly(null, "n2").inOrder()
        assertThat(vm.ids()).containsExactly("a", "b").inOrder()
        assertThat(vm.state.value.canLoadMore).isFalse()
    }

    @Test
    fun `accept marks the row at once and keeps it, for Remove me`() = runTest {
        val (source, vm) = loaded("a", "b")
        source.gate = CompletableDeferred()

        vm.accept("a")
        assertThat(vm.state.value.rows.first().accepted).isTrue()
        assertThat("a" in vm.state.value.busy).isTrue()
        source.gate!!.complete(Unit)

        assertThat(source.calls).containsExactly("accept:a")
        assertThat(vm.state.value.rows.first().accepted).isTrue()
        assertThat(vm.state.value.busy).isEmpty()
    }

    @Test
    fun `a refused accept goes back to pending, with a message`() = runTest {
        val (source, vm) = loaded("a")
        source.answer = AppResult.Failure(AppError.Server(statusCode = 500, code = null))

        vm.accept("a")

        assertThat(vm.state.value.rows.single().accepted).isFalse()
        assertThat(vm.state.value.message).isEqualTo("Couldn't accept that credit. Try again.")
    }

    @Test
    fun `decline takes the row away at once, and a refusal puts it back in its place`() = runTest {
        val (source, vm) = loaded("a", "b", "c")
        source.gate = CompletableDeferred()
        source.answer = AppResult.Failure(AppError.NoNetwork())

        vm.decline("b")
        assertThat(vm.ids()).containsExactly("a", "c").inOrder()
        source.gate!!.complete(Unit)

        assertThat(vm.ids()).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.state.value.message).isEqualTo("Couldn't decline that credit. Try again.")
    }

    @Test
    fun `remove me takes an accepted row away, and a refusal brings it back accepted`() = runTest {
        val (source, vm) = loaded("a")
        vm.accept("a")
        source.answer = AppResult.Failure(AppError.Server(statusCode = 503, code = null))

        vm.removeMe("a")

        assertThat(source.calls).containsExactly("accept:a", "remove:a").inOrder()
        assertThat(vm.state.value.rows.single().accepted).isTrue()
        assertThat(vm.state.value.message).isEqualTo("Couldn't remove you from that video. Try again.")

        source.answer = AppResult.Success(Unit)
        vm.removeMe("a")
        assertThat(vm.state.value.rows).isEmpty()
    }

    @Test
    fun `a request gone on the server leaves rather than coming back`() = runTest {
        val (source, vm) = loaded("a", "b")
        source.answer = AppResult.Failure(AppError.NotFound())

        vm.accept("a")

        assertThat(vm.ids()).containsExactly("b")
        assertThat(vm.state.value.message).isEqualTo("That credit is no longer there.")
    }

    @Test
    fun `a second tap while an answer is in flight does nothing`() = runTest {
        val (source, vm) = loaded("a")
        source.gate = CompletableDeferred()

        vm.accept("a")
        vm.accept("a")
        vm.decline("a")
        source.gate!!.complete(Unit)

        assertThat(source.calls).containsExactly("accept:a")
    }

    @Test
    fun `a pull keeps the rows accepted this visit`() = runTest {
        val (source, vm) = loaded("a", "b")
        vm.accept("a")
        source.pages += page("b", "c")

        vm.refresh()

        assertThat(vm.ids()).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.state.value.rows.first().accepted).isTrue()
        assertThat(vm.state.value.refreshing).isFalse()
    }

    @Test
    fun `rollback puts the row after the one that preceded it, leaving later answers alone`() {
        val a = CreditRequestRow(request("a"))
        val b = CreditRequestRow(request("b"))
        val c = CreditRequestRow(request("c"))

        val back = CreditRequestsViewModel.rollback(current = listOf(a), snapshot = listOf(a, b, c), id = "b")
        assertThat(back.map { it.request.id }).containsExactly("a", "b").inOrder()

        val first = CreditRequestsViewModel.rollback(current = listOf(c), snapshot = listOf(a, b, c), id = "a")
        assertThat(first.map { it.request.id }).containsExactly("a", "c").inOrder()
    }

    @Test
    fun `the request line says who, as what, and the name they typed`() {
        assertThat(creditRequestLine("The Maker", "Director", "Asha R."))
            .isEqualTo("The Maker credited you as Director · \"Asha R.\"")
        assertThat(creditRequestLine("", "", "")).isEqualTo("Someone credited you")
    }
}
