package com.us.android.feature.tube.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.VideoFeedQuery
import org.junit.Test

/** The chip rail: its order, and the request each pill stands for. */
class TubeChipsTest {

    private val categories = listOf(FeedCategory("comedy", "Comedy"), FeedCategory("music", "Music"))

    @Test
    fun `All and Subscriptions lead, then the taxonomy in the server's order`() {
        val chips = tubeChips(categories)

        assertThat(chips.map { it.label }).containsExactly("All", "Subscriptions", "Comedy", "Music").inOrder()
        assertThat(chips.map { it.key }).containsExactly("all", "subscriptions", "category:comedy", "category:music")
    }

    @Test
    fun `no taxonomy still gives the two fixed chips`() {
        assertThat(tubeChips(emptyList())).containsExactly(TubeChip.All, TubeChip.Subscriptions).inOrder()
    }

    @Test
    fun `All is the plain videos surface`() {
        assertThat(TubeChip.All.toQuery()).isEqualTo(VideoFeedQuery.All)
    }

    /** Subscribed, not Following: a followed author never subscribed to is not a channel the viewer chose. */
    @Test
    fun `Subscriptions is the watch surface narrowed to subscribed channels`() {
        assertThat(TubeChip.Subscriptions.toQuery()).isEqualTo(VideoFeedQuery.Subscribed)
    }

    @Test
    fun `no chip on the rail asks for the Following feed`() {
        assertThat(tubeChips(categories).map { it.toQuery() }).doesNotContain(VideoFeedQuery.Following)
    }

    @Test
    fun `a category chip carries its id`() {
        assertThat(TubeChip.Category("comedy", "Comedy").toQuery()).isEqualTo(VideoFeedQuery.Category("comedy"))
    }

    @Test
    fun `only Subscriptions is not a suggestion`() {
        assertThat(TubeChip.Subscriptions.isSuggested()).isFalse()
        assertThat(TubeChip.All.isSuggested()).isTrue()
        assertThat(TubeChip.Category("music", "Music").isSuggested()).isTrue()
    }

    @Test
    fun `a stored key resolves to its chip and an unknown key to All`() {
        val chips = tubeChips(categories)

        assertThat(chips.chipFor("category:music")).isEqualTo(TubeChip.Category("music", "Music"))
        assertThat(chips.chipFor("subscriptions")).isEqualTo(TubeChip.Subscriptions)
        assertThat(chips.chipFor("category:gone")).isEqualTo(TubeChip.All)
        assertThat(chips.chipFor(null)).isEqualTo(TubeChip.All)
    }

    /** The key a pre-rename build saved. It must land somewhere sane, not on a chip that no longer exists. */
    @Test
    fun `the old following key falls back to All`() {
        assertThat(tubeChips(categories).chipFor("following")).isEqualTo(TubeChip.All)
    }
}
