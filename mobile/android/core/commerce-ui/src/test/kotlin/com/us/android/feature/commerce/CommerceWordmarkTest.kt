package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.commerce.ui.CommerceBrand
import com.us.android.feature.commerce.ui.wordmarkParts
import org.junit.Test

/**
 * The wordmark.
 *
 * The founder said "M shop", then corrected it to "M store", and the buyer app
 * shipped as MStore. The names live in exactly one place so a further rename
 * is one line, and the split that stylises the capital M is a pure function so
 * the rendering rule is checkable without a screenshot.
 */
class CommerceWordmarkTest {

    @Test
    fun `the two mini-apps are named once`() {
        assertThat(CommerceBrand.Buyer).isEqualTo("MStore")
        assertThat(CommerceBrand.Seller).isEqualTo("MSeller")
    }

    @Test
    fun `the mark splits after the stylised initial`() {
        assertThat(wordmarkParts(CommerceBrand.Buyer)).isEqualTo("M" to "Store")
        assertThat(wordmarkParts(CommerceBrand.Seller)).isEqualTo("M" to "Seller")
    }

    /**
     * A rename must not need this function changed. Whatever the name becomes,
     * the first character is the mark and the rest is the tail.
     */
    @Test
    fun `any name splits, including the degenerate ones`() {
        assertThat(wordmarkParts("MShop")).isEqualTo("M" to "Shop")
        assertThat(wordmarkParts("M")).isEqualTo("M" to "")

        // An empty name is drawn as nothing rather than throwing: a wordmark
        // is not worth an exception on a screen that is otherwise fine.
        assertThat(wordmarkParts("")).isEqualTo("" to "")
    }
}
