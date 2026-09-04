package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Tube's bar: the slot order, what each slot does, and which slot a page lights. */
class TubeBarTest {

    @Test
    fun `the slots are Home, Shorts, Subscriptions, You — the plus sits between the pairs`() {
        assertThat(TubeBarItem.entries.map { it.label })
            .containsExactly("Home", "Shorts", "Subscriptions", "You")
            .inOrder()
    }

    @Test
    fun `Home, Subscriptions and You are Tube pages`() {
        assertThat(TubeBarItem.HOME.action()).isEqualTo(TubeBarAction.OpenTab(TubeTab.HOME))
        assertThat(TubeBarItem.SUBSCRIPTIONS.action()).isEqualTo(TubeBarAction.OpenTab(TubeTab.SUBSCRIPTIONS))
        assertThat(TubeBarItem.YOU.action()).isEqualTo(TubeBarAction.OpenTab(TubeTab.YOU))
    }

    @Test
    fun `Shorts leaves for the app's Reels tab`() {
        assertThat(TubeBarItem.SHORTS.action()).isEqualTo(TubeBarAction.OpenReels)
    }

    @Test
    fun `each page lights its own slot and Shorts is never lit`() {
        assertThat(TubeTab.HOME.barIndex()).isEqualTo(TubeBarItem.HOME.ordinal)
        assertThat(TubeTab.SUBSCRIPTIONS.barIndex()).isEqualTo(TubeBarItem.SUBSCRIPTIONS.ordinal)
        assertThat(TubeTab.YOU.barIndex()).isEqualTo(TubeBarItem.YOU.ordinal)
        assertThat(TubeTab.entries.map { it.barIndex() }).doesNotContain(TubeBarItem.SHORTS.ordinal)
    }

    @Test
    fun `no slot has a blank label or description`() {
        TubeBarItem.entries.forEach {
            assertThat(it.label).isNotEmpty()
            assertThat(it.contentDescription).isNotEmpty()
        }
    }
}
