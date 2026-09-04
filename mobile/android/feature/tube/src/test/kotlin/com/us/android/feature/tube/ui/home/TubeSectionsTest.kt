package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The home layout as a table: where the hero, the two shelves and the head fall. */
class TubeSectionsTest {

    @Test
    fun `a full page is hero, continue, one card, shorts, then the rest`() {
        val sections = tubeSections(videoCount = 5, hasHead = false, hasContinueWatching = true, hasShorts = true)

        assertThat(sections).containsExactly(
            TubeSection.Hero(0),
            TubeSection.ContinueWatching,
            TubeSection.Video(1),
            TubeSection.Shorts,
            TubeSection.Video(2),
            TubeSection.Video(3),
            TubeSection.Video(4),
        ).inOrder()
    }

    @Test
    fun `an absent shelf is simply not there`() {
        val sections = tubeSections(videoCount = 3, hasHead = false, hasContinueWatching = false, hasShorts = false)

        assertThat(sections).containsExactly(TubeSection.Hero(0), TubeSection.Video(1), TubeSection.Video(2)).inOrder()
    }

    @Test
    fun `the head sits above the hero`() {
        val sections = tubeSections(videoCount = 1, hasHead = true, hasContinueWatching = false, hasShorts = true)

        assertThat(sections).containsExactly(TubeSection.Head, TubeSection.Hero(0), TubeSection.Shorts).inOrder()
    }

    @Test
    fun `shelves still show with no ranked videos`() {
        val sections = tubeSections(videoCount = 0, hasHead = false, hasContinueWatching = true, hasShorts = true)

        assertThat(sections).containsExactly(TubeSection.ContinueWatching, TubeSection.Shorts).inOrder()
    }

    @Test
    fun `nothing at all is an empty page`() {
        val sections = tubeSections(videoCount = 0, hasHead = false, hasContinueWatching = false, hasShorts = false)

        assertThat(sections).isEmpty()
    }

    @Test
    fun `every video index appears exactly once`() {
        val sections = tubeSections(videoCount = 7, hasHead = true, hasContinueWatching = true, hasShorts = true)

        val indexes = sections.mapNotNull {
            when (it) {
                is TubeSection.Hero -> it.index
                is TubeSection.Video -> it.index
                else -> null
            }
        }
        assertThat(indexes).containsExactlyElementsIn(0 until 7).inOrder()
        assertThat(sections.map { it.key }).containsNoDuplicates()
    }
}
