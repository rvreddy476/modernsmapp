package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.CreditedUser
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.VideoCredit
import com.us.android.core.model.VideoCredits
import com.us.android.feature.tube.data.hub.normalizeDraft
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

/**
 * The watch page's Credits block (video credits, 2026-10-23, as the server
 * landed): `credits` is ONLY on the post detail, never on a list row, so the
 * block reads them from the detail laid over the row — and draws nothing
 * when there are none.
 */
class WatchCreditsTest {

    private fun video(credits: VideoCredits = VideoCredits.None) = FeedItem(
        id = "v1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Kiran", username = "kiran", avatarMediaId = null),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "2026-10-23T09:30:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(false, false, false),
        isRepostable = false,
        score = null,
        credits = credits,
    )

    private val asha = VideoCredit(
        id = "e1",
        name = "Asha Rao",
        role = "Director",
        user = CreditedUser(id = "u1", username = "asha", displayName = "Asha Rao"),
    )

    /** A list row: the genres, year and house are on every post body; the people are not. */
    private val listRow = video(VideoCredits(genres = listOf("drama"), releaseYear = 2024, productionHouse = "Blue Hour Films"))

    @Test
    fun `a list row draws no Credits block, and the detail's credits bring it`() {
        val detail = video(VideoCredits(credits = listOf(asha), genres = listOf("drama"), releaseYear = 2024))

        assertThat(watchCreditsShown(listRow.credits)).isFalse()
        val merged = listRow.withViewerStateOf(detail)
        assertThat(merged.credits.credits).containsExactly(asha)
        assertThat(watchCreditsShown(merged.credits)).isTrue()
    }

    @Test
    fun `no credits - absent or empty - draws nothing, whatever else the video has`() {
        assertThat(watchCreditsShown(VideoCredits.None)).isFalse()
        assertThat(watchCreditsShown(VideoCredits(credits = emptyList(), genres = listOf("drama")))).isFalse()
        // A detail without credits clears what a stale row may have held.
        val stale = video(VideoCredits(credits = listOf(asha)))
        assertThat(watchCreditsShown(stale.withViewerStateOf(video()).credits)).isFalse()
    }

    /**
     * post-service's `reel_draft_response.json` (copied byte for byte into
     * `src/test/resources/contracts/credits`) through the Creator Hub's drafts
     * normaliser — the only reader of a reel draft on Android. The draft's
     * credits are carried but not shown in the Hub's drafts list.
     */
    @Test
    fun `a long-video reel draft with credits still reads as a draft row`() {
        val raw = File("src/test/resources/contracts/credits/reel_draft_response.json").readText()
        val data = Json.parseToJsonElement(raw).jsonObject.getValue("data")

        val draft = normalizeDraft(data)!!

        assertThat(draft.id).isEqualTo("e6666666-6666-4666-8666-666666666666")
        assertThat(draft.title).isEqualTo("Monsoon Letters")
        assertThat(draft.statusLabel).isEqualTo("Draft")
    }
}
