package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.echo.data.Eligibility
import com.us.android.feature.echo.data.EligibilityReason
import com.us.android.feature.echo.ui.ComposeGate
import com.us.android.feature.echo.ui.postGate
import com.us.android.feature.echo.ui.showGetPremium
import org.junit.Test

/**
 * Echo's premium sheet (2026-11-13): "Get Premium" only for the Premium
 * reason AND only while `:app` draws a way in (its purchase switch on and
 * premium plans answered 200). Otherwise the sheet keeps OK alone.
 */
class EchoGetPremiumTest {

    private fun gate(reason: EligibilityReason) =
        postGate(Eligibility(canPost = false, canReply = true, reason = reason)) as ComposeGate.Blocked

    @Test
    fun `the premium sheet offers Get Premium only while the shell draws a way in`() {
        val premium = gate(EligibilityReason.PREMIUM_REQUIRED)
        assertThat(premium.premiumRequired).isTrue()
        assertThat(showGetPremium(premium, entryShown = true)).isTrue()
        assertThat(showGetPremium(premium, entryShown = false)).isFalse()
    }

    @Test
    fun `no other reason ever offers a purchase`() {
        val others = listOf(EligibilityReason.ACCOUNT_TOO_NEW, EligibilityReason.SUSPENDED, EligibilityReason.NONE)
        others.forEach { reason ->
            val blocked = gate(reason)
            assertThat(blocked.premiumRequired).isFalse()
            assertThat(showGetPremium(blocked, entryShown = true)).isFalse()
        }
    }
}
