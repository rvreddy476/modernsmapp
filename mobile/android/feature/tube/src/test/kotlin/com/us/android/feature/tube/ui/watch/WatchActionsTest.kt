package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedPostControls
import org.junit.Test

/** The action row honours the author's switches by hiding, and labels with counts. */
class WatchActionsTest {

    @Test
    fun `all five in order with counts as labels`() {
        val actions = watchActions(FeedPostControls(), likes = 8_800, comments = 12, saved = false)

        assertThat(actions.map { it.kind }).containsExactly(
            WatchActionKind.LIKE,
            WatchActionKind.COMMENT,
            WatchActionKind.SHARE,
            WatchActionKind.SAVE,
            WatchActionKind.MORE,
        ).inOrder()
        assertThat(actions.map { it.label }).containsExactly("8.8K", "12", "Share", "Save", "More").inOrder()
    }

    @Test
    fun `nothing to count falls back to the noun and saved says so`() {
        val actions = watchActions(FeedPostControls(), likes = 0, comments = 0, saved = true)
        assertThat(actions.map { it.label }).containsExactly("Like", "Comment", "Share", "Saved", "More").inOrder()
    }

    @Test
    fun `no comments hides Comment and hide_share hides Share`() {
        val kinds = watchActions(FeedPostControls(noComments = true, hideShare = true), 0, 0, false).map { it.kind }
        assertThat(kinds).containsExactly(WatchActionKind.LIKE, WatchActionKind.SAVE, WatchActionKind.MORE).inOrder()
    }
}
