package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/** Picture in picture (2026-10-24): which videos shrink into the window when the app is left, and its control. */
class WatchPictureInPictureTest {

    private val video = FeedItem(
        id = "v1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Kiran"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(false, false, false),
        isRepostable = false,
    )
    private val playable = WatchContent.Ready(video, Playback("https://gw/v1/media/m/hls/master.m3u8", PlaybackKind.Hls))

    @Test
    fun `a playing video on a device with the window is eligible`() {
        assertThat(pictureInPictureEligible(playable, playing = true, ended = false, supported = true)).isTrue()
    }

    @Test
    fun `nothing else is`() {
        assertThat(pictureInPictureEligible(playable, playing = false, ended = false, supported = true)).isFalse()
        assertThat(pictureInPictureEligible(playable, playing = true, ended = true, supported = true)).isFalse()
        assertThat(pictureInPictureEligible(playable, playing = true, ended = false, supported = false)).isFalse()
        assertThat(pictureInPictureEligible(playable.copy(locked = true), playing = true, ended = false, supported = true)).isFalse()
        assertThat(pictureInPictureEligible(playable.copy(playback = null), playing = true, ended = false, supported = true)).isFalse()
        assertThat(pictureInPictureEligible(WatchContent.Loading, playing = true, ended = false, supported = true)).isFalse()
        assertThat(pictureInPictureEligible(WatchContent.Failed("x"), playing = true, ended = false, supported = true)).isFalse()
    }

    @Test
    fun `the window offers Pause while playing and Play while held`() {
        assertThat(pipToggle(playing = true)).isEqualTo(PipToggle("Pause", playIcon = false))
        assertThat(pipToggle(playing = false)).isEqualTo(PipToggle("Play", playIcon = true))
    }
}
