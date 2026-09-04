package com.us.android.feature.feed.ui.reels

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedPostControls
import org.junit.Test

/**
 * The rail's labels, the author line and the playhead line — the rules the
 * founder set from YouTube Shorts (2026-09-04), pinned so the screen cannot
 * drift from them:
 *
 *  - a count is the label where there is one, the control's name where
 *    there is not — "8.8K" over "Like", never "0";
 *  - `hide_share` removes Share, `no_comments` removes Comment, and nothing
 *    the author sets touches Like, Save or More;
 *  - "@handle" only where there is a handle; a display name gets no "@";
 *  - the playhead fraction is 0..1 whatever the player reports.
 */
class ReelRailTest {

    private fun rail(
        controls: FeedPostControls = FeedPostControls(),
        likes: Int = 0,
        comments: Int = 0,
        saved: Boolean = false,
        offersMore: Boolean = true,
    ) = railControls(controls, likes, comments, saved, offersMore)

    @Test
    fun `the rail is like, comment, share, save, more with their names when there is nothing to count`() {
        assertThat(rail().map { it.kind to it.label }).containsExactly(
            RailKind.LIKE to "Like",
            RailKind.COMMENT to "Comment",
            RailKind.SHARE to "Share",
            RailKind.SAVE to "Save",
            RailKind.MORE to "More",
        ).inOrder()
    }

    @Test
    fun `like and comment carry their compact counts`() {
        val labels = rail(likes = 8_800, comments = 1_234_567).associate { it.kind to it.label }

        assertThat(labels[RailKind.LIKE]).isEqualTo("8.8K")
        assertThat(labels[RailKind.COMMENT]).isEqualTo("1.2M")
    }

    @Test
    fun `a count label falls back to the noun at zero and below`() {
        assertThat(railCountLabel(0, "Like")).isEqualTo("Like")
        assertThat(railCountLabel(-3, "Comment")).isEqualTo("Comment")
        assertThat(railCountLabel(1, "Like")).isEqualTo("1")
        assertThat(railCountLabel(999, "Like")).isEqualTo("999")
        assertThat(railCountLabel(1_000, "Like")).isEqualTo("1K")
    }

    @Test
    fun `save says whether it is done`() {
        assertThat(rail(saved = false).first { it.kind == RailKind.SAVE }.label).isEqualTo("Save")
        assertThat(rail(saved = true).first { it.kind == RailKind.SAVE }.label).isEqualTo("Saved")
    }

    @Test
    fun `hide_share drops share and nothing else`() {
        val kinds = rail(controls = FeedPostControls(hideShare = true)).map { it.kind }

        assertThat(kinds).containsExactly(RailKind.LIKE, RailKind.COMMENT, RailKind.SAVE, RailKind.MORE).inOrder()
    }

    @Test
    fun `no_comments drops comment and nothing else`() {
        val kinds = rail(controls = FeedPostControls(noComments = true)).map { it.kind }

        assertThat(kinds).containsExactly(RailKind.LIKE, RailKind.SHARE, RailKind.SAVE, RailKind.MORE).inOrder()
    }

    @Test
    fun `more is there only when the surface has a sheet to open`() {
        assertThat(rail(offersMore = false).map { it.kind }).doesNotContain(RailKind.MORE)
        assertThat(rail(offersMore = true).last().kind).isEqualTo(RailKind.MORE)
    }

    // ── The author line ─────────────────────────────────────────────────

    @Test
    fun `a handle is printed with an at sign`() {
        assertThat(reelAuthorLabel("call_userb", "Call Userb")).isEqualTo("@call_userb")
        assertThat(reelAuthorLabel("@call_userb", "Call Userb")).isEqualTo("@call_userb")
    }

    @Test
    fun `an account without a handle shows its display name, no at sign`() {
        assertThat(reelAuthorLabel(null, "Ada Lovelace")).isEqualTo("Ada Lovelace")
        assertThat(reelAuthorLabel("", "Ada Lovelace")).isEqualTo("Ada Lovelace")
        assertThat(reelAuthorLabel("   ", "Ada Lovelace")).isEqualTo("Ada Lovelace")
    }

    // ── The playhead ────────────────────────────────────────────────────

    @Test
    fun `the fraction is position over duration`() {
        assertThat(progressFraction(2_500L, 10_000L)).isEqualTo(0.25f)
        assertThat(progressFraction(10_000L, 10_000L)).isEqualTo(1f)
    }

    @Test
    fun `an unknown duration is no progress, never a division by zero`() {
        assertThat(progressFraction(3_000L, 0L)).isEqualTo(0f)
        // C.TIME_UNSET is Long.MIN_VALUE + 1: negative, like any "not yet".
        assertThat(progressFraction(3_000L, Long.MIN_VALUE + 1)).isEqualTo(0f)
    }

    @Test
    fun `the fraction clamps to the frame`() {
        assertThat(progressFraction(12_000L, 10_000L)).isEqualTo(1f)
        assertThat(progressFraction(-500L, 10_000L)).isEqualTo(0f)
    }
}
