package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.AuthorPostSort
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.ReelPublishState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Test

/** The media grid's rules: where a pending video goes, what each tab reads, and the tile shapes. */
@OptIn(ExperimentalCoroutinesApi::class)
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
    fun `a long video held for copyright review says so instead of the green words`() {
        val held = PublishSuccess(ProfileGridTab.VIDEOS, notice = "In copyright review — this video is hidden until our team checks it.")
        assertThat(held.message).isEqualTo("In copyright review — this video is hidden until our team checks it.")
        assertThat(held.held).isTrue()
        assertThat(PublishSuccess(ProfileGridTab.VIDEOS).held).isFalse()
    }

    @Test
    fun `a scheduled post lands on the tab of its content type`() {
        assertThat(scheduledTabFor("flick")).isEqualTo(ProfileGridTab.REELS)
        assertThat(scheduledTabFor("long_video")).isEqualTo(ProfileGridTab.VIDEOS)
        assertThat(scheduledTabFor("post")).isEqualTo(ProfileGridTab.POSTS)
        assertThat(scheduledTabFor("poll")).isNull()
    }

    @Test
    fun `the author tabs read the three content types in order`() {
        val authored = ProfileGridTab.entries.filter { it.authored }
        assertThat(authored.map { it.label }).containsExactly("Posts", "Reels", "Videos").inOrder()
        assertThat(authored.map { it.contentType })
            .containsExactly("post", "flick", "long_video")
            .inOrder()
    }

    // ── Owner / visitor tab sets (T-21) ──────────────────────────────────

    @Test
    fun `a visitor gets the three author tabs and never Saved or Liked`() {
        assertThat(gridTabsFor(isOwn = false))
            .containsExactly(ProfileGridTab.POSTS, ProfileGridTab.REELS, ProfileGridTab.VIDEOS)
            .inOrder()
    }

    /** Liked (2026-10-17): `GET /v1/reels/liked` is written and backfilled now, and it is the viewer's own. */
    @Test
    fun `the owner gets Saved and then Liked after the author tabs`() {
        assertThat(gridTabsFor(isOwn = true))
            .containsExactly(
                ProfileGridTab.POSTS,
                ProfileGridTab.REELS,
                ProfileGridTab.VIDEOS,
                ProfileGridTab.SAVED,
                ProfileGridTab.LIKED,
            )
            .inOrder()
    }

    @Test
    fun `only the author tabs sort, and Saved and Liked claim no content type`() {
        assertThat(ProfileGridTab.entries.filter { it.sortable })
            .containsExactly(ProfileGridTab.POSTS, ProfileGridTab.REELS, ProfileGridTab.VIDEOS)
        assertThat(ProfileGridTab.LIKED.authored).isFalse()
        assertThat(scheduledTabFor("")).isNull()
        assertThat(emptyTitle(ProfileGridTab.SAVED)).isEqualTo("Nothing saved yet")
        assertThat(emptyTitle(ProfileGridTab.REELS)).isEqualTo("No reels yet")
    }

    @Test
    fun `an empty Liked tab says so in its own words, and its tiles are reel-shaped`() {
        assertThat(emptyTitle(ProfileGridTab.LIKED)).isEqualTo("No liked reels yet")
        assertThat(tabNoun(ProfileGridTab.LIKED)).isEqualTo("liked reels")
        assertThat(tileAspect(ProfileGridTab.LIKED)).isEqualTo(tileAspect(ProfileGridTab.REELS))
    }

    /** Nothing is published into Liked, so no pending or scheduled tile can land there. */
    @Test
    fun `no publish or schedule lands on the Liked tab`() {
        assertThat(PublishKind.entries.map { pendingTabFor(it) }).doesNotContain(ProfileGridTab.LIKED)
        assertThat(listOf("post", "flick", "long_video", "").map { scheduledTabFor(it) })
            .doesNotContain(ProfileGridTab.LIKED)
    }

    // ── Sort (T-21) ──────────────────────────────────────────────────────

    @Test
    fun `a sort change re-reads the tab from a fresh source`() = runTest {
        val sort = MutableStateFlow(AuthorPostSort.LATEST)
        val opened = mutableListOf<AuthorPostSort>()
        val seen = mutableListOf<String>()

        val job = launch(UnconfinedTestDispatcher(testScheduler)) {
            pagesBySort(sort) { s ->
                opened += s
                flowOf("page-1:${s.wire}")
            }.collect { seen += it }
        }
        sort.value = AuthorPostSort.POPULAR
        sort.value = AuthorPostSort.OLDEST
        job.cancel()

        assertThat(opened).containsExactly(AuthorPostSort.LATEST, AuthorPostSort.POPULAR, AuthorPostSort.OLDEST)
            .inOrder()
        assertThat(seen).containsExactly("page-1:latest", "page-1:popular", "page-1:oldest").inOrder()
    }

    /** Re-picking the sort already on screen must not throw the loaded pages away. */
    @Test
    fun `re-selecting the current sort keeps the source it has`() = runTest {
        val sort = MutableStateFlow(AuthorPostSort.POPULAR)
        val opened = mutableListOf<AuthorPostSort>()

        val job = launch(UnconfinedTestDispatcher(testScheduler)) {
            pagesBySort(sort.map { it }) { s ->
                opened += s
                flowOf(s)
            }.collect { }
        }
        sort.value = AuthorPostSort.POPULAR
        job.cancel()

        assertThat(opened).containsExactly(AuthorPostSort.POPULAR)
    }

    /** The old sort's pager is dropped, not left running beside the new one. */
    @Test
    fun `the previous sort's source is cancelled when the sort changes`() = runTest {
        val sort = MutableStateFlow(AuthorPostSort.LATEST)
        val live = mutableSetOf<AuthorPostSort>()

        val job = launch(UnconfinedTestDispatcher(testScheduler)) {
            pagesBySort(sort) { s ->
                flow<AuthorPostSort> {
                    live += s
                    try {
                        awaitCancellation()
                    } finally {
                        live -= s
                    }
                }
            }.collect { }
        }
        sort.value = AuthorPostSort.OLDEST
        val during = live.toSet()
        job.cancel()

        assertThat(during).containsExactly(AuthorPostSort.OLDEST)
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

    /** 2026-10-19: a failed photo post said "Couldn't post your video". */
    @Test
    fun `the failure sheet names photos for a post and video for the video tabs`() {
        val failed = ReelPublishState.Failed("That didn't finish uploading. Try again.", retryable = true)
        fun tile(tab: ProfileGridTab) = PendingVideoTile("k", null, "", failed, tab)

        assertThat(tile(ProfileGridTab.POSTS).failureTitle).isEqualTo("Couldn't post your photos")
        assertThat(tile(ProfileGridTab.REELS).failureTitle).isEqualTo("Couldn't post your video")
        assertThat(tile(ProfileGridTab.VIDEOS).failureTitle).isEqualTo("Couldn't post your video")
    }

    /** Video credits (2026-10-23): "Appears in" on anyone's profile, only when it holds something. */
    @Test
    fun `Appears in follows the author tabs when there is something in it, on any profile`() {
        assertThat(gridTabsFor(isOwn = false, appearsIn = true))
            .containsExactly(ProfileGridTab.POSTS, ProfileGridTab.REELS, ProfileGridTab.VIDEOS, ProfileGridTab.APPEARS_IN)
            .inOrder()
        assertThat(gridTabsFor(isOwn = true, appearsIn = true))
            .containsExactly(
                ProfileGridTab.POSTS,
                ProfileGridTab.REELS,
                ProfileGridTab.VIDEOS,
                ProfileGridTab.APPEARS_IN,
                ProfileGridTab.SAVED,
                ProfileGridTab.LIKED,
            )
            .inOrder()
        assertThat(gridTabsFor(isOwn = true)).doesNotContain(ProfileGridTab.APPEARS_IN)
        // Not an author tab: it sorts by nothing, and no scheduled post lands in it.
        assertThat(ProfileGridTab.APPEARS_IN.sortable).isFalse()
        assertThat(scheduledTabFor("long_video")).isEqualTo(ProfileGridTab.VIDEOS)
    }

    /** Paid memberships (2026-10-23): "Members" on anyone's profile while the creator offers a level. */
    @Test
    fun `Members follows the author tabs while a level is offered, on any profile`() {
        assertThat(gridTabsFor(isOwn = false, members = true))
            .containsExactly(ProfileGridTab.POSTS, ProfileGridTab.REELS, ProfileGridTab.VIDEOS, ProfileGridTab.MEMBERS)
            .inOrder()
        assertThat(gridTabsFor(isOwn = true, appearsIn = true, members = true))
            .containsExactly(
                ProfileGridTab.POSTS,
                ProfileGridTab.REELS,
                ProfileGridTab.VIDEOS,
                ProfileGridTab.MEMBERS,
                ProfileGridTab.APPEARS_IN,
                ProfileGridTab.SAVED,
                ProfileGridTab.LIKED,
            )
            .inOrder()
        assertThat(gridTabsFor(isOwn = true)).doesNotContain(ProfileGridTab.MEMBERS)
        // Not an author tab: it mixes every kind, sorts by nothing, and no publish lands in it.
        assertThat(ProfileGridTab.MEMBERS.authored).isFalse()
        assertThat(ProfileGridTab.MEMBERS.sortable).isFalse()
        assertThat(scheduledTabFor("")).isNull()
    }
}
