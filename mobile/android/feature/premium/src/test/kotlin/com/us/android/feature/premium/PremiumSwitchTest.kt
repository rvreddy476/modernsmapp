package com.us.android.feature.premium

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.premium.data.PremiumRepository
import com.us.android.feature.premium.entry.PremiumAvailability
import com.us.android.feature.premium.payment.PremiumPaymentOpener
import com.us.android.feature.premium.ui.InvoiceFileTarget
import com.us.android.feature.premium.ui.PremiumViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The founder's switch (13 Nov 2026): selling Premium on Android ships dark.
 * Off, no way in is drawn and nothing is asked of the server; on, a way in is
 * drawn only after premium plans answered 200 — a 503 PREMIUM_NOT_ENABLED or
 * any failure keeps it hidden.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PremiumSwitchTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakePremiumApi()
    private val repository = PremiumRepository(api, ErrorMapper(testJson))

    private fun availability(enabled: Boolean) = PremiumAvailability(PremiumPurchaseSwitch(enabled), repository)

    @Test
    fun `switch off — every way in stays hidden and the server is never asked`() = runTest {
        val entry = availability(enabled = false)
        entry.refresh()

        assertThat(entry.shown.value).isFalse()
        assertThat(api.requests).isEqualTo(0)
    }

    @Test
    fun `switch on and plans answered 200 — the ways in are drawn`() = runTest {
        val entry = availability(enabled = true)
        entry.refresh()

        assertThat(entry.shown.value).isTrue()
        assertThat(api.requests).isEqualTo(1)
    }

    @Test
    fun `switch on but the server's switch is off (503) — hidden`() = runTest {
        api.planAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        val entry = availability(enabled = true)
        entry.refresh()

        assertThat(entry.shown.value).isFalse()
    }

    @Test
    fun `a failed read hides what an earlier answer showed, and sign-out hides it too`() = runTest {
        val entry = availability(enabled = true)
        entry.refresh()
        assertThat(entry.shown.value).isTrue()

        api.planAnswers += { offline() }
        entry.refresh()
        assertThat(entry.shown.value).isFalse()

        entry.refresh()
        assertThat(entry.shown.value).isTrue()
        entry.clear()
        assertThat(entry.shown.value).isFalse()
    }

    private fun viewModel(enabled: Boolean): PremiumViewModel {
        val handoff = PaymentHandoff()
        val payments = coordinator()
        return PremiumViewModel(
            switch = PremiumPurchaseSwitch(enabled),
            repository = repository,
            opener = PremiumPaymentOpener(payments, handoff),
            files = InvoiceFileTarget { _: Uri -> null },
            handoff = handoff,
            payments = payments,
            savedState = SavedStateHandle(),
        )
    }

    @Test
    fun `switch off — the screen says not available, asks nothing, and Buy does nothing`() = runTest {
        val vm = viewModel(enabled = false)
        advanceUntilIdle()

        assertThat(vm.state.value.off).isTrue()
        assertThat(vm.state.value.loading).isFalse()
        vm.refresh()
        vm.pay()
        assertThat(vm.startDownload("d4444444-4444-4444-8444-444444444444")).isFalse()
        advanceUntilIdle()
        assertThat(api.requests).isEqualTo(0)
        assertThat(api.checkoutBodies).isEmpty()
    }

    @Test
    fun `switch on — the screen loads plans, status and invoices`() = runTest {
        val vm = viewModel(enabled = true)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.off).isFalse()
        assertThat(s.plans).hasSize(2)
        assertThat(s.status!!.active).isTrue()
        assertThat(s.invoices.single().number).isEqualTo("MP/26-27/000001")
    }

    @Test
    fun `the server's PREMIUM_NOT_ENABLED turns the screen to not available`() = runTest {
        api.planAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        val vm = viewModel(enabled = true)
        advanceUntilIdle()

        assertThat(vm.state.value.off).isTrue()
    }
}
