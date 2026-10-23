package com.us.android.core.memberships

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.TierDto
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.memberships.payment.MembershipPaymentOpener
import com.us.android.core.memberships.ui.JoinMembershipViewModel
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The Join sheet's state holder (paid memberships, 2026-10-23): the levels a
 * post's lock offers, which of them open that post (access is by level price,
 * service/entitlement.go), and that the unlock is announced only when the
 * server said paid — or that the viewer already was a member.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class JoinMembershipViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeMembershipsApi().apply {
        creatorTiers = listOf(
            TierDto(id = "superfan", creatorId = "c1", name = "Superfan", pricePaise = 49_900, isActive = true),
            TierDto(id = "supporter", creatorId = "c1", name = "Supporter", pricePaise = 9_900, isActive = true),
            TierDto(id = "fan", creatorId = "c1", name = "Fan", pricePaise = 4_900, isActive = true),
            TierDto(id = "gone", creatorId = "c1", name = "Gone", pricePaise = 2_900, isActive = false),
        )
    }
    private val repository = MembershipsRepository(api, ErrorMapper(NetworkModule.provideJson()))
    private val handoff = PaymentHandoff()
    private val events = MembershipEvents()
    private val coordinator = confirmingOnlyCoordinator()

    private fun vm() = JoinMembershipViewModel(
        repository = repository,
        opener = MembershipPaymentOpener(coordinator, handoff),
        events = events,
        handoff = handoff,
        payments = coordinator,
        savedState = SavedStateHandle(),
    )

    @Test
    fun `the levels are the active ones, cheapest first, and the cheapest that opens the post is picked`() = runTest(
        dispatcher
    ) {
        val model = vm()
        model.bind("c1", requiredLevelId = "supporter")
        advanceUntilIdle()

        val state = model.state.value
        assertThat(state.levels.map { it.id }).containsExactly("fan", "supporter", "superfan").inOrder()
        assertThat(state.selectedId).isEqualTo("supporter")
        assertThat(state.opensThisPost(state.levels[0])).isFalse()
        assertThat(state.opensThisPost(state.levels[1])).isTrue()
        assertThat(state.opensThisPost(state.levels[2])).isTrue()
        assertThat(state.canJoin).isTrue()
    }

    @Test
    fun `a creator with no levels says so, and offers no Join`() = runTest(dispatcher) {
        api.creatorTiers = emptyList()
        val model = vm()
        model.bind("c1", requiredLevelId = null)
        advanceUntilIdle()

        assertThat(model.state.value.unavailable).isNotNull()
        assertThat(model.state.value.canJoin).isFalse()
    }

    @Test
    fun `the unlock is announced only once the server says paid`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        api.statusAnswers += { payment("confirming") }
        api.statusAnswers += { payment("paid") }
        val announced = mutableListOf<String>()
        backgroundScope.launch(
            UnconfinedTestDispatcher(testScheduler)
        ) { events.accessChanged.collect { announced += it } }
        val model = vm()
        model.bind("c1", requiredLevelId = null)
        advanceUntilIdle()

        model.join()
        runCurrent()
        val opening = model.state.value.pay as MembershipPayState.OpeningPayment
        assertThat(api.subscribeCalls.single().third).isEqualTo("fan")
        assertThat(announced).isEmpty()

        handoff.publish(PaymentHandoffEvent.SheetClosed(opening.request.attempt))
        runCurrent()
        assertThat(announced).isEmpty()
        advanceUntilIdle()

        assertThat(model.state.value.pay).isEqualTo(MembershipPayState.Paid)
        assertThat(announced).containsExactly("c1")
    }

    @Test
    fun `already a member unlocks the stale lock without a payment`() = runTest(dispatcher) {
        api.subscribeAnswers += { refused(409, "ALREADY_SUBSCRIBED") }
        val announced = mutableListOf<String>()
        backgroundScope.launch(
            UnconfinedTestDispatcher(testScheduler)
        ) { events.accessChanged.collect { announced += it } }
        val model = vm()
        model.bind("c1", requiredLevelId = null)
        advanceUntilIdle()

        model.join()
        advanceUntilIdle()

        assertThat((model.state.value.pay as MembershipPayState.Refused).code).isEqualTo("ALREADY_SUBSCRIBED")
        assertThat(announced).containsExactly("c1")
        assertThat(api.statusReads).isEqualTo(0)
    }

    @Test
    fun `a pick cannot change under a payment in flight`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        val model = vm()
        model.bind("c1", requiredLevelId = null)
        advanceUntilIdle()
        model.join()
        runCurrent()

        model.select("superfan")
        assertThat(model.state.value.selectedId).isEqualTo("fan")
        model.join()
        runCurrent()
        assertThat(api.subscribeCalls).hasSize(1)
    }
}
