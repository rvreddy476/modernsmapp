package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.ZoneId
import java.time.ZoneOffset

/**
 * The copyright title review's words (founder quick wins, 2026-10-24): every
 * sentence the hub, the Edit sheet, the watch screen and the publish tile
 * show comes from [CopyrightCopy], in the contract's words. The states are
 * post-service's (store/postgres/copyright_title_review.go:40-45); the pause
 * and its `until` are standing_errors.go:53-55; the parked reasons are
 * copyright_title_review.go:64-75; the note limit is copyright_title_review.go:709.
 */
class CopyrightReviewTest {

    private val utc: ZoneId = ZoneOffset.UTC

    private fun review(
        state: CopyrightReviewState,
        title: String = "Kalki 2898 AD",
        requested: String? = null,
    ) = CopyrightReview(state, title, createdAt = "2026-10-24T08:00:01Z", reviewRequestedAt = requested)

    @Test
    fun `the three wire states read, and anything else is not a review`() {
        assertThat(CopyrightReviewState.fromWire("pending")).isEqualTo(CopyrightReviewState.PENDING)
        assertThat(CopyrightReviewState.fromWire(" Cleared ")).isEqualTo(CopyrightReviewState.CLEARED)
        assertThat(CopyrightReviewState.fromWire("blocked")).isEqualTo(CopyrightReviewState.BLOCKED)
        assertThat(CopyrightReviewState.fromWire("held")).isNull()
        assertThat(CopyrightReviewState.fromWire("")).isNull()
        assertThat(CopyrightReviewState.fromWire(null)).isNull()
    }

    @Test
    fun `pending says it is hidden and what it matched, and offers Request review`() {
        val notice = CopyrightCopy.notice(review(CopyrightReviewState.PENDING), utc)
        assertThat(notice.message).isEqualTo(
            "In copyright review — this video is hidden until our team checks it. It matched “Kalki 2898 AD”.",
        )
        assertThat(notice.offersRequest).isTrue()
        assertThat(notice.requested).isNull()
        assertThat(notice.state).isEqualTo(CopyrightReviewState.PENDING)
    }

    @Test
    fun `once requested, pending says when, and Request review is gone`() {
        val notice = CopyrightCopy.notice(review(CopyrightReviewState.PENDING, requested = "2026-10-24T21:30:00Z"), utc)
        assertThat(notice.requested).isEqualTo("Review requested 24 Oct 2026")
        assertThat(notice.offersRequest).isFalse()
        // The day is the viewer's: 21:30 UTC is already the 25th in India.
        val ist = CopyrightCopy.notice(
            review(CopyrightReviewState.PENDING, requested = "2026-10-24T21:30:00Z"),
            ZoneId.of("Asia/Kolkata"),
        )
        assertThat(ist.requested).isEqualTo("Review requested 25 Oct 2026")
        // An unreadable instant still says it was asked.
        val odd = CopyrightCopy.notice(review(CopyrightReviewState.PENDING, requested = "yesterday"), utc)
        assertThat(odd.requested).isEqualTo("Review requested")
    }

    @Test
    fun `cleared says it is live, and offers nothing`() {
        val notice = CopyrightCopy.notice(review(CopyrightReviewState.CLEARED, requested = "2026-10-24T09:00:00Z"), utc)
        assertThat(notice.message).isEqualTo("Cleared — your video is live")
        assertThat(notice.offersRequest).isFalse()
        assertThat(notice.requested).isNull()
    }

    @Test
    fun `blocked names the match and says it is not a strike`() {
        val notice = CopyrightCopy.notice(review(CopyrightReviewState.BLOCKED), utc)
        assertThat(notice.message).isEqualTo(
            "Blocked for copyright — it matched “Kalki 2898 AD”. " +
                "This isn't a strike, but repeated blocks pause long-video uploads.",
        )
        assertThat(notice.offersRequest).isFalse()
    }

    @Test
    fun `a review with no matched title leaves the quote out rather than printing empty quotes`() {
        assertThat(CopyrightCopy.notice(review(CopyrightReviewState.PENDING, title = " "), utc).message)
            .isEqualTo("In copyright review — this video is hidden until our team checks it.")
        assertThat(CopyrightCopy.notice(review(CopyrightReviewState.BLOCKED, title = ""), utc).message)
            .isEqualTo("Blocked for copyright. This isn't a strike, but repeated blocks pause long-video uploads.")
    }

    @Test
    fun `held is pending or blocked, and only an unanswered pending review may be requested`() {
        assertThat(review(CopyrightReviewState.PENDING).isHeld).isTrue()
        assertThat(review(CopyrightReviewState.BLOCKED).isHeld).isTrue()
        assertThat(review(CopyrightReviewState.CLEARED).isHeld).isFalse()
        assertThat(review(CopyrightReviewState.PENDING).canRequestReview).isTrue()
        assertThat(review(CopyrightReviewState.PENDING, requested = "2026-10-24T09:00:00Z").canRequestReview).isFalse()
        assertThat(review(CopyrightReviewState.BLOCKED).canRequestReview).isFalse()
    }

    @Test
    fun `the short labels`() {
        assertThat(CopyrightCopy.label(review(CopyrightReviewState.PENDING))).isEqualTo("In copyright review")
        assertThat(CopyrightCopy.label(review(CopyrightReviewState.CLEARED))).isEqualTo("Cleared")
        assertThat(CopyrightCopy.label(review(CopyrightReviewState.BLOCKED))).isEqualTo("Blocked for copyright")
    }

    @Test
    fun `the upload pause says until when, and still reads without a date`() {
        assertThat(CopyrightCopy.uploadsPaused("2026-10-31T08:00:00Z", utc)).isEqualTo(
            "Long-video uploads are paused until 31 Oct 2026 after repeated copyright blocks. Reels and posts still work.",
        )
        assertThat(CopyrightCopy.uploadsPaused(null, utc)).isEqualTo(
            "Long-video uploads are paused after repeated copyright blocks. Reels and posts still work.",
        )
        assertThat(CopyrightCopy.uploadsPaused("next week", utc)).isEqualTo(
            "Long-video uploads are paused after repeated copyright blocks. Reels and posts still work.",
        )
    }

    @Test
    fun `a parked draft's reason reads in words, and any other reason is left alone`() {
        assertThat(CopyrightCopy.blockedReason("uploads_paused until 2026-10-31T08:00:00Z", utc))
            .isEqualTo(CopyrightCopy.uploadsPaused("2026-10-31T08:00:00Z", utc))
        assertThat(CopyrightCopy.blockedReason("uploads_paused", utc)).isEqualTo(CopyrightCopy.uploadsPaused(null, utc))
        assertThat(CopyrightCopy.blockedReason("copyright_review", utc)).isEqualTo(CopyrightCopy.MEMBERS_ONLY_HELD)
        assertThat(CopyrightCopy.blockedReason("author_suspended", utc)).isNull()
        assertThat(CopyrightCopy.blockedReason(null, utc)).isNull()
    }

    @Test
    fun `a note is 1 to 1000 characters once trimmed, counted as the server counts them`() {
        assertThat(CopyrightCopy.noteProblem("")).isEqualTo("Tell us why you have the rights to this video.")
        assertThat(CopyrightCopy.noteProblem("   \n ")).isEqualTo("Tell us why you have the rights to this video.")
        assertThat(CopyrightCopy.noteProblem("I own the rights")).isNull()
        assertThat(CopyrightCopy.noteProblem("a".repeat(1000))).isNull()
        assertThat(CopyrightCopy.noteProblem("  " + "a".repeat(1000) + "  ")).isNull()
        assertThat(CopyrightCopy.noteProblem("a".repeat(1001))).isEqualTo("Keep it to 1000 characters.")
        // Go counts runes: 1000 emoji are 2000 UTF-16 units and still fit.
        val emoji = "🎬"
        assertThat(CopyrightCopy.noteProblem(emoji.repeat(1000))).isNull()
        assertThat(CopyrightCopy.noteProblem(emoji.repeat(1001))).isEqualTo("Keep it to 1000 characters.")
    }
}
