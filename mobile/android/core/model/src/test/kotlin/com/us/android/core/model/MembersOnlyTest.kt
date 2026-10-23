package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The members-only lock decision (paid memberships, 2026-10-23). What it
 * protects: a body the server withheld is ALWAYS a lock, whoever asks; a
 * gated row with nothing to draw is a lock, never an empty post or a player
 * started from nothing; an open row is never locked.
 */
class MembersOnlyTest {

    private val gated = MembersOnly(tierRequiredId = "tier-1")
    private val redacted = MembersOnly(tierRequiredId = "tier-1", bodyRedacted = true, redactedDurationMs = 754_000)

    @Test
    fun `the wire reads as no gate, a gate, or a redacted gate`() {
        assertThat(MembersOnly.fromWire(null, false, 0)).isEqualTo(MembersOnly.None)
        assertThat(MembersOnly.fromWire("  ", false, 0)).isEqualTo(MembersOnly.None)
        assertThat(MembersOnly.fromWire("tier-1", false, 0)).isEqualTo(gated)
        assertThat(MembersOnly.fromWire(" tier-1 ", true, 754_000)).isEqualTo(redacted)
        // A redacted row without its level is still gated: the body is gone.
        assertThat(MembersOnly.fromWire(null, true, -5).isGated).isTrue()
        assertThat(MembersOnly.fromWire(null, true, -5).redactedDurationMs).isEqualTo(0)
    }

    @Test
    fun `a redacted row is locked whoever asks, the author included`() {
        assertThat(redacted.access(isAuthor = false, hasBody = false)).isEqualTo(MembersAccess.Locked)
        assertThat(redacted.access(isAuthor = true, hasBody = true)).isEqualTo(MembersAccess.Locked)
        assertThat(
            MembersOnly(bodyRedacted = true).access(isAuthor = false, hasBody = true)
        ).isEqualTo(MembersAccess.Locked)
    }

    @Test
    fun `an open row is open, with or without a body`() {
        assertThat(MembersOnly.None.access(isAuthor = false, hasBody = true)).isEqualTo(MembersAccess.Open)
        assertThat(MembersOnly.None.access(isAuthor = false, hasBody = false)).isEqualTo(MembersAccess.Open)
    }

    @Test
    fun `a gated row with its body is the member's, and without one it is locked`() {
        assertThat(gated.access(isAuthor = false, hasBody = true)).isEqualTo(MembersAccess.Member)
        assertThat(gated.access(isAuthor = true, hasBody = false)).isEqualTo(MembersAccess.Member)
        assertThat(gated.access(isAuthor = false, hasBody = false)).isEqualTo(MembersAccess.Locked)
    }

    @Test
    fun `a feed row's body is its media, its text or its poll`() {
        val row = feedItem(membersOnly = gated)
        assertThat(row.membersAccess(viewerId = "viewer")).isEqualTo(MembersAccess.Locked)
        assertThat(row.copy(text = "hello").membersAccess("viewer")).isEqualTo(MembersAccess.Member)
        assertThat(row.membersAccess(viewerId = "author")).isEqualTo(MembersAccess.Member)
        assertThat(row.membersAccess(viewerId = null)).isEqualTo(MembersAccess.Locked)
    }

    @Test
    fun `something that must play needs its media, even for a row that has words`() {
        val row = feedItem(membersOnly = gated, text = "caption kept by a member's row")
        assertThat(row.membersPlayback("viewer")).isEqualTo(MembersAccess.Locked)
        // The author's own row without media has nothing to play either.
        assertThat(row.membersPlayback("author")).isEqualTo(MembersAccess.Locked)
        val withVideo = row.copy(media = listOf(FeedMedia(mediaId = "m1", kind = "video")))
        assertThat(withVideo.membersPlayback("viewer")).isEqualTo(MembersAccess.Member)
        assertThat(withVideo.copy(membersOnly = redacted).membersPlayback("author")).isEqualTo(MembersAccess.Locked)
        assertThat(withVideo.copy(membersOnly = MembersOnly.None).membersPlayback(null)).isEqualTo(MembersAccess.Open)
    }

    private fun feedItem(membersOnly: MembersOnly, text: String = "") = FeedItem(
        id = "p1",
        authorId = "author",
        author = FeedAuthor(id = "author", displayName = "Asha"),
        text = text,
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
        membersOnly = membersOnly,
    )
}
