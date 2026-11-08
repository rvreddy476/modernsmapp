package com.us.android.feature.rider.root

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.AuthRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.home.HomeViewModel
import com.us.android.feature.rider.home.LocationDisclosureStore
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.offers.GOLDEN_EXPIRY
import com.us.android.feature.rider.offers.HandClock
import com.us.android.feature.rider.offers.offerWatch
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import org.junit.Rule
import org.junit.Test

/** A rider carrying an order cannot sign out (food audit M2), from root or Home; an expired session still ends. */
class RiderSignOutTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val food = mockk<FoodRepository>().also {
        coEvery { it.capabilities() } returns FoodResult.Failure(FoodError.Network(null))
    }
    private val rider = mockk<RiderRepository>(relaxed = true)
    private val auth = mockk<AuthRepository>(relaxed = true)
    private val duty = RiderDuty()

    private fun viewModel() = RiderRootViewModel(food, rider, auth, duty, RiderDeepLinkBus())

    @Test
    fun `sign-out is refused while a delivery is active`() {
        duty.setOnJob(true)

        viewModel().signOut()

        coVerify(exactly = 0) { auth.logout() }
        coVerify(exactly = 0) { rider.setAvailability(any()) }
    }

    @Test
    fun `with no delivery the rider signs out`() {
        viewModel().signOut()

        coVerify(exactly = 1) { auth.logout() }
    }

    @Test
    fun `an expired session ends even mid-delivery`() {
        duty.setOnJob(true)

        viewModel().endExpiredSession()

        coVerify(exactly = 1) { auth.logout() }
    }

    private fun home(): HomeViewModel {
        coEvery { rider.profile() } returns FoodResult.Failure(FoodError.Network(null))
        coEvery { rider.earnings() } returns FoodResult.Failure(FoodError.Network(null))
        val watch = offerWatch(rider, duty, HandClock(GOLDEN_EXPIRY))
        return HomeViewModel(rider, duty, mockk<LocationDisclosureStore>(relaxed = true), watch)
    }

    @Test
    fun `Home refuses the sign-out tap mid-delivery and allows it after`() {
        val home = home()
        duty.setOnJob(true)
        assertThat(home.onSignOutTapped()).isFalse()

        duty.setOnJob(false)
        assertThat(home.onSignOutTapped()).isTrue()
    }
}
