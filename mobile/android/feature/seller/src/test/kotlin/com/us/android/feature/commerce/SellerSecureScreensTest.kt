package com.us.android.feature.commerce

import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.feature.commerce.seller.DocumentScreen
import com.us.android.feature.commerce.seller.DocumentViewModel
import com.us.android.feature.commerce.seller.PayoutScreen
import com.us.android.feature.commerce.seller.PayoutViewModel
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Audit L4: the payout screen (bank account number, IFSC) and the document
 * screen (an identity document's number) hold FLAG_SECURE while shown, and
 * let go of it when they leave.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SellerSecureScreensTest {

    @get:Rule
    val compose = createAndroidComposeRule<ComponentActivity>()

    private fun secure(): Boolean =
        compose.activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE != 0

    @Test
    fun `the payout screen is kept out of screenshots only while it is shown`() {
        val vm = PayoutViewModel(CommerceRepository(FakeCommerceApi()))
        var shown by mutableStateOf(true)
        compose.setContent {
            UsTheme { if (shown) PayoutScreen(onBack = {}, onSaved = {}, viewModel = vm) }
        }
        compose.waitForIdle()
        assertThat(secure()).isTrue()

        shown = false
        compose.waitForIdle()
        assertThat(secure()).isFalse()
    }

    @Test
    fun `the document screen is kept out of screenshots`() {
        val vm = DocumentViewModel(
            CommerceRepository(FakeCommerceApi()),
            mockk(relaxed = true),
            MediaSourceResolver { null },
            Dispatchers.Unconfined,
        )
        compose.setContent {
            UsTheme { DocumentScreen(onBack = {}, onAttached = {}, viewModel = vm) }
        }
        compose.waitForIdle()
        assertThat(secure()).isTrue()
    }
}
