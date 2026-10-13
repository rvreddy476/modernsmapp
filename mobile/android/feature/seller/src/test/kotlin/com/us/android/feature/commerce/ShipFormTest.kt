package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.commerce.seller.MAX_TRACKING_CHARS
import com.us.android.feature.commerce.seller.MIN_TRACKING_CHARS
import com.us.android.feature.commerce.seller.ShipForm
import com.us.android.feature.commerce.seller.trackingNumberProblem
import org.junit.Test

/**
 * What the ship sheet lets leave the device.
 *
 * The tracking number is the one thing on the sheet the buyer acts on; a
 * blank or a pasted label fragment reaching the server becomes a "Track"
 * link that goes nowhere. The rule is shape only, on purpose: the couriers'
 * formats vary, and a rule that knew them would be wrong for the next one.
 */
class ShipFormTest {

    @Test
    fun `a blank tracking number is refused with a prompt, not a complaint`() {
        assertThat(trackingNumberProblem("")).isEqualTo("Enter the tracking number")
        assertThat(trackingNumberProblem("   ")).isEqualTo("Enter the tracking number")
    }

    @Test
    fun `too short and too long are said as such`() {
        assertThat(trackingNumberProblem("A".repeat(MIN_TRACKING_CHARS - 1))).contains("short")
        assertThat(trackingNumberProblem("A".repeat(MAX_TRACKING_CHARS + 1))).contains("long")
        assertThat(trackingNumberProblem("A".repeat(MIN_TRACKING_CHARS))).isNull()
        assertThat(trackingNumberProblem("A".repeat(MAX_TRACKING_CHARS))).isNull()
    }

    @Test
    fun `letters digits and hyphens pass, anything else does not`() {
        assertThat(trackingNumberProblem("DL-1234567890")).isNull()
        assertThat(trackingNumberProblem("EK123456789IN")).isNull()
        // A space inside is a paste that picked up the label's text.
        assertThat(trackingNumberProblem("DL 1234567")).isEqualTo("Letters, digits and hyphens only")
        assertThat(trackingNumberProblem("DL#1234567")).isEqualTo("Letters, digits and hyphens only")
    }

    @Test
    fun `surrounding whitespace is forgiven`() {
        assertThat(trackingNumberProblem("  DL1234567  ")).isNull()
    }

    @Test
    fun `the form needs both a courier and a valid number`() {
        assertThat(ShipForm(courier = "", trackingNumber = "DL1234567").isValid).isFalse()
        assertThat(ShipForm(courier = "Delhivery", trackingNumber = "").isValid).isFalse()
        assertThat(ShipForm(courier = "Delhivery", trackingNumber = "DL1234567").isValid).isTrue()
        assertThat(ShipForm(courier = "  ", trackingNumber = "DL1234567").courierProblem).isNotNull()
    }
}
