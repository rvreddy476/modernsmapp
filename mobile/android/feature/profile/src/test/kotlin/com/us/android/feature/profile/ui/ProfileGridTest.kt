package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.VideoKind
import org.junit.Test

/** The media grid's rules: where a pending video goes, what each tab reads, and the tile shapes. */
class ProfileGridTest {

    @Test
    fun `a posting long video belongs on the Videos tab and a reel never shows here`() {
        assertThat(pendingTabFor(VideoKind.LONG)).isEqualTo(ProfileGridTab.VIDEOS)
        assertThat(pendingTabFor(VideoKind.REEL)).isNull()
        assertThat(pendingTabFor(null)).isNull()
    }

    @Test
    fun `the tabs read the three content types in order`() {
        assertThat(ProfileGridTab.entries.map { it.label }).containsExactly("Posts", "Reels", "Videos").inOrder()
        assertThat(ProfileGridTab.entries.map { it.contentType })
            .containsExactly("post", "flick", "long_video")
            .inOrder()
    }

    @Test
    fun `tiles are square for posts, portrait for reels, landscape for videos`() {
        assertThat(tileAspect(ProfileGridTab.POSTS)).isEqualTo(1f)
        assertThat(tileAspect(ProfileGridTab.REELS)).isLessThan(1f)
        assertThat(tileAspect(ProfileGridTab.VIDEOS)).isGreaterThan(1f)
    }

    @Test
    fun `a pending tile knows whether it stopped`() {
        val posting = PendingVideoTile("k", "/cache/k.jpg", "Title", ReelPublishState.Uploading(0.4f))
        val stopped = PendingVideoTile("k", "/cache/k.jpg", "Title", ReelPublishState.Failed("no", retryable = true))

        assertThat(posting.failure).isNull()
        assertThat(stopped.failure?.retryable).isTrue()
    }
}
