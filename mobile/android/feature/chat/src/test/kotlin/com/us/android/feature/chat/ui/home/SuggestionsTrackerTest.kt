package com.us.android.feature.chat.ui.home

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Impressions post once per shown batch; a dismissed id stays out for the session. */
class SuggestionsTrackerTest {

    @Test
    fun `the first sight of a batch posts, the same batch again does not`() {
        val tracker = SuggestionsTracker()
        assertThat(tracker.shouldPostImpression("friend", listOf("a", "b"))).isTrue()
        assertThat(tracker.shouldPostImpression("friend", listOf("a", "b"))).isFalse()
    }

    @Test
    fun `a different batch or a different kind posts again`() {
        val tracker = SuggestionsTracker()
        tracker.shouldPostImpression("friend", listOf("a", "b"))
        assertThat(tracker.shouldPostImpression("friend", listOf("a", "b", "c"))).isTrue()
        assertThat(tracker.shouldPostImpression("community", listOf("a", "b"))).isTrue()
    }

    @Test
    fun `an empty batch never posts`() {
        assertThat(SuggestionsTracker().shouldPostImpression("friend", emptyList())).isFalse()
    }

    @Test
    fun `a dismissed id leaves the visible list and stays out on the next refresh`() {
        val tracker = SuggestionsTracker()
        tracker.dismiss("b")
        assertThat(tracker.visible(listOf("a", "b", "c")) { it }).containsExactly("a", "c").inOrder()
        assertThat(tracker.visible(listOf("b", "d")) { it }).containsExactly("d")
        assertThat(tracker.isDismissed("b")).isTrue()
        assertThat(tracker.isDismissed("a")).isFalse()
    }
}
