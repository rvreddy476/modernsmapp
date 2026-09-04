package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Which feed video plays (founder, 2026-09-05): the most visible video
 * frame, and only when at least 60% of it is on screen. Pinned as a pure
 * rule over frame rectangles so the list's layout info cannot drift from
 * it: a card peeking in must not steal playback, two fully visible cards
 * must not swap it back and forth, and an empty screen plays nothing.
 */
class FeedAutoplayRuleTest {

    /** A 1000px viewport; frames are 800px tall like a 4:5 frame on a 640px-wide list. */
    private val viewportTop = 0
    private val viewportBottom = 1000

    private fun frame(id: String, top: Int, height: Int = 800) = VisibleFrame(id, top, top + height)

    @Test
    fun `a frame fully on screen plays`() {
        assertThat(mostVisibleVideo(listOf(frame("a", 100)), viewportTop, viewportBottom)).isEqualTo("a")
    }

    @Test
    fun `nothing plays when no video is on screen`() {
        assertThat(mostVisibleVideo(emptyList(), viewportTop, viewportBottom)).isNull()
    }

    /** 60% of 800 is 480: a frame showing 479px stays a poster, one showing 480px plays. */
    @Test
    fun `a frame plays only when at least 60 percent of it is visible`() {
        assertThat(mostVisibleVideo(listOf(frame("a", top = 521)), viewportTop, viewportBottom)).isNull()
        assertThat(mostVisibleVideo(listOf(frame("a", top = 520)), viewportTop, viewportBottom)).isEqualTo("a")
    }

    /** The same rule leaving at the top: 479px still on screen is not enough. */
    @Test
    fun `a frame scrolling out at the top stops at the same threshold`() {
        assertThat(mostVisibleVideo(listOf(frame("a", top = -321)), viewportTop, viewportBottom)).isNull()
        assertThat(mostVisibleVideo(listOf(frame("a", top = -320)), viewportTop, viewportBottom)).isEqualTo("a")
    }

    /** The one with more of its frame on screen wins, wherever it sits. */
    @Test
    fun `of two candidates the more visible one plays`() {
        // a shows 500px of 800, b shows 550px: b plays.
        assertThat(mostVisibleVideo(listOf(frame("a", top = -300), frame("b", top = 450)), viewportTop, viewportBottom))
            .isEqualTo("b")
        // Scrolled up 100px: a shows 600px, b shows 450px: a plays.
        assertThat(mostVisibleVideo(listOf(frame("a", top = -200), frame("b", top = 550)), viewportTop, viewportBottom))
            .isEqualTo("a")
    }

    /** Two frames equally visible: the first — nearer the top — plays, and keeps playing on a nudge. */
    @Test
    fun `a tie goes to the frame nearer the top`() {
        val frames = listOf(frame("a", top = -300), frame("b", top = 500))
        assertThat(mostVisibleVideo(frames, viewportTop, viewportBottom)).isEqualTo("a")
    }

    /** A card peeking in at the bottom does not take playback from the one the reader is looking at. */
    @Test
    fun `a card peeking in does not steal playback`() {
        val frames = listOf(frame("a", top = 100), frame("b", top = 950))
        assertThat(mostVisibleVideo(frames, viewportTop, viewportBottom)).isEqualTo("a")
    }

    /** The viewport may not start at zero (content padding); the rule reads the bounds it is given. */
    @Test
    fun `the viewport bounds are honoured`() {
        assertThat(mostVisibleVideo(listOf(frame("a", top = 200)), viewportTop = 200, viewportBottom = 1200))
            .isEqualTo("a")
        assertThat(mostVisibleVideo(listOf(frame("a", top = 200)), viewportTop = 800, viewportBottom = 1800))
            .isNull()
    }

    @Test
    fun `a frame with no height is never a candidate`() {
        assertThat(mostVisibleVideo(listOf(VisibleFrame("a", 100, 100)), viewportTop, viewportBottom)).isNull()
    }
}
