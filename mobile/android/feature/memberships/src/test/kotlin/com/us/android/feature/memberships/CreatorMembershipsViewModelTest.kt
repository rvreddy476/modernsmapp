package com.us.android.feature.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.EligibilityItem
import com.us.android.core.memberships.data.MembershipEligibility
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.model.SessionState
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.memberships.creator.CreatorMembershipsViewModel
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The creator's Memberships page (paid memberships, 2026-10-23): the form
 * refuses here what the server would refuse (so a bad level is never sent),
 * the first level's eligibility refusal says what is missing, and the add
 * and on/off controls follow the three-active-levels rule.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CreatorMembershipsViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val session = object : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> = MutableStateFlow(SessionState.Authenticated("me", "s1"))
    }
    private val repository = mockk<MembershipsRepository>(relaxed = true)

    private fun level(id: String, active: Boolean = true, price: Long = 9_900) =
        MembershipLevel(
            id = id,
            creatorId = "me",
            name = id,
            pricePaise = price,
            perks = emptyList(),
            memberCount = 2,
            isActive = active
        )

    private fun given(
        levels: List<MembershipLevel>,
        eligible: Boolean = true,
        missing: List<EligibilityItem> = emptyList(),
    ) {
        coEvery { repository.myLevels() } returns AppResult.Success(levels)
        coEvery { repository.eligibility("me") } returns AppResult.Success(MembershipEligibility(eligible, missing))
    }

    @Test
    fun `a level the server would refuse is never sent, and every problem is said`() = runTest(dispatcher) {
        given(emptyList())
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()

        model.startNew()
        model.editDraft(name = " ", price = "10", perks = "x".repeat(101))
        model.saveDraft()
        advanceUntilIdle()

        assertThat(model.state.value.draft?.problems).hasSize(3)
        coVerify(exactly = 0) { repository.createLevel(any(), any(), any()) }
    }

    @Test
    fun `a valid level is sent in paise with its perks, one per line`() = runTest(dispatcher) {
        given(emptyList())
        coEvery { repository.createLevel(any(), any(), any()) } returns AppResult.Success(level("new"))
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()

        model.startNew()
        model.editDraft(name = "Supporter", price = "99", perks = "Early access\n\nMonthly Q&A")
        model.saveDraft()
        advanceUntilIdle()

        coVerify { repository.createLevel("Supporter", 9_900, listOf("Early access", "", "Monthly Q&A")) }
        assertThat(model.state.value.draft).isNull()
        assertThat(model.state.value.message).isEqualTo("Level added.")
    }

    @Test
    fun `the first level refused for eligibility shows what is missing`() = runTest(dispatcher) {
        given(emptyList())
        coEvery { repository.createLevel(any(), any(), any()) } returns AppResult.Failure(
            AppError.Forbidden(
                code = "MEMBERSHIPS_NOT_ELIGIBLE",
                details = mapOf("missing" to """["pan","followers"]"""),
            ),
        )
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()

        model.startNew()
        model.editDraft(name = "Supporter", price = "99")
        model.saveDraft()
        advanceUntilIdle()

        val eligibility = model.state.value.eligibility!!
        assertThat(eligibility.eligible).isFalse()
        assertThat(eligibility.missing).containsExactly(EligibilityItem.Pan, EligibilityItem.Followers).inOrder()
        assertThat(model.state.value.canAddLevel).isFalse()
    }

    @Test
    fun `a creator not yet eligible with no levels cannot start one, and one with levels can`() = runTest(dispatcher) {
        given(emptyList(), eligible = false, missing = listOf(EligibilityItem.Age))
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()
        assertThat(model.state.value.canAddLevel).isFalse()
        model.startNew()
        assertThat(model.state.value.draft).isNull()

        given(listOf(level("a")), eligible = false, missing = listOf(EligibilityItem.Followers))
        model.refresh()
        advanceUntilIdle()
        assertThat(model.state.value.canAddLevel).isTrue()
    }

    @Test
    fun `three active levels is the most, and turning a fourth on is refused here`() = runTest(dispatcher) {
        val off = level("off", active = false)
        given(listOf(level("a"), level("b"), level("c"), off))
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()

        assertThat(model.state.value.canAddLevel).isFalse()
        model.setActive(off, active = true)
        advanceUntilIdle()

        coVerify(exactly = 0) { repository.updateLevel(any(), any(), any(), any(), any()) }
        assertThat(model.state.value.message).isEqualTo("You can have up to 3 active levels.")
        // The levels are listed active first.
        assertThat(model.state.value.levels.last().id).isEqualTo("off")
    }

    @Test
    fun `turning a level off sends is_active false alone`() = runTest(dispatcher) {
        val a = level("a")
        given(listOf(a))
        coEvery { repository.updateLevel("a", isActive = false) } returns AppResult.Success(null)
        val model = CreatorMembershipsViewModel(repository, session)
        advanceUntilIdle()

        model.setActive(a, active = false)
        advanceUntilIdle()

        coVerify { repository.updateLevel("a", null, null, null, false) }
    }
}
