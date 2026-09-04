package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.VideoFeedQuery
import org.junit.Test

/** The chip rail: its order, and the request each pill stands for. */
class TubeChipsTest {

    private val categories = listOf(FeedCategory("comedy", "Comedy"), FeedCategory("music", "Music"))

    @Test
    fun `All and Following lead, then the taxonomy in the server's order`() {
        val chips = tubeChips(categories)

        assertThat(chips.map { it.label }).containsExactly("All", "Following", "Comedy", "Music").inOrder()
        assertThat(chips.map { it.key }).containsExactly("all", "following", "category:comedy", "category:music")
    }

    @Test
    fun `no taxonomy still gives the two fixed chips`() {
        assertThat(tubeChips(emptyList())).containsExactly(TubeChip.All, TubeChip.Following).inOrder()
    }

    @Test
    fun `All is the plain videos surface`() {
        assertThat(TubeChip.All.toQuery()).isEqualTo(VideoFeedQuery.All)
    }

    @Test
    fun `Following is the watch surface narrowed to followed authors`() {
        assertThat(TubeChip.Following.toQuery()).isEqualTo(VideoFeedQuery.Following)
    }

    @Test
    fun `a category chip carries its id`() {
        assertThat(TubeChip.Category("comedy", "Comedy").toQuery()).isEqualTo(VideoFeedQuery.Category("comedy"))
    }

    @Test
    fun `only Following is not a suggestion`() {
        assertThat(TubeChip.Following.isSuggested()).isFalse()
        assertThat(TubeChip.All.isSuggested()).isTrue()
        assertThat(TubeChip.Category("music", "Music").isSuggested()).isTrue()
    }

    @Test
    fun `a stored key resolves to its chip and an unknown key to All`() {
        val chips = tubeChips(categories)

        assertThat(chips.chipFor("category:music")).isEqualTo(TubeChip.Category("music", "Music"))
        assertThat(chips.chipFor("following")).isEqualTo(TubeChip.Following)
        assertThat(chips.chipFor("category:gone")).isEqualTo(TubeChip.All)
        assertThat(chips.chipFor(null)).isEqualTo(TubeChip.All)
    }
}
