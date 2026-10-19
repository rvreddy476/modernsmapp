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
 *    there is not — "8.8K", and "0" at zero; a name only for a count never sent;
 *  - share and save carry their counts too, when the row sends them
 *    (2026-09-30), and a count the row never gave is never invented;
 *  - `hide_share` removes Share, `no_comments` removes Comment, and nothing
 *    the author sets touches Like or Save;
 *  - there is no More on the rail: it moved to the header's hamburger
 *    (founder, 2026-09-05), so the rail is Like, Comment, Save, Share — the
 *    shared full-screen rail's order since 2026-10-18;
 *  - "@handle" only where there is a handle; a display name gets no "@";
 *  - the playhead fraction is 0..1 whatever the player reports.
 */
class ReelRailTest {

    private fun rail(
        controls: FeedPostControls = FeedPostControls(),
        likes: Int = 0,
        comments: Int = 0,
        saved: Boolean = false,
        shares: Int? = null,
        saves: Int? = null,
    ) = railControls(controls, likes, comments, saved, shares, saves)

    @Test
    fun `the rail is like, comment, save, share, zero shows as 0 and an unsent count as the name`() {
        assertThat(rail().map { it.kind to it.label }).containsExactly(
            RailKind.LIKE to "0",
            RailKind.COMMENT to "0",
            RailKind.SAVE to "Save",
            RailKind.SHARE to "Share",
        ).inOrder()
    }

    @Test
    fun `like and comment carry their compact counts`() {
        val labels = rail(likes = 8_800, comments = 1_234_567).associate { it.kind to it.label }

        assertThat(labels[RailKind.LIKE]).isEqualTo("8.8K")
        assertThat(labels[RailKind.COMMENT]).isEqualTo("1.2M")
    }

    @Test
    fun `a count label shows zero as 0 and falls back to the noun only when no count was sent`() {
        assertThat(railCountLabel(0, "Like")).isEqualTo("0")
        assertThat(railCountLabel(-3, "Comment")).isEqualTo("0")
        assertThat(railCountLabel(null, "Share")).isEqualTo("Share")
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
    fun `share and save carry their compact counts when the row sends them`() {
        val labels = rail(shares = 1_200, saves = 45, saved = true).associate { it.kind to it.label }

        assertThat(labels[RailKind.SHARE]).isEqualTo("1.2K")
        assertThat(labels[RailKind.SAVE]).isEqualTo("45")
    }

    @Test
    fun `a share or save count of zero shows 0, and only a count never sent falls back to the name`() {
        val zero = rail(shares = 0, saves = 0).associate { it.kind to it.label }
        assertThat(zero[RailKind.SHARE]).isEqualTo("0")
        assertThat(zero[RailKind.SAVE]).isEqualTo("0")

        val absent = rail(saved = true).associate { it.kind to it.label }
        assertThat(absent[RailKind.SHARE]).isEqualTo("Share")
        assertThat(absent[RailKind.SAVE]).isEqualTo("Saved")
    }

    @Test
    fun `this session's save is layered on the row's count, and a count never given stays absent`() {
        assertThat(layeredSaves(10, serverSaved = false, saved = true)).isEqualTo(11)
        assertThat(layeredSaves(10, serverSaved = true, saved = false)).isEqualTo(9)
        assertThat(layeredSaves(10, serverSaved = true, saved = true)).isEqualTo(10)
        assertThat(layeredSaves(10, serverSaved = false, saved = false)).isEqualTo(10)
        assertThat(layeredSaves(0, serverSaved = true, saved = false)).isEqualTo(0)
        assertThat(layeredSaves(null, serverSaved = false, saved = true)).isNull()
    }

    @Test
    fun `hide_share drops share and nothing else`() {
        val kinds = rail(controls = FeedPostControls(hideShare = true)).map { it.kind }

        assertThat(kinds).containsExactly(RailKind.LIKE, RailKind.COMMENT, RailKind.SAVE).inOrder()
    }

    @Test
    fun `no_comments drops comment and nothing else`() {
        val kinds = rail(controls = FeedPostControls(noComments = true)).map { it.kind }

        assertThat(kinds).containsExactly(RailKind.LIKE, RailKind.SAVE, RailKind.SHARE).inOrder()
    }

    @Test
    fun `nothing the author sets touches like or save, and more is not on the rail`() {
        val every = rail(controls = FeedPostControls(hideShare = true, noComments = true)).map { it.kind }

        assertThat(every).containsExactly(RailKind.LIKE, RailKind.SAVE).inOrder()
        assertThat(RailKind.entries)
            .containsExactly(RailKind.LIKE, RailKind.COMMENT, RailKind.SAVE, RailKind.SHARE)
            .inOrder()
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
