package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.dto.FeedItemDto
import org.junit.Test

/** The library's pure rules, as tables: where a video goes in a collection, what a list shows, what Saved lists. */
class VideoLibraryRulesTest {

    private fun row(postId: String, position: Int, post: FeedItemDto? = FeedItemDto(id = postId)) =
        PlaylistItemDto(playlistId = "c1", postId = postId, position = position, post = post)

    // ── The position a video is added at ────────────────────────────────

    /**
     * The server REPLACES whatever sits at a position that is taken. After a
     * removal the positions have a gap (0, 2), and "the count" (2) is then a
     * position in use: adding there would silently swap out a video.
     */
    @Test
    fun `the next position is one past the highest in use, never a position that is taken`() {
        assertThat(nextPosition(emptyList())).isEqualTo(0)
        assertThat(nextPosition(listOf(row("a", 0)))).isEqualTo(1)
        assertThat(nextPosition(listOf(row("a", 0), row("b", 1), row("c", 2)))).isEqualTo(3)
        // A gap left by a removal: the count is 2, and 2 is taken.
        val gapped = listOf(row("a", 0), row("c", 2))
        assertThat(nextPosition(gapped)).isEqualTo(3)
        assertThat(gapped.map { it.position }).doesNotContain(nextPosition(gapped))
        // Rows in any order.
        assertThat(nextPosition(listOf(row("c", 7), row("a", 0)))).isEqualTo(8)
    }

    // ── What a list shows ───────────────────────────────────────────────

    @Test
    fun `a list's videos are in position order, without the unseen, the deleted and repeats`() {
        val rows = listOf(
            row("late", 5),
            row("early", 0),
            row("hidden", 1, post = null),
            row("deleted", 2, post = FeedItemDto(id = "deleted", deletedAt = "2026-09-30T00:00:00Z")),
            row("early", 3),
            row("blank", 4, post = FeedItemDto(id = "")),
        )

        assertThat(rows.toVideos().map { it.id }).containsExactly("early", "late").inOrder()
    }

    @Test
    fun `the system list is named Watch later whatever the server's row title says`() {
        val system = PlaylistDto(id = "wl", title = "Queue", visibility = "private", kind = "watch_later")
        assertThat(system.toCollection().title).isEqualTo("Watch later")
        assertThat(system.toCollection().isWatchLater).isTrue()

        val own = PlaylistDto(id = "c1", title = "Queue", visibility = "public", kind = "user")
        assertThat(own.toCollection().title).isEqualTo("Queue")
        assertThat(own.toCollection().isWatchLater).isFalse()
    }

    /** The label must never overstate reach: anything that is not plainly public reads Private. */
    @Test
    fun `only a public collection reads as public`() {
        fun isPrivate(visibility: String) =
            PlaylistDto(id = "c", visibility = visibility, kind = "user").toCollection().isPrivate

        assertThat(isPrivate("public")).isFalse()
        assertThat(isPrivate("private")).isTrue()
        assertThat(isPrivate("unlisted")).isTrue()
        assertThat(isPrivate("")).isTrue()
    }

    // ── What the Saved page lists ───────────────────────────────────────

    /**
     * A saved reel is listed (2026-10-02). Before this the Saved page kept
     * long videos only, so a reel saved from the rail was listed nowhere.
     */
    @Test
    fun `saved lists long videos and reels, and nothing that does not play`() {
        assertThat(savedKind("long_video")).isEqualTo(SavedKind.VIDEO)
        assertThat(savedKind("flick")).isEqualTo(SavedKind.REEL)
        assertThat(savedKind("reel")).isEqualTo(SavedKind.REEL)
        assertThat(savedKind("short")).isEqualTo(SavedKind.REEL)
        assertThat(savedKind("post")).isNull()
        assertThat(savedKind("photo")).isNull()
        assertThat(savedKind("")).isNull()
    }

    // ── What a refusal says ─────────────────────────────────────────────

    @Test
    fun `a refusal is worded by what went wrong, with the caller's line as the fallback`() {
        val fallback = "Couldn't update Watch later. Try again."

        assertThat(VideoLibraryRepository.errorMessage(AppError.NoNetwork(), fallback))
            .isEqualTo("You're offline. Check your connection and try again.")
        assertThat(VideoLibraryRepository.errorMessage(AppError.Timeout(), fallback))
            .isEqualTo("That took too long. Try again.")
        assertThat(VideoLibraryRepository.errorMessage(AppError.NotFound(), fallback))
            .isEqualTo("This video is no longer available.")
        assertThat(VideoLibraryRepository.errorMessage(AppError.AuthFailed(), fallback)).isEqualTo(fallback)
    }
}
