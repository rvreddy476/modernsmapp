package com.us.android.feature.kitchen.ui

import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.compliance.ComplianceScreen
import com.us.android.feature.kitchen.compliance.ComplianceViewModel
import com.us.android.feature.kitchen.fssai.FssaiScreen
import com.us.android.feature.kitchen.fssai.FssaiViewModel
import com.us.android.feature.kitchen.payout.PayoutScreen
import com.us.android.feature.kitchen.payout.PayoutViewModel
import com.us.android.feature.kitchen.queue.KitchenClock
import io.mockk.coEvery
import io.mockk.mockk
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Food audit L4: the bank account, PAN / GSTIN and FSSAI licence screens hold
 * FLAG_SECURE while shown (no screenshots, recordings or recents thumbnail),
 * and let go of it when they leave.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class KitchenSecureScreensTest {

    @get:Rule
    val compose = createAndroidComposeRule<ComponentActivity>()

    private val offline = FoodResult.Failure(FoodError.Network(null))
    private val food = mockk<FoodRepository>(relaxed = true).also { food ->
        coEvery { food.getRestaurantPayoutAccount(any()) } returns offline
    }
    private val args = SavedStateHandle(mapOf("restaurantId" to "rst_1"))

    private fun secure(): Boolean =
        compose.activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE != 0

    @Test
    fun `the bank account screen is kept out of screenshots only while it is shown`() {
        val vm = PayoutViewModel(args, food)
        var shown by mutableStateOf(true)
        compose.setContent { UsTheme { if (shown) PayoutScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()

        shown = false
        compose.waitForIdle()
        assertThat(secure()).isFalse()
    }

    @Test
    fun `the PAN and GSTIN screen is kept out of screenshots`() {
        val vm = ComplianceViewModel(args, food, KitchenClock.System)
        compose.setContent { UsTheme { ComplianceScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()
    }

    @Test
    fun `the FSSAI licence screen is kept out of screenshots`() {
        val vm = FssaiViewModel(args, food, mockk(relaxed = true), KitchenClock.System)
        compose.setContent { UsTheme { FssaiScreen(onBack = {}, viewModel = vm) } }
        compose.waitForIdle()
        assertThat(secure()).isTrue()
    }
}
