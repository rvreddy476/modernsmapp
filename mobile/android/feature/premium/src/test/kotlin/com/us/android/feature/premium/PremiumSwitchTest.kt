package com.us.android.feature.premium

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.premium.data.InvoicesDto
import com.us.android.feature.premium.data.PremiumRepository
import com.us.android.feature.premium.data.StatusDto
import com.us.android.feature.premium.entry.PremiumAvailability
import com.us.android.feature.premium.entry.PremiumEntry
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
 * Off, nothing can be bought and plans/checkout are never asked; a viewer who
 * already holds Premium or has an invoice still reaches their status and
 * invoices (15 Nov 2026). On, selling is drawn only after premium plans
 * answered 200 — a 503 PREMIUM_NOT_ENABLED or any failure keeps it hidden.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PremiumSwitchTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakePremiumApi()
    private val repository = PremiumRepository(api, ErrorMapper(testJson))

    private fun availability(enabled: Boolean) = PremiumAvailability(PremiumPurchaseSwitch(enabled), repository)

    @Test
    fun `switch off and nothing owned — no way in, and plans are never asked`() = runTest {
        api.meAnswers += { ApiEnvelope(data = StatusDto(active = false)) }
        api.invoiceAnswers += { ApiEnvelope(data = InvoicesDto()) }
        val entry = availability(enabled = false)
        entry.refresh()

        assertThat(entry.entry.value).isEqualTo(PremiumEntry.Hidden)
        assertThat(api.planReads).isEqualTo(0)
    }

    @Test
    fun `switch off with a live pass — the Settings row shows, Get Premium does not`() = runTest {
        val entry = availability(enabled = false)
        entry.refresh()

        assertThat(entry.entry.value).isEqualTo(PremiumEntry(settingsRow = true, canBuy = false))
        assertThat(api.planReads).isEqualTo(0)
    }

    @Test
    fun `switch off with only an old invoice — the Settings row shows`() = runTest {
        api.meAnswers += { ApiEnvelope(data = StatusDto(active = false)) }
        val entry = availability(enabled = false)
        entry.refresh()

        assertThat(entry.entry.value.settingsRow).isTrue()
        assertThat(entry.entry.value.canBuy).isFalse()
    }

    @Test
    fun `switch off and the server's switch off (503) — hidden`() = runTest {
        api.meAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        val entry = availability(enabled = false)
        entry.refresh()

        assertThat(entry.entry.value).isEqualTo(PremiumEntry.Hidden)
    }

    @Test
    fun `switch on and plans answered 200 — the ways in are drawn`() = runTest {
        val entry = availability(enabled = true)
        entry.refresh()

        assertThat(entry.entry.value).isEqualTo(PremiumEntry(settingsRow = true, canBuy = true))
        assertThat(api.requests).isEqualTo(1)
    }

    @Test
    fun `switch on but the server's switch is off (503) — hidden`() = runTest {
        api.planAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        val entry = availability(enabled = true)
        entry.refresh()

        assertThat(entry.entry.value).isEqualTo(PremiumEntry.Hidden)
    }

    @Test
    fun `a failed read hides what an earlier answer showed, and sign-out hides it too`() = runTest {
        val entry = availability(enabled = true)
        entry.refresh()
        assertThat(entry.entry.value).isEqualTo(PremiumEntry(settingsRow = true, canBuy = true))

        api.planAnswers += { offline() }
        entry.refresh()
        assertThat(entry.entry.value).isEqualTo(PremiumEntry.Hidden)

        entry.refresh()
        assertThat(entry.entry.value).isEqualTo(PremiumEntry(settingsRow = true, canBuy = true))
        entry.clear()
        assertThat(entry.entry.value).isEqualTo(PremiumEntry.Hidden)
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
    fun `switch off — status and invoices load, no plans are asked, and Buy does nothing`() = runTest {
        val vm = viewModel(enabled = false)
        advanceUntilIdle()

        val s = vm.state.value
        assertThat(s.off).isFalse()
        assertThat(s.loading).isFalse()
        assertThat(s.canBuy).isFalse()
        assertThat(s.plans).isEmpty()
        assertThat(s.status!!.active).isTrue()
        assertThat(s.invoices.single().number).isEqualTo("MP/26-27/000001")

        vm.refresh()
        vm.pay()
        advanceUntilIdle()
        assertThat(api.planReads).isEqualTo(0)
        assertThat(api.checkoutBodies).isEmpty()
        // A web buyer's invoice still downloads.
        assertThat(vm.startDownload("d4444444-4444-4444-8444-444444444444")).isTrue()
    }

    @Test
    fun `switch off and the server's PREMIUM_NOT_ENABLED — not available, no download`() = runTest {
        api.meAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        val vm = viewModel(enabled = false)
        advanceUntilIdle()

        assertThat(vm.state.value.off).isTrue()
        assertThat(vm.startDownload("d4444444-4444-4444-8444-444444444444")).isFalse()
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
