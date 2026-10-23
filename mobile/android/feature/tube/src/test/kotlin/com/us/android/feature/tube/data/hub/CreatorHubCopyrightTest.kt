package com.us.android.feature.tube.data.hub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightReviewState
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File
import java.time.ZoneOffset

/**
 * The Creator Hub's uploads with copyright title review (2026-10-24). The
 * fixture is the same one `:core:feed` decodes (contracts/copyright/
 * uploads_videos.json), written from post-service my_uploads.go:36-83 and
 * :399 (flags, restrictions, the author's copyright_review) and
 * copyright_title_review.go:651-658 (the review's shape). Row 1 is pending
 * and already requested, row 2 blocked, row 3 cleared, row 4 hidden by a
 * copyright CASE (no title review), row 5 untouched.
 */
class CreatorHubCopyrightTest {

    private val rows = (
        Json.parseToJsonElement(
            File("../../core/feed/src/test/resources/contracts/copyright/uploads_videos.json").readText(),
        ).jsonObject["data"] as JsonArray
        ).mapNotNull(::normalizeLibraryRow)

    @Test
    fun `every row keeps its review, its hold flag and its title-review restriction`() {
        assertThat(rows.map { it.copyrightReview?.state }).containsExactly(
            CopyrightReviewState.PENDING,
            CopyrightReviewState.BLOCKED,
            CopyrightReviewState.CLEARED,
            null,
            null,
        ).inOrder()
        assertThat(rows.map { it.copyrightHold }).containsExactly(true, true, false, true, false).inOrder()
        assertThat(rows.map { it.titleReviewHold }).containsExactly(true, true, false, false, false).inOrder()
    }

    @Test
    fun `held is a holding review, or a hold with no review to show`() {
        assertThat(rows.map { it.heldForCopyright }).containsExactly(true, true, false, true, false).inOrder()
        // A cleared review wins over a stale flag: the review is the more specific answer.
        val cleared = rows[2].copy(copyrightHold = true)
        assertThat(cleared.heldForCopyright).isFalse()
    }

    @Test
    fun `the rows say the contract's words`() {
        val utc = ZoneOffset.UTC
        val pending = CopyrightCopy.notice(rows[0].copyrightReview!!, utc)
        assertThat(pending.message).isEqualTo(
            "In copyright review — this video is hidden until our team checks it. It matched “Kalki 2898 AD”.",
        )
        assertThat(pending.requested).isEqualTo("Review requested 24 Oct 2026")
        assertThat(pending.offersRequest).isFalse()
        assertThat(CopyrightCopy.notice(rows[1].copyrightReview!!, utc).message).startsWith(
            "Blocked for copyright — it matched “Pushpa 2”.",
        )
        assertThat(CopyrightCopy.notice(rows[2].copyrightReview!!, utc).message).isEqualTo("Cleared — your video is live")
    }

    @Test
    fun `a parked draft's copyright reason is said in words`() {
        val draft = normalizeDraft(
            Json.parseToJsonElement(
                """{"id":"d1","title":"Part 2","status":"blocked","blocked_reason":"uploads_paused until 2026-10-31T08:00:00Z"}""",
            ),
        )!!
        assertThat(draft.statusLabel).isEqualTo("Held")
        assertThat(CopyrightCopy.blockedReason(draft.blockedReason, ZoneOffset.UTC)).isEqualTo(
            "Long-video uploads are paused until 31 Oct 2026 after repeated copyright blocks. Reels and posts still work.",
        )
    }
}
