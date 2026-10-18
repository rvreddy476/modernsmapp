package com.us.android.feature.feed.ui

import androidx.compose.ui.layout.ContentScale
import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedDisclosure
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPoll
import com.us.android.core.model.FeedPollOption
import com.us.android.core.model.FeedViewerState
import org.junit.Test

/**
 * The full-screen Home feed's rules (2026-10-18): which post a page is, its
 * key, its rank, what is warmed ahead of the reader, when coming back starts
 * over, and what each kind of post draws.
 */
class FeedStageRulesTest {

    private fun item(
        id: String,
        media: List<FeedMedia> = emptyList(),
        poll: FeedPoll? = null,
        text: String = "words",
        disclosure: FeedDisclosure = FeedDisclosure(),
    ) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Asha", username = "asha"),
        text = text,
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "2026-10-18T10:00:00Z",
        isPinned = false,
        media = media,
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        poll = poll,
        disclosure = disclosure,
    )

    private val rows = listOf(item("r0"), item("r1"), item("r2"))
    private val head = item("mine")

    @Test
    fun `the head takes page zero and every row moves down one`() {
        assertThat(stageItemAt(0, head, rows.size) { rows[it] }?.id).isEqualTo("mine")
        assertThat(stageItemAt(1, head, rows.size) { rows[it] }?.id).isEqualTo("r0")
        assertThat(stageItemAt(3, head, rows.size) { rows[it] }?.id).isEqualTo("r2")
        assertThat(stageItemAt(4, head, rows.size) { rows[it] }).isNull()
        assertThat(stageItemAt(0, null, rows.size) { rows[it] }?.id).isEqualTo("r0")
        assertThat(stageItemAt(-1, null, rows.size) { rows[it] }).isNull()
    }

    @Test
    fun `the page count has room for the head and for a failed append's retry`() {
        assertThat(stagePageCount(3, hasHead = false, appendFailed = false)).isEqualTo(3)
        assertThat(stagePageCount(3, hasHead = true, appendFailed = false)).isEqualTo(4)
        assertThat(stagePageCount(3, hasHead = true, appendFailed = true)).isEqualTo(5)
        assertThat(pagingIndexOf(4, hasHead = true, itemCount = 3)).isNull()
    }

    @Test
    fun `a page is keyed by its post, the head apart from its own row`() {
        val peek: (Int) -> String? = { rows[it].id }
        assertThat(stagePageKey(0, head, rows.size, peek)).isEqualTo("head_mine")
        assertThat(stagePageKey(1, head, rows.size, peek)).isEqualTo("r0")
        assertThat(stagePageKey(0, null, rows.size, peek)).isEqualTo("r0")
        assertThat(stagePageKey(3, null, rows.size, peek)).isEqualTo("append_retry")
        val keys = (0 until 4).map { stagePageKey(it, head, rows.size, peek) }
        assertThat(keys).containsNoDuplicates()
    }

    @Test
    fun `the rank counts ranked rows from one and gives the head none`() {
        assertThat(rankedPosition(0, hasHead = true, itemCount = 3)).isNull()
        assertThat(rankedPosition(1, hasHead = true, itemCount = 3)).isEqualTo(1)
        assertThat(rankedPosition(0, hasHead = false, itemCount = 3)).isEqualTo(1)
        assertThat(rankedPosition(2, hasHead = false, itemCount = 3)).isEqualTo(3)
    }

    @Test
    fun `three pages ahead and one behind are warmed, in order of need, within range`() {
        assertThat(prefetchPages(settled = 5, pageCount = 20)).containsExactly(6, 7, 8, 4).inOrder()
        assertThat(prefetchPages(settled = 0, pageCount = 20)).containsExactly(1, 2, 3).inOrder()
        assertThat(prefetchPages(settled = 18, pageCount = 20)).containsExactly(19, 17).inOrder()
        assertThat(prefetchPages(settled = 0, pageCount = 1)).isEmpty()
    }

    @Test
    fun `coming back after ten minutes starts over, sooner keeps the place, a first start does neither`() {
        val stopped = 1_000_000L
        assertThat(shouldRefreshOnReturn(stopped, stopped + KEEP_POSITION_MILLIS + 1)).isTrue()
        assertThat(shouldRefreshOnReturn(stopped, stopped + KEEP_POSITION_MILLIS)).isFalse()
        assertThat(shouldRefreshOnReturn(stopped, stopped + 60_000L)).isFalse()
        assertThat(shouldRefreshOnReturn(null, stopped)).isFalse()
        assertThat(KEEP_POSITION_MILLIS).isEqualTo(10 * 60 * 1000L)
    }

    @Test
    fun `each post draws its own kind of page`() {
        val video = FeedMedia(mediaId = "v", kind = "video")
        val photo = FeedMedia(mediaId = "p", kind = "image")
        val poll = FeedPoll(question = "Tea?", allowsMultiple = false, options = emptyList(), totalVotes = 0)
        assertThat(stageKind(item("1", media = listOf(video)))).isEqualTo(StageKind.VIDEO)
        assertThat(stageKind(item("2", media = listOf(photo, photo)))).isEqualTo(StageKind.PHOTO)
        assertThat(stageKind(item("3", poll = poll))).isEqualTo(StageKind.POLL)
        assertThat(stageKind(item("4"))).isEqualTo(StageKind.TEXT)
    }

    @Test
    fun `declared labels show in a fixed order, and none when nothing is declared`() {
        assertThat(disclosureLabels(FeedDisclosure())).isEmpty()
        assertThat(disclosureLabels(FeedDisclosure(paidPromotion = true, isBranded = true, aiGenerated = true)))
            .containsExactly(PAID_PARTNERSHIP, BRAND, AI_GENERATED).inOrder()
        assertThat(disclosureLabels(FeedDisclosure(aiGenerated = true))).containsExactly("AI-generated")
    }

    @Test
    fun `a text or poll page does not repeat its words in the caption`() {
        val text = item("t", text = "hello there")
        assertThat(stageCaption(text).text).isEmpty()
        assertThat(stageCaption(text).handle).isEqualTo("@asha")
        val photo = item("p", media = listOf(FeedMedia(mediaId = "m", kind = "image")), text = "beach")
        assertThat(stageCaption(photo).text).isEqualTo("beach")
        assertThat(stageCaption(item("d", disclosure = FeedDisclosure(paidPromotion = true))).labels)
            .containsExactly(PAID_PARTNERSHIP)
    }

    @Test
    fun `a video is cropped only when it is at least as wide as the screen`() {
        val screen = 9f / 19.5f
        assertThat(stageVideoScale(9f / 16f, screen)).isEqualTo(ContentScale.Crop)
        assertThat(stageVideoScale(16f / 9f, screen)).isEqualTo(ContentScale.Fit)
        // Narrower than the screen: cropping would run into the next page.
        assertThat(stageVideoScale(0.4f, screen)).isEqualTo(ContentScale.Fit)
        assertThat(stageVideoScale(0f, screen)).isEqualTo(ContentScale.Fit)
    }

    @Test
    fun `a vote cast this session is layered over the server's counts once`() {
        val poll = FeedPoll(
            question = "Tea?",
            allowsMultiple = false,
            options = listOf(FeedPollOption("y", "Yes", 3, 75.0), FeedPollOption("n", "No", 1, 25.0)),
            totalVotes = 4,
        )
        val local = poll.toCardPoll(setOf("n"))
        assertThat(local.totalVotes).isEqualTo(5)
        assertThat(local.options.first { it.id == "n" }.voteCount).isEqualTo(2)
        // The server already counted it: nothing is added twice.
        val counted = poll.copy(viewerVotedOptionIds = listOf("n")).toCardPoll(setOf("n"))
        assertThat(counted.totalVotes).isEqualTo(4)
    }
}
