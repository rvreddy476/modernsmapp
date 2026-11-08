package com.us.android.feature.rider.ui

import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.documents.DocumentsScreen
import com.us.android.feature.rider.documents.DocumentsViewModel
import com.us.android.feature.rider.payout.PayoutScreen
import com.us.android.feature.rider.payout.PayoutViewModel
import com.us.android.feature.rider.selfie.SelfieScreen
import com.us.android.feature.rider.selfie.SelfieViewModel
import io.mockk.coEvery
import io.mockk.mockk
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Food audit L4: the bank account, driving documents and selfie screens hold
 * FLAG_SECURE while shown (no screenshots, recordings or recents thumbnail),
 * and let go of it when they leave.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class RiderSecureScreensTest {

    @get:Rule
    val compose = createAndroidComposeRule<ComponentActivity>()

    private val offline = FoodResult.Failure(FoodError.Network(null))
    private val food = mockk<FoodRepository>(relaxed = true).also { food ->
        coEvery { food.getDeliveryPayoutAccount() } returns offline
    }
    private val rider = mockk<RiderRepository>(relaxed = true).also { rider ->
        coEvery { rider.kycStatus() } returns offline
    }

    private fun secure(): Boolean =
        compose.activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE != 0

    @Test
    fun `the bank account screen is kept out of screenshots only while it is shown`() {
        val vm = PayoutViewModel(food)
        var shown by mutableStateOf(true)
        compose.setContent { UsTheme { if (shown) PayoutScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()

        shown = false
        compose.waitForIdle()
        assertThat(secure()).isFalse()
    }

    @Test
    fun `the driving documents screen is kept out of screenshots`() {
        val vm = DocumentsViewModel(rider, mockk(relaxed = true))
        compose.setContent { UsTheme { DocumentsScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()
    }

    @Test
    fun `the selfie screen is kept out of screenshots`() {
        val vm = SelfieViewModel(rider, mockk(relaxed = true))
        compose.setContent { UsTheme { SelfieScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()
    }
}
