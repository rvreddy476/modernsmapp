package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.tube.data.SeriesEpisode
import com.us.android.feature.tube.data.SeriesInfo
import org.junit.Test

/** The series rule: next and previous by episode number, and what the end of a video does. */
class SeriesAdvanceTest {

    // Numbered 1, 2, 4: episode 3 is unpublished or hidden from this viewer.
    private val gapped = listOf(episode("p1", 1), episode("p2", 2), episode("p4", 4))
    private val series = SeriesInfo(id = "s", title = "Season one", episodes = gapped)

    @Test
    fun `next skips the gap and prev steps back over it`() {
        assertThat(nextEpisode(gapped, "p2")?.postId).isEqualTo("p4")
        assertThat(prevEpisode(gapped, "p4")?.postId).isEqualTo("p2")
        assertThat(prevEpisode(gapped, "p2")?.postId).isEqualTo("p1")
    }

    @Test
    fun `next goes by episode number even when the list is out of order`() {
        val shuffled = listOf(episode("p4", 4), episode("p1", 1), episode("p2", 2))
        assertThat(nextEpisode(shuffled, "p1")?.postId).isEqualTo("p2")
        assertThat(nextEpisode(shuffled, "p2")?.postId).isEqualTo("p4")
    }

    @Test
    fun `the last episode has no next and the first has no prev`() {
        assertThat(nextEpisode(gapped, "p4")).isNull()
        assertThat(prevEpisode(gapped, "p1")).isNull()
    }

    @Test
    fun `a post the list does not know has neither`() {
        assertThat(nextEpisode(gapped, "zz")).isNull()
        assertThat(prevEpisode(gapped, "zz")).isNull()
    }

    @Test
    fun `the end of a video counts down to the next episode`() {
        val end = endOfVideo(series, "p2", autoplayEnabled = true)
        assertThat(end).isEqualTo(EndOfVideo.Countdown(episode("p4", 4), seconds = AUTOPLAY_NEXT_SECONDS))
        assertThat(AUTOPLAY_NEXT_SECONDS).isEqualTo(10)
    }

    @Test
    fun `the end screen when autoplay is off`() {
        assertThat(endOfVideo(series, "p2", autoplayEnabled = false)).isEqualTo(EndOfVideo.EndScreen)
    }

    @Test
    fun `the end screen when there is no series`() {
        assertThat(endOfVideo(null, "p2", autoplayEnabled = true)).isEqualTo(EndOfVideo.EndScreen)
    }

    @Test
    fun `the end screen after the last episode`() {
        assertThat(endOfVideo(series, "p4", autoplayEnabled = true)).isEqualTo(EndOfVideo.EndScreen)
    }

    private fun episode(postId: String, num: Int) = SeriesEpisode(postId = postId, episodeNum = num, title = "Ep $num")
}
