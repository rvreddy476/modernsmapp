package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.analytics.NoOpAnalyticsRecorder
import com.us.android.core.analytics.VideoWatchTracker
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedFeedbackRequest
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.PollVoteRequest
import com.us.android.core.feed.data.PostEditPatch
import com.us.android.core.feed.data.PostEdits
import com.us.android.core.feed.data.dto.FeedAuthorDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.media.FeedEntry
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feed.data.RecordingGraphApi
import com.us.android.feature.feed.data.followGraph
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * The post Home pins at the top after publishing (audit 2026-10-30, A1)
 * goes through the same hides, deletes, blocks and edits as every paged row;
 * and an appended page asks the follow graph only about its own new rows
 * (A4).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeedPinnedHeadTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** Serves the one just-published post by id; nothing else is under test. */
    private class OnePostApi(private val post: FeedItemDto) : FeedApi {
        override suspend fun getPost(postId: String): ApiEnvelope<FeedItemDto> =
            ApiEnvelope(data = post.takeIf { it.id == postId } ?: error("no post $postId"), meta = null)

        override suspend fun getFeed(
            surface: String,
            limit: Int,
            cursor: String?,
            followingOnly: Boolean?,
            circleOnly: Boolean?,
            feedMode: String?,
        ): Nothing = error("feed loading is not under test")

        override suspend fun getTrendingHashtags(limit: Int): Nothing = error("unused")
        override suspend fun getPostsByHashtag(tag: String, limit: Int, cursor: String?, sort: String): Nothing =
            error("unused")
        override suspend fun getFollowedHashtagPosts(limit: Int, cursor: String?): Nothing = error("unused")
        override suspend fun getDelta(feedType: String, anchor: String, limit: Int): Nothing = error("unused")
        override suspend fun votePoll(postId: String, body: PollVoteRequest): Nothing = error("unused")
        override suspend fun feedback(body: FeedFeedbackRequest): Nothing = error("unused")
    }

    private class NoWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)
        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    private val hidden = HiddenPosts()
    private val edits = PostEdits()

    private fun viewModel(
        published: String = "mine",
        authorId: String = "me",
        follows: FollowGraph = followGraph(),
    ): FeedViewModel {
        val config = ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
        val post = FeedItemDto(
            id = published,
            authorId = authorId,
            author = FeedAuthorDto(id = authorId),
            text = "old words",
        )
        return FeedViewModel(
            mode = FeedMode.Home,
            repository = FeedRepository(OnePostApi(post), ErrorMapper(json)) { it },
            urlResolver = MediaUrlResolver(config),
            engagement = EngagementStore(NoWrites()),
            shares = EngagementRepository(NoShareApi(), ErrorMapper(json)),
            tabState = FeedTabState(),
            follows = follows,
            // The publish just finished: Home opens with this post first.
            feedEntry = FeedEntry().apply { showFirst(published) },
            watchTracker = VideoWatchTracker.disabled(),
            analytics = NoOpAnalyticsRecorder,
            hidden = hidden,
            edits = edits,
        )
    }

    private fun TestScope.watched(vm: FeedViewModel): FeedViewModel {
        backgroundScope.launch { vm.head.collect {} }
        runCurrent()
        return vm
    }

    @Test
    fun `the just-published post is pinned`() = runTest {
        val vm = watched(viewModel())

        assertThat(vm.head.value?.id).isEqualTo("mine")
    }

    /** Delete from the More sheet hides the post through HiddenPosts; the pinned copy must go too. */
    @Test
    fun `deleting the pinned post takes it off the top`() = runTest {
        val vm = watched(viewModel())

        hidden.hidePost("mine")
        runCurrent()

        assertThat(vm.head.value).isNull()
    }

    @Test
    fun `restoring the deleted post brings the pin back`() = runTest {
        val vm = watched(viewModel())
        hidden.hidePost("mine")
        runCurrent()

        hidden.unhidePost("mine")
        runCurrent()

        assertThat(vm.head.value?.id).isEqualTo("mine")
    }

    @Test
    fun `blocking the pinned post's author takes it off the top`() = runTest {
        val vm = watched(viewModel(published = "theirs", authorId = "ada"))

        hidden.hideAuthor("ada")
        runCurrent()

        assertThat(vm.head.value).isNull()
    }

    @Test
    fun `an edit to the pinned post shows at once`() = runTest {
        val vm = watched(viewModel())

        edits.record("mine", PostEditPatch(text = "new words"))
        runCurrent()

        assertThat(vm.head.value?.text).isEqualTo("new words")
    }

    @Test
    fun `hiding another post leaves the pin alone`() = runTest {
        val vm = watched(viewModel())

        hidden.hidePost("someone-else")
        runCurrent()

        assertThat(vm.head.value?.id).isEqualTo("mine")
    }

    // ── A4: follow lookups per page ──────────────────────────────────

    private fun row(id: String, author: String) = FeedItem(
        id = id,
        authorId = author,
        author = FeedAuthor(id = author, displayName = author),
        text = "t",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "2026-10-30T00:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    /**
     * Page one's author fails to look up. Once the back-off has passed, an
     * append — whose snapshot still carries page one — must ask only about
     * page two's author: the retained rows are not new.
     */
    @Test
    fun `an append asks the graph only about the new rows' authors`() = runTest {
        val api = RecordingGraphApi().apply { failingRelationships += "gone" }
        var clock = 0L
        val graph = followGraph(api).also { it.now = { clock } }
        val vm = viewModel(follows = graph)
        val pageOne = listOf(row("p1", author = "gone"))
        vm.onRefreshHydrated(pageOne)
        runCurrent()
        api.relationshipRequests.clear()
        clock += BACKOFF_PASSED

        vm.onAppendHydrated(pageOne + row("p2", author = "ada"))
        runCurrent()

        assertThat(api.relationshipRequests).containsExactly("me" to "ada")
    }

    @Test
    fun `an append with no new rows asks the graph nothing`() = runTest {
        val api = RecordingGraphApi().apply { failingRelationships += "gone" }
        var clock = 0L
        val graph = followGraph(api).also { it.now = { clock } }
        val vm = viewModel(follows = graph)
        val pageOne = listOf(row("p1", author = "gone"))
        vm.onRefreshHydrated(pageOne)
        runCurrent()
        api.relationshipRequests.clear()
        clock += BACKOFF_PASSED

        vm.onAppendHydrated(pageOne)
        runCurrent()

        assertThat(api.relationshipRequests).isEmpty()
    }

    private companion object {
        /** Longer than any back-off the graph applies (ten minutes at most). */
        const val BACKOFF_PASSED = 60L * 60L * 1000L
    }
}

/** Shares are not under test; any call means the test reached the wrong thing. */
private class NoShareApi : com.us.android.core.engagement.data.EngagementApi {
    override suspend fun addReaction(
        postId: String,
        body: com.us.android.core.engagement.data.ReactionRequest,
    ): Nothing = error("unused")

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

    override suspend fun getComments(postId: String, limit: Int, cursor: String?): Nothing = error("unused")
    override suspend fun addComment(
        postId: String,
        idempotencyKey: String,
        body: com.us.android.core.engagement.data.CreateCommentRequest,
    ): Nothing = error("unused")
}
