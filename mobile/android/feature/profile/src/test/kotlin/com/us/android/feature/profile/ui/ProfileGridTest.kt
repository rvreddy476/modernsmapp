package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.VideoKind
import org.junit.Test

/** The media grid's rules: where a pending video goes, what each tab reads, and the tile shapes. */
class ProfileGridTest {

    @Test
    fun `a posting long video belongs on the Videos tab and a reel on the Reels tab`() {
        assertThat(pendingTabFor(VideoKind.LONG)).isEqualTo(ProfileGridTab.VIDEOS)
        assertThat(pendingTabFor(VideoKind.REEL)).isEqualTo(ProfileGridTab.REELS)
        assertThat(pendingTabFor(null)).isNull()
    }

    @Test
    fun `a scheduled post lands on the tab of its content type`() {
        assertThat(scheduledTabFor("flick")).isEqualTo(ProfileGridTab.REELS)
        assertThat(scheduledTabFor("long_video")).isEqualTo(ProfileGridTab.VIDEOS)
        assertThat(scheduledTabFor("post")).isEqualTo(ProfileGridTab.POSTS)
        assertThat(scheduledTabFor("poll")).isNull()
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
        val tab = ProfileGridTab.REELS
        val posting = PendingVideoTile("k", "/cache/k.jpg", "Title", ReelPublishState.Uploading(0.4f), tab)
        val failed = ReelPublishState.Failed("no", retryable = true)
        val stopped = PendingVideoTile("k", "/cache/k.jpg", "Title", failed, tab)

        assertThat(posting.failure).isNull()
        assertThat(stopped.failure?.retryable).isTrue()
        assertThat(posting.scheduleLabel).isNull()
    }

    /** A scheduled pending tile says when, in the viewer's zone, and a bad wire value says nothing. */
    @Test
    fun `a pending tile with a publish time wears the schedule label`() {
        val scheduled = PendingVideoTile(
            creationKey = "k",
            coverPath = "/cache/k.jpg",
            title = "Title",
            state = ReelPublishState.Uploading(0.4f),
            tab = ProfileGridTab.REELS,
            publishAt = "2026-09-06T13:00:00Z",
        )
        val broken = scheduled.copy(publishAt = "tomorrow-ish")

        assertThat(scheduled.scheduleLabel).startsWith("Scheduled · ")
        assertThat(scheduled.scheduleLabel).contains("Sep")
        assertThat(broken.scheduleLabel).isNull()
    }
}
