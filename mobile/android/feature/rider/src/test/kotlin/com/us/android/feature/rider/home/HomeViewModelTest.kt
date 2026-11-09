package com.us.android.feature.rider.home

import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.food.repository.ServerTimed
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.rider.location.DutyNotice
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.offers.GOLDEN_EXPIRY
import com.us.android.feature.rider.offers.HandClock
import com.us.android.feature.rider.offers.OfferWindow
import com.us.android.feature.rider.offers.goldenOffer
import com.us.android.feature.rider.offers.offerWatch
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/** Home's offer cards count down on the feed's clocks, and a refused Go offline says why (food server lane). */
@OptIn(ExperimentalCoroutinesApi::class)
class HomeViewModelTest {
    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val rider = mockk<RiderRepository>()
    private val duty = RiderDuty()
    private val offer = goldenOffer()

    private val disclosure = object : LocationDisclosureStore {
        override fun accepted(): Boolean = true
        override fun markAccepted() = Unit
    }

    private fun model(store: ViewModelStore, clock: HandClock): HomeViewModel {
        coEvery { rider.profile() } returns FoodResult.Success(null)
        coEvery { rider.earnings() } returns FoodResult.Failure(FoodError.Network(IOException("offline")))
        coEvery { rider.offers() } returns FoodResult.Success(ServerTimed(listOf(offer), null))
        coEvery { rider.currentAssignment() } returns FoodResult.Success(null)
        coEvery { rider.assignments() } returns FoodResult.Success(emptyList())
        return HomeViewModel(rider, duty, disclosure, offerWatch(rider, duty, clock)).also { store.put("home", it) }
    }

    @Test
    fun `an offer card counts down from the server's seconds_to_expiry, not the phone's clock`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            // An hour slow, and no Date header: expires_at alone would read an hour and 20 s.
            val clock = HandClock(GOLDEN_EXPIRY.minusSeconds(3_600))
            val vm = model(store, clock)
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()

            val state = vm.state.value
            assertThat(state.offers).containsExactly(offer)
            assertThat(state.offerClock.windowOf(offer, clock.now)).isEqualTo(OfferWindow.Open(20, urgent = false))
        } finally { store.clear() }
    }

    @Test
    fun `go offline refused mid-delivery says finish it first`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            val vm = model(store, HandClock(GOLDEN_EXPIRY))
            backgroundScope.launch { vm.state.collect {} }
            runCurrent()

            duty.notice(DutyNotice.OFFLINE_REFUSED_ON_JOB)
            runCurrent()

            assertThat(vm.state.value.message?.text)
                .isEqualTo("You're still online: you're carrying a delivery. Finish it, then go offline.")
        } finally { store.clear() }
    }
}
