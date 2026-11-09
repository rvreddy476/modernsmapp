package com.us.android.feature.rider.offers

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.OfferResponseDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.food.repository.ServerTimed
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.rider.job.routeAssignment
import com.us.android.feature.rider.location.RiderDuty
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * One job offer: counted down on the server's clock (food audit L1), never
 * refused on the device, and an accept whose answer is lost still lands the
 * rider on the job (M3).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class OfferViewModelTest {
    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val rider = mockk<RiderRepository>()
    private val duty = RiderDuty()
    private val offer = goldenOffer()
    private val lost = FoodResult.Failure(FoodError.Network(IOException("read timed out")))

    private fun model(
        store: ViewModelStore,
        clock: HandClock,
        serverSays: java.time.Instant?,
        listed: DeliveryOfferDto = offer,
    ): OfferViewModel {
        coEvery { rider.offers() } returns FoodResult.Success(ServerTimed(listOf(listed), serverSays))
        coEvery { rider.currentAssignment() } returns FoodResult.Success(null)
        coEvery { rider.assignments() } returns FoodResult.Success(emptyList())
        val watch = offerWatch(rider, duty, clock)
        return OfferViewModel(SavedStateHandle(mapOf("offerId" to offer.id)), rider, duty, watch, clock)
            .also { store.put("offer", it) }
    }

    @Test
    fun `a phone whose clock runs fast still shows the time the server allows`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            // An older server (no seconds_to_expiry): the device reads 06:30:35, past the 06:30:20
            // expiry; the server's Date says 06:30:00.
            val older = offer.copy(secondsToExpiry = null)
            val clock = HandClock(GOLDEN_EXPIRY.plusSeconds(15))
            val vm = model(store, clock, serverSays = GOLDEN_EXPIRY.minusSeconds(20), listed = older)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()

            assertThat(vm.state.value.offer).isEqualTo(older)
            assertThat(vm.state.value.window).isEqualTo(OfferWindow.Open(20, urgent = false))
            assertThat(vm.state.value.window.canRespond).isTrue()
        } finally { store.clear() }
    }

    @Test
    fun `the countdown runs from the server's seconds_to_expiry, whatever the phone's clock and Date say`() =
        runTest(dispatcher) {
            val store = ViewModelStore()
            try {
                // The phone is an hour slow and the response carried no Date header: expires_at
                // would read an hour and 20 s. The server said 20 s.
                val clock = HandClock(GOLDEN_EXPIRY.minusSeconds(3_600))
                val vm = model(store, clock, serverSays = null)
                backgroundScope.launch { vm.state.collect {} }
                runCurrent()
                assertThat(vm.state.value.window).isEqualTo(OfferWindow.Open(20, urgent = false))

                clock.now = clock.now.plusSeconds(12)
                advanceTimeBy(1_001)
                runCurrent()
                assertThat(vm.state.value.window).isEqualTo(OfferWindow.Open(8, urgent = true))
            } finally { store.clear() }
        }

    @Test
    fun `seconds_to_expiry is read on the server's clock when a Date header is present`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            // The phone is an hour slow; the Date header puts the server at 06:30:00. The server said 20 s.
            val clock = HandClock(GOLDEN_EXPIRY.minusSeconds(3_620))
            val vm = model(store, clock, serverSays = GOLDEN_EXPIRY.minusSeconds(20))
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()

            assertThat(vm.state.value.window).isEqualTo(OfferWindow.Open(20, urgent = false))
        } finally { store.clear() }
    }

    @Test
    fun `accept is sent even when the countdown reads expired, and the server decides`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val clock = HandClock(GOLDEN_EXPIRY.plusSeconds(5))
            // The server says no time is left.
            val vm = model(store, clock, serverSays = null, listed = offer.copy(secondsToExpiry = 0))
            val accepted = OfferResponseDto(offerId = offer.id, status = "accepted")
            coEvery { rider.acceptOffer(offer.id) } returns FoodResult.Success(accepted)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            assertThat(vm.state.value.window).isEqualTo(OfferWindow.Expired)

            vm.accept()
            runCurrent()

            coVerify(exactly = 1) { rider.acceptOffer(offer.id) }
            assertThat(vm.outcome.first()).isEqualTo(OfferOutcome.Accepted)
        } finally { store.clear() }
    }

    @Test
    fun `an accept whose answer is lost but which landed puts the rider on the job`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store, HandClock(GOLDEN_EXPIRY.minusSeconds(15)), serverSays = null)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.acceptOffer(offer.id) } returns lost
            val landed = routeAssignment().copy(orderId = offer.orderId)
            coEvery { rider.currentAssignment() } returns FoodResult.Success(landed)

            vm.accept()
            runCurrent()

            coVerify(exactly = 2) { rider.acceptOffer(offer.id) }
            assertThat(duty.onJob.value).isTrue()
            assertThat(vm.outcome.first()).isEqualTo(OfferOutcome.Accepted)
        } finally { store.clear() }
    }

    @Test
    fun `a busy rider is told to finish the current delivery, and stays on the offer`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store, HandClock(GOLDEN_EXPIRY.minusSeconds(15)), serverSays = null)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.acceptOffer(offer.id) } returns FoodResult.Failure(
                FoodError.Unexpected(409, "WORK_BUSY", "partner work busy"),
            )

            vm.accept()
            runCurrent()

            coVerify(exactly = 1) { rider.acceptOffer(offer.id) }
            assertThat(duty.onJob.value).isFalse()
            assertThat(vm.state.value.message?.text).isEqualTo("Finish your current delivery before taking another.")
            assertThat(vm.state.value.gone).isFalse()
            assertThat(vm.state.value.responding).isFalse()
        } finally { store.clear() }
    }

    @Test
    fun `an offer taken by someone else is gone`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store, HandClock(GOLDEN_EXPIRY.minusSeconds(15)), serverSays = null)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()
            coEvery { rider.acceptOffer(offer.id) } returns FoodResult.Failure(
                FoodError.Unexpected(409, "FOOD_ORDER_STATUS_CONFLICT", "assignment held by another partner"),
            )

            vm.accept()
            runCurrent()

            assertThat(vm.state.value.gone).isTrue()
            assertThat(vm.state.value.message?.text).isEqualTo(OfferAcceptance.GONE)
        } finally { store.clear() }
    }
}
