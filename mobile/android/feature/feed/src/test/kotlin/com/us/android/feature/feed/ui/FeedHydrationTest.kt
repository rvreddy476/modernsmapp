package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.followGraph
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Which server values are allowed to retire a confirmed optimistic action.
 *
 * WHY THIS FILE EXISTS
 *
 * Reconciliation used to run over the WHOLE paging snapshot whenever the
 * append state or item count changed. A paging snapshot is cumulative: after
 * page two arrives it still contains page one's original rows, captured before
 * the viewer touched anything. Feeding those back in looked like fresh server
 * authority and retired the overlay, so this happened:
 *
 *   1. page one row says has_reacted = false
 *   2. viewer likes it; the server acknowledges; the overlay settles at true
 *   3. viewer scrolls, page two appends
 *   4. the stale page-one row is reconciled and the like visibly reverts,
 *      while the server still holds the reaction
 *
 * A refresh is different: those rows really were just fetched, so they are
 * authority and must win — including when they disagree, which is how a like
 * made on another device or removed by moderation reaches this client.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeedHydrationTest {

    private val postId = "p1"

    /** Accepts every write immediately; ordering is not what this file tests. */
    private class AcceptingWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) =
            AppResult.Success(Unit)

        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    private fun item(id: String, reacted: Boolean, likes: Int) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "A"),
        text = "t",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "2026-08-21T00:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = likes, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = reacted, hasReposted = false),
        isRepostable = true,
    )

    private val json = Json { ignoreUnknownKeys = true }

    private fun viewModel(store: EngagementStore): FeedViewModel {
        val config = ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
        return FeedViewModel(
            mode = FeedMode.Home,
            repository = FeedRepository(UnusedFeedApi(), ErrorMapper(json)) { it },
            urlResolver = MediaUrlResolver(config),
            engagement = store,
            shares = EngagementRepository(UnusedEngagementApi(), ErrorMapper(json)),
            tabState = FeedTabState(),
            follows = followGraph(),
            hidden = HiddenPosts(),
        )
    }

    /**
     * PROOF 2a — an append must not reprocess a retained page-one row.
     */
    @Test
    fun `append does not retire a confirmed overlay using a stale snapshot row`() = runTest {
        val store = EngagementStore(AcceptingWrites())
        val viewModel = viewModel(store)

        // Page one, as loaded: not reacted.
        val pageOne = listOf(item(postId, reacted = false, likes = 3))
        viewModel.onRefreshHydrated(pageOne)

        // The viewer likes it and the server acknowledges.
        launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()

        // Page two appends. The snapshot still carries page one's ORIGINAL
        // row, which still says has_reacted = false.
        val afterAppend = pageOne + item("p2", reacted = false, likes = 0)
        viewModel.onAppendHydrated(afterAppend)

        // The like must survive.
        assertThat(store.overlayFor(postId).reactedOr(false)).isTrue()
    }

    /**
     * PROOF 2b — a genuinely fresh refresh generation still wins, even when it
     * disagrees. This is how another device's removal reaches this client.
     */
    @Test
    fun `a refresh generation retires the overlay and shows server truth`() = runTest {
        val store = EngagementStore(AcceptingWrites())
        val viewModel = viewModel(store)

        viewModel.onRefreshHydrated(listOf(item(postId, reacted = false, likes = 3)))

        launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        assertThat(store.overlayFor(postId).reacted).isTrue()

        // A real refresh, and the server disagrees — the reaction is gone.
        viewModel.onRefreshHydrated(listOf(item(postId, reacted = false, likes = 3)))

        assertThat(store.overlays.value).doesNotContainKey(postId)
        assertThat(store.overlayFor(postId).reactedOr(false)).isFalse()
    }

    /**
     * NEGATIVE CONTROL for proof 2 — the same stale row, sent through the
     * REFRESH path instead of the append path, does retire the overlay.
     *
     * This is what the old code did to every row on every append. If this ever
     * stops retiring, proof 2a has stopped distinguishing the two paths and is
     * no longer evidence of anything.
     */
    @Test
    fun `the same stale row through the refresh path does retire the overlay`() = runTest {
        val store = EngagementStore(AcceptingWrites())
        val viewModel = viewModel(store)

        val pageOne = listOf(item(postId, reacted = false, likes = 3))
        viewModel.onRefreshHydrated(pageOne)

        launch { store.toggleReaction(postId, serverReacted = false) }
        runCurrent()
        assertThat(store.overlayFor(postId).reacted).isTrue()

        viewModel.onRefreshHydrated(pageOne)

        assertThat(store.overlays.value).doesNotContainKey(postId)
    }

    /** A row appended for the first time IS new authority and is reconciled. */
    @Test
    fun `a genuinely new appended row is reconciled`() = runTest {
        val store = EngagementStore(AcceptingWrites())
        val viewModel = viewModel(store)

        viewModel.onRefreshHydrated(listOf(item(postId, reacted = false, likes = 3)))

        // Never seen before, and the server says it is already reacted.
        launch { store.toggleReaction("p2", serverReacted = false) }
        runCurrent()
        assertThat(store.overlayFor("p2").reacted).isTrue()

        viewModel.onAppendHydrated(
            listOf(item(postId, reacted = false, likes = 3), item("p2", reacted = true, likes = 1)),
        )

        // p2 was reconciled: settled overlay agreeing with the server retires.
        assertThat(store.overlays.value).doesNotContainKey("p2")
    }
}

/**
 * Neither API is exercised: this file drives the ViewModel's hydration entry
 * points directly, so any real call means the test is not measuring what it
 * claims to.
 */
private class UnusedFeedApi : com.us.android.feature.feed.data.FeedApi {
    override suspend fun getFeed(
        surface: String,
        limit: Int,
        cursor: String?,
        followingOnly: Boolean?,
        circleOnly: Boolean?,
    ): Nothing = error("feed loading is not under test")

    override suspend fun getTrendingHashtags(limit: Int): Nothing = error("trending is not under test")

    override suspend fun getPostsByHashtag(tag: String, limit: Int, cursor: String?, sort: String): Nothing =
        error("hashtag pages are not under test")

    override suspend fun getPost(postId: String): Nothing = error("single posts are not under test")

    override suspend fun getDelta(feedType: String, anchor: String, limit: Int): Nothing =
        error("feed delta is not under test")

    override suspend fun votePoll(
        postId: String,
        body: com.us.android.feature.feed.data.PollVoteRequest,
    ): Nothing = error("poll voting is not under test")

    override suspend fun feedback(body: com.us.android.feature.feed.data.FeedFeedbackRequest): Nothing =
        error("feedback is not under test")
}

private class UnusedEngagementApi : com.us.android.core.engagement.data.EngagementApi {
    override suspend fun addReaction(
        postId: String,
        body: com.us.android.core.engagement.data.ReactionRequest,
    ): Nothing = error("writes go through EngagementWrites in this test")

    override suspend fun removeReaction(postId: String): Nothing = error("unused")
    override suspend fun addBookmark(postId: String): Nothing = error("unused")
    override suspend fun removeBookmark(postId: String): Nothing = error("unused")
    override suspend fun repost(
        postId: String,
        body: com.us.android.core.engagement.data.RepostRequest,
    ): Nothing = error("unused")

    override suspend fun removeRepost(postId: String): Nothing = error("unused")
    override suspend fun share(
        postId: String,
        body: com.us.android.core.engagement.data.ShareRequest,
    ): Nothing = error("unused")

    override suspend fun getComments(postId: String, limit: Int, cursor: String?): Nothing =
        error("unused")

    override suspend fun addComment(
        postId: String,
        idempotencyKey: String,
        body: com.us.android.core.engagement.data.CreateCommentRequest,
    ): Nothing = error("unused")
}
