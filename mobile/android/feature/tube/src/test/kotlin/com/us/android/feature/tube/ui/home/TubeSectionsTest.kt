package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The home layout as a table: strip, carousel, shelf, chips, mosaic, and where the Reels panel cuts in. */
class TubeSectionsTest {

    @Test
    fun `a full page is channels, featured, continue, chips, four tiles, reels, then the rest`() {
        val sections = tubeSections(videoCount = 7, hasChannels = true, hasContinueWatching = true, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Channels,
            TubeSection.Featured(5),
            TubeSection.ContinueWatching,
            TubeSection.Chips,
            TubeSection.Video(0),
            TubeSection.Video(1),
            TubeSection.Video(2),
            TubeSection.Video(3),
            TubeSection.Reels,
            TubeSection.Video(4),
            TubeSection.Video(5),
            TubeSection.Video(6),
        ).inOrder()
    }

    @Test
    fun `the carousel pages through at most five, and fewer when there are fewer`() {
        assertThat(tubeSections(3, hasChannels = false, hasContinueWatching = false, hasReels = false))
            .contains(TubeSection.Featured(3))
        assertThat(tubeSections(9, hasChannels = false, hasContinueWatching = false, hasReels = false))
            .contains(TubeSection.Featured(5))
    }

    @Test
    fun `the reels panel comes after the last tile when there are fewer than four`() {
        val sections = tubeSections(videoCount = 2, hasChannels = false, hasContinueWatching = false, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Featured(2),
            TubeSection.Chips,
            TubeSection.Video(0),
            TubeSection.Video(1),
            TubeSection.Reels,
        ).inOrder()
    }

    @Test
    fun `an absent strip, shelf or panel is simply not there`() {
        val sections = tubeSections(videoCount = 1, hasChannels = false, hasContinueWatching = false, hasReels = false)

        assertThat(sections).containsExactly(TubeSection.Featured(1), TubeSection.Chips, TubeSection.Video(0)).inOrder()
    }

    @Test
    fun `the shelves still show with no ranked videos, and the chips always do`() {
        val sections = tubeSections(videoCount = 0, hasChannels = true, hasContinueWatching = true, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Channels,
            TubeSection.ContinueWatching,
            TubeSection.Chips,
            TubeSection.Reels,
        ).inOrder()
    }

    @Test
    fun `only a tile takes a single lane`() {
        val sections = tubeSections(videoCount = 6, hasChannels = true, hasContinueWatching = true, hasReels = true)

        sections.forEach { section ->
            assertThat(section.fullSpan).isEqualTo(section !is TubeSection.Video)
        }
    }

    @Test
    fun `every video index appears exactly once and every key is unique`() {
        val sections = tubeSections(videoCount = 11, hasChannels = true, hasContinueWatching = true, hasReels = true)

        val indexes = sections.mapNotNull { (it as? TubeSection.Video)?.index }
        assertThat(indexes).containsExactlyElementsIn(0 until 11).inOrder()
        assertThat(sections.map { it.key }).containsNoDuplicates()
    }
}
