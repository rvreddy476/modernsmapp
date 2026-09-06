package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.ReelPublishState
import org.junit.Test

/** The media grid's rules: where a pending video goes, what each tab reads, and the tile shapes. */
class ProfileGridTest {

    @Test
    fun `a posting long video belongs on the Videos tab and a reel on the Reels tab`() {
        assertThat(pendingTabFor(PublishKind.LONG)).isEqualTo(ProfileGridTab.VIDEOS)
        assertThat(pendingTabFor(PublishKind.REEL)).isEqualTo(ProfileGridTab.REELS)
        assertThat(pendingTabFor(null)).isNull()
    }

    /**
     * The photo studio joined this queue on 2026-09-06. Without a tab its
     * pending tile is dropped on the floor and pressing Post lands the viewer
     * on a profile showing no sign that anything is happening.
     */
    @Test
    fun `a posting photo belongs on the Posts tab`() {
        assertThat(pendingTabFor(PublishKind.PHOTO)).isEqualTo(ProfileGridTab.POSTS)
    }

    /** Every kind the queue can carry has somewhere to be drawn. */
    @Test
    fun `no publish kind is left without a tab`() {
        assertThat(PublishKind.entries.map { pendingTabFor(it) }).doesNotContain(null)
    }

    /**
     * The green moment (founder, 2026-09-06): the same three words whatever
     * was posted, and no control on it — it takes itself away.
     */
    @Test
    fun `every finished publish says the same thing`() {
        assertThat(ProfileGridTab.entries.map { PublishSuccess(it).message }.distinct())
            .containsExactly("Uploaded successfully")
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
