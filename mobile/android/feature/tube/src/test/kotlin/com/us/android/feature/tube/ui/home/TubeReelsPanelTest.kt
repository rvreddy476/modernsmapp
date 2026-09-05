package com.us.android.feature.tube.ui.home

import androidx.compose.ui.unit.dp
import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The Reels panel's split (founder, 2026-09-05): two cards to a screen width, the gap between them. */
class TubeReelsPanelTest {

    @Test
    fun `two cards fill the panel's inner width with one gap between them`() {
        val card = reelCardWidth(panelWidth = 360.dp, inset = 14.dp, gap = 8.dp)

        // 360 − 2·14 − 8 = 324, halved.
        assertThat(card).isEqualTo(162.dp)
        assertThat(card * 2 + 8.dp + 14.dp * 2).isEqualTo(360.dp)
    }

    @Test
    fun `a wider screen gets wider cards, still two across`() {
        val narrow = reelCardWidth(panelWidth = 360.dp, inset = 14.dp, gap = 8.dp)
        val wide = reelCardWidth(panelWidth = 600.dp, inset = 14.dp, gap = 8.dp)

        assertThat(wide).isGreaterThan(narrow)
        assertThat(wide * 2 + 8.dp + 14.dp * 2).isEqualTo(600.dp)
    }

    /** The strip only exists for followed channels: no channels, no strip, no "Create" bubble. */
    @Test
    fun `the channels strip is absent from the page when there is nothing to put in it`() {
        val without = tubeSections(videoCount = 3, hasChannels = false, hasContinueWatching = false, hasReels = true)
        val with = tubeSections(videoCount = 3, hasChannels = true, hasContinueWatching = false, hasReels = true)

        assertThat(without).doesNotContain(TubeSection.Channels)
        assertThat(with.first()).isEqualTo(TubeSection.Channels)
    }
}
