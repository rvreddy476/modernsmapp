package com.us.android.feature.rider.offers

import com.google.common.truth.Truth.assertThat
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
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.time.Duration

/** One process-wide offer feed that runs only while held (food audit H1, L5) and reads the server's clock (L1). */
@OptIn(ExperimentalCoroutinesApi::class)
class RiderOfferWatchTest {
    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val rider = mockk<RiderRepository>()
    private val duty = RiderDuty()
    private val clock = HandClock(GOLDEN_EXPIRY.minusSeconds(40))
    private val offer = goldenOffer()

    private fun watch(): RiderOfferWatch {
        val listed = ServerTimed(listOf(offer), GOLDEN_EXPIRY.minusSeconds(10))
        coEvery { rider.offers() } returns FoodResult.Success(listed)
        coEvery { rider.currentAssignment() } returns FoodResult.Success(null)
        return offerWatch(rider, duty, clock)
    }

    @Test
    fun `nothing is read until someone holds the feed, and nothing after the last lets go`() = runTest(dispatcher) {
        val watch = watch()
        advanceTimeBy(60_000)
        runCurrent()
        coVerify(exactly = 0) { rider.offers() }

        val service = watch.hold()
        val home = watch.hold()
        runCurrent()
        coVerify(exactly = 1) { rider.offers() }
        assertThat(watch.state.value.offers).containsExactly(offer)

        home.release()
        home.release()
        advanceTimeBy(15_001)
        runCurrent()
        coVerify(exactly = 2) { rider.offers() }

        service.release()
        advanceTimeBy(120_000)
        runCurrent()
        coVerify(exactly = 2) { rider.offers() }
        assertThat(watch.state.value.offers).isEmpty()
        assertThat(watch.state.value.loaded).isFalse()
    }

    @Test
    fun `the countdown clock is the server's, read from the response's Date header`() = runTest(dispatcher) {
        val watch = watch()
        val hold = watch.hold()
        runCurrent()

        // The device runs 30 s fast: the server said 06:30:15 when the device read 06:29:45.
        assertThat(watch.state.value.clock.offset).isEqualTo(Duration.ofSeconds(30))
        val window = OfferCountdown.of(offer.expiresAt).at(watch.state.value.clock.serverNow(clock.now()))
        assertThat(window).isEqualTo(OfferWindow.Open(10, urgent = true))
        hold.release()
    }

    @Test
    fun `a job the server holds puts the cadence on the job, wherever the rider is in the app`() = runTest(dispatcher) {
        val watch = watch()
        coEvery { rider.currentAssignment() } returns FoodResult.Success(routeAssignment())

        val hold = watch.hold()
        runCurrent()

        assertThat(duty.onJob.value).isTrue()
        hold.release()
    }
}
