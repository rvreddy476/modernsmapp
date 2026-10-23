package com.us.android.feature.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.CreatorMember
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.MembersPage
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.memberships.creator.CreatorMembersViewModel
import com.us.android.feature.memberships.creator.memberTimeLine
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The creator's Members list (2026-10-23): the first page, then the next by
 * the server's cursor until there is none; a member already shown is never
 * repeated; a failed later page keeps what is shown and offers Try again.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CreatorMembersViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repository = mockk<MembershipsRepository>()

    private fun member(sub: String, status: MembershipStatus = MembershipStatus.Active, until: String? = "2026-11-20T09:30:00Z") =
        CreatorMember(
            subscriptionId = sub,
            userId = "u-$sub",
            username = "user_$sub",
            name = "Member $sub",
            avatarUrl = null,
            levelId = "t1",
            levelName = "Supporter",
            pricePaise = 9_900,
            status = status,
            since = "2026-10-20T09:30:00Z",
            accessUntil = until,
        )

    @Test
    fun `pages follow the cursor until the last, never repeating a member`() = runTest(dispatcher) {
        coEvery { repository.members(cursor = null) } returns AppResult.Success(MembersPage(listOf(member("1"), member("2")), "c2"))
        coEvery { repository.members(cursor = "c2") } returns AppResult.Success(MembersPage(listOf(member("2"), member("3")), null))
        val model = CreatorMembersViewModel(repository)
        advanceUntilIdle()
        assertThat(model.state.value.members.map { it.subscriptionId }).containsExactly("1", "2").inOrder()
        assertThat(model.state.value.canLoadMore).isTrue()

        model.loadMore()
        advanceUntilIdle()
        assertThat(model.state.value.members.map { it.subscriptionId }).containsExactly("1", "2", "3").inOrder()
        assertThat(model.state.value.nextCursor).isNull()
        assertThat(model.state.value.canLoadMore).isFalse()

        // The last page is in: asking again asks nothing.
        model.loadMore()
        advanceUntilIdle()
        coVerify(exactly = 1) { repository.members(cursor = "c2") }
    }

    @Test
    fun `a failed later page keeps the list and Try again asks for it again`() = runTest(dispatcher) {
        coEvery { repository.members(cursor = null) } returns AppResult.Success(MembersPage(listOf(member("1")), "c2"))
        coEvery { repository.members(cursor = "c2") } returns AppResult.Failure(AppError.NoNetwork())
        val model = CreatorMembersViewModel(repository)
        advanceUntilIdle()
        model.loadMore()
        advanceUntilIdle()
        assertThat(model.state.value.members).hasSize(1)
        assertThat(model.state.value.moreError).contains("offline")
        // No automatic retry loop while the error stands.
        assertThat(model.state.value.canLoadMore).isFalse()

        coEvery { repository.members(cursor = "c2") } returns AppResult.Success(MembersPage(listOf(member("2")), null))
        model.retryMore()
        advanceUntilIdle()
        assertThat(model.state.value.members.map { it.subscriptionId }).containsExactly("1", "2").inOrder()
        assertThat(model.state.value.moreError).isNull()
    }

    @Test
    fun `a failed first page says so, and refresh starts again`() = runTest(dispatcher) {
        coEvery { repository.members(cursor = null) } returns
            AppResult.Failure(AppError.Unknown(code = "MEMBERSHIPS_NOT_ENABLED", statusCode = 503))
        val model = CreatorMembersViewModel(repository)
        advanceUntilIdle()
        assertThat(model.state.value.loadError).isEqualTo("Memberships aren't available yet.")
        assertThat(model.state.value.members).isEmpty()

        coEvery { repository.members(cursor = null) } returns AppResult.Success(MembersPage(emptyList(), null))
        model.refresh()
        advanceUntilIdle()
        assertThat(model.state.value.loadError).isNull()
        assertThat(model.state.value.loading).isFalse()
        assertThat(model.state.value.members).isEmpty()
    }

    @Test
    fun `the row's time line says since when and until when`() {
        assertThat(memberTimeLine(member("1"))).isEqualTo("Since 20 Oct 2026 · Active until 20 Nov 2026")
        assertThat(memberTimeLine(member("2", MembershipStatus.CancelledAtPeriodEnd)))
            .isEqualTo("Since 20 Oct 2026 · Cancelled · access until 20 Nov 2026")
        assertThat(memberTimeLine(member("3", MembershipStatus.Expired, until = null))).isEqualTo("Since 20 Oct 2026 · Ended")
    }
}
