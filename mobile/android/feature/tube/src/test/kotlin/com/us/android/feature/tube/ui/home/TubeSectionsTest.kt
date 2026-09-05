package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The home layout as a table: strip, chips, two lead videos, the shelf, the panel, then the rest. */
class TubeSectionsTest {

    @Test
    fun `a full page is channels, chips, two videos, continue, reels, then the rest`() {
        val sections = tubeSections(videoCount = 7, hasChannels = true, hasContinueWatching = true, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Channels,
            TubeSection.Chips,
            TubeSection.Video(0),
            TubeSection.Video(1),
            TubeSection.ContinueWatching,
            TubeSection.Reels,
            TubeSection.Video(2),
            TubeSection.Video(3),
            TubeSection.Video(4),
            TubeSection.Video(5),
            TubeSection.Video(6),
        ).inOrder()
    }

    @Test
    fun `the shelf and the panel follow the last video when there are fewer than two`() {
        val sections = tubeSections(videoCount = 1, hasChannels = false, hasContinueWatching = true, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Chips,
            TubeSection.Video(0),
            TubeSection.ContinueWatching,
            TubeSection.Reels,
        ).inOrder()
    }

    @Test
    fun `an absent strip, shelf or panel is simply not there`() {
        val sections = tubeSections(videoCount = 3, hasChannels = false, hasContinueWatching = false, hasReels = false)

        assertThat(sections).containsExactly(
            TubeSection.Chips,
            TubeSection.Video(0),
            TubeSection.Video(1),
            TubeSection.Video(2),
        ).inOrder()
    }

    @Test
    fun `the shelves still show with no ranked videos, and the chips always do`() {
        val sections = tubeSections(videoCount = 0, hasChannels = true, hasContinueWatching = true, hasReels = true)

        assertThat(sections).containsExactly(
            TubeSection.Channels,
            TubeSection.Chips,
            TubeSection.ContinueWatching,
            TubeSection.Reels,
        ).inOrder()
    }

    @Test
    fun `the chips come before the first video whatever else is on the page`() {
        listOf(0, 1, 2, 9).forEach { count ->
            val sections = tubeSections(count, hasChannels = true, hasContinueWatching = true, hasReels = true)
            val chips = sections.indexOf(TubeSection.Chips)
            val firstVideo = sections.indexOfFirst { it is TubeSection.Video }
            assertThat(chips).isAtMost(1)
            if (firstVideo >= 0) assertThat(chips).isLessThan(firstVideo)
        }
    }

    @Test
    fun `nothing on the page scrolls sideways through videos`() {
        val sections = tubeSections(videoCount = 9, hasChannels = true, hasContinueWatching = true, hasReels = true)

        assertThat(sections.filterIsInstance<TubeSection.Video>()).hasSize(9)
        assertThat(sections.map { it.key }).doesNotContain("featured")
    }

    @Test
    fun `every video index appears exactly once and every key is unique`() {
        val sections = tubeSections(videoCount = 11, hasChannels = true, hasContinueWatching = true, hasReels = true)

        val indexes = sections.mapNotNull { (it as? TubeSection.Video)?.index }
        assertThat(indexes).containsExactlyElementsIn(0 until 11).inOrder()
        assertThat(sections.map { it.key }).containsNoDuplicates()
    }
}
