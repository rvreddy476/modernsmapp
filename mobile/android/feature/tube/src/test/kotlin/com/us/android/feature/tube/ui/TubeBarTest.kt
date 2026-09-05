package com.us.android.feature.tube.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Tube's bar: the slot order, what each slot does, and which slot a page lights. */
class TubeBarTest {

    @Test
    fun `the slots are Home, Reels, Explore, You - the plus sits between the pairs`() {
        assertThat(TubeBarItem.entries.map { it.label })
            .containsExactly("Home", "Reels", "Explore", "You")
            .inOrder()
    }

    @Test
    fun `nothing in Tube is called Shorts any more`() {
        TubeBarItem.entries.forEach {
            assertThat(it.label).doesNotContain("Shorts")
            assertThat(it.contentDescription).doesNotContain("Shorts")
        }
    }

    @Test
    fun `Home and You are Tube pages`() {
        assertThat(TubeBarItem.HOME.action()).isEqualTo(TubeBarAction.OpenTab(TubeTab.HOME))
        assertThat(TubeBarItem.YOU.action()).isEqualTo(TubeBarAction.OpenTab(TubeTab.YOU))
    }

    @Test
    fun `Reels and Explore leave Tube for the app`() {
        assertThat(TubeBarItem.REELS.action()).isEqualTo(TubeBarAction.OpenReels)
        assertThat(TubeBarItem.EXPLORE.action()).isEqualTo(TubeBarAction.OpenExplore)
    }

    @Test
    fun `Subscriptions is not on the bar - it lives under You`() {
        assertThat(TubeBarItem.entries.map { it.label }).doesNotContain("Subscriptions")
        assertThat(TubeTab.SUBSCRIPTIONS.barIndex()).isEqualTo(TubeBarItem.YOU.ordinal)
    }

    @Test
    fun `each page lights its slot and Reels and Explore are never lit`() {
        assertThat(TubeTab.HOME.barIndex()).isEqualTo(TubeBarItem.HOME.ordinal)
        assertThat(TubeTab.YOU.barIndex()).isEqualTo(TubeBarItem.YOU.ordinal)
        val lit = TubeTab.entries.map { it.barIndex() }
        assertThat(lit).doesNotContain(TubeBarItem.REELS.ordinal)
        assertThat(lit).doesNotContain(TubeBarItem.EXPLORE.ordinal)
    }

    @Test
    fun `no slot has a blank label or description`() {
        TubeBarItem.entries.forEach {
            assertThat(it.label).isNotEmpty()
            assertThat(it.contentDescription).isNotEmpty()
        }
    }
}
