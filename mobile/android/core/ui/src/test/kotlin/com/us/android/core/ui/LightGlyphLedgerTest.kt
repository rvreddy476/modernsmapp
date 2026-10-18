package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class LightGlyphLedgerTest {

    @Test
    fun `one hold draws light glyphs and puts the original back`() {
        val ledger = LightGlyphLedger()
        assertThat(ledger.acquire(current = true)).isFalse()
        assertThat(ledger.release()).isTrue()
    }

    /** The camera handing to the studio: the second hold starts before the first ends. */
    @Test
    fun `overlapping holds restore the first holder's original only when the last leaves`() {
        val ledger = LightGlyphLedger()
        ledger.acquire(current = true) // camera, on a light-theme device
        ledger.acquire(current = false) // studio, while the camera still holds
        assertThat(ledger.release()).isNull() // camera leaves: the studio still holds
        assertThat(ledger.release()).isTrue() // studio leaves: dark glyphs for the light screen
    }

    @Test
    fun `a release with no hold changes nothing`() {
        assertThat(LightGlyphLedger().release()).isNull()
    }
}
