package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Which post the reader is ON — the card a dwell is measured against.
 *
 * The same shape as [FeedAutoplayRuleTest], and deliberately the same 60 %
 * bar, but over whole ROWS rather than video frames: a photo, a poll and a
 * text post all have to be candidates, because they are the posts nothing
 * else in the analytics pipeline can measure at all.
 *
 * Pinned as a pure rule so the list's layout info cannot drift from it, and
 * because the two cases that matter are the ones a frame-shaped rule gets
 * wrong: a short text post, and a row taller than the screen.
 */
class FeedDwellRuleTest {

    private val viewportTop = 0
    private val viewportBottom = 1000

    private fun row(id: String, top: Int, height: Int) = VisibleFrame(id, top, top + height)

    @Test
    fun `a row fully on screen is the one being read`() {
        assertThat(mostDwelledPost(listOf(row("a", 100, 800)), viewportTop, viewportBottom)).isEqualTo("a")
    }

    @Test
    fun `an empty screen has nobody reading anything`() {
        assertThat(mostDwelledPost(emptyList(), viewportTop, viewportBottom)).isNull()
    }

    /** 60 % of an 800px row is 480px: 479 showing is not being read, 480 is. */
    @Test
    fun `a row is read only when at least 60 percent of it is visible`() {
        assertThat(mostDwelledPost(listOf(row("a", 521, 800)), viewportTop, viewportBottom)).isNull()
        assertThat(mostDwelledPost(listOf(row("a", 520, 800)), viewportTop, viewportBottom)).isEqualTo("a")
    }

    /**
     * A two-line text post is a short row, and short rows are the whole reason
     * this rule exists: nothing else in the pipeline measures them.
     */
    @Test
    fun `a short text row qualifies on its own height, not the screen's`() {
        assertThat(mostDwelledPost(listOf(row("text", 400, 180)), viewportTop, viewportBottom)).isEqualTo("text")
    }

    /**
     * The case a frame-shaped rule silently loses: a row TALLER than the
     * viewport can never be 60 % visible, so it would never be read by
     * anybody — and a long caption is precisely the post whose dwell is worth
     * knowing. It qualifies by filling the screen instead.
     */
    @Test
    fun `a row taller than the screen qualifies by filling the screen`() {
        val tall = row("essay", top = -400, height = 2_400)
        assertThat(mostDwelledPost(listOf(tall), viewportTop, viewportBottom)).isEqualTo("essay")
    }

    /** A card peeking in at the bottom does not take the reader off the one above it. */
    @Test
    fun `a card peeking in does not steal the reader`() {
        val rows = listOf(row("a", 100, 800), row("b", 950, 800))
        assertThat(mostDwelledPost(rows, viewportTop, viewportBottom)).isEqualTo("a")
    }

    /**
     * Exactly one at a time. Three short posts fully on screen for ten seconds
     * are not thirty seconds of reading, and reporting them as three
     * ten-second dwells would teach the ranker that short posts hold people
     * three times over. All three are equally visible, so the tie rule below
     * decides — the reader has just scrolled, and they are looking at the top.
     */
    @Test
    fun `only one post is being read even when several are fully visible`() {
        val rows = listOf(row("a", 0, 200), row("b", 210, 300), row("c", 520, 250))
        assertThat(mostDwelledPost(rows, viewportTop, viewportBottom)).isEqualTo("a")
    }

    /** Ties go to the row nearer the top, so a nudge does not flip the reader between two. */
    @Test
    fun `a tie goes to the row nearer the top`() {
        val rows = listOf(row("a", 100, 300), row("b", 500, 300))
        assertThat(mostDwelledPost(rows, viewportTop, viewportBottom)).isEqualTo("a")
    }

    @Test
    fun `a row with no height is never a candidate`() {
        assertThat(mostDwelledPost(listOf(VisibleFrame("a", 100, 100)), viewportTop, viewportBottom)).isNull()
    }

    // ── addressing a row by key, not by index ───────────────────────────

    /**
     * The list's rows are not all paged rows. A lookup by index attributes the
     * reader's time to the row above the one they are on whenever the pinned
     * just-published post is present, so the id is read off the key instead.
     */
    @Test
    fun `a post id is read off the row's key`() {
        assertThat(dwellPostId("018f2c1e-0000-7000-8000-000000000001"))
            .isEqualTo("018f2c1e-0000-7000-8000-000000000001")
        assertThat(dwellPostId("${HEAD_KEY_PREFIX}018f2c1e-0000-7000-8000-000000000002"))
            .isEqualTo("018f2c1e-0000-7000-8000-000000000002")
    }

    @Test
    fun `rows that are not posts carry no id`() {
        // The append footer, and a paged row whose item has not loaded yet
        // (the list falls back to the Int index for its key).
        assertThat(dwellPostId(APPEND_KEY)).isNull()
        assertThat(dwellPostId(7)).isNull()
        assertThat(dwellPostId(null)).isNull()
        assertThat(dwellPostId(HEAD_KEY_PREFIX)).isNull()
    }
}
