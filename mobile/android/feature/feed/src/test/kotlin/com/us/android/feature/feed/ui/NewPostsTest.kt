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
import com.us.android.core.feed.data.PollVoteRequest
import com.us.android.core.feed.data.dto.FeedDeltaDto
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
import com.us.android.feature.feed.data.followGraph
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

private const val OPENED_AT = "2026-10-13T07:00Z"

/**
 * The Following tab's dot (2026-10-18; it replaced For You's "N new posts"
 * pill). The anchor is the newest `created_at` Following's last refresh
 * loaded, or the moment Home opened until it has loaded; the delta endpoint
 * counts from it; the dot is never lit while Following is on screen.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class NewPostsTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /** Answers every delta request with [answer] and records what was asked. */
    private class DeltaApi(var answer: FeedDeltaDto) : FeedApi {
        val asked = mutableListOf<Pair<String, String>>()

        override suspend fun getDelta(feedType: String, anchor: String, limit: Int): ApiEnvelope<FeedDeltaDto> {
            asked += feedType to anchor
            return ApiEnvelope(data = answer, meta = null)
        }

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

        override suspend fun getPost(postId: String): Nothing = error("unused")
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

    private fun item(id: String, createdAt: String) = FeedItem(
        id = id,
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "A"),
        text = "t",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = createdAt,
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    private fun viewModel(api: FeedApi, mode: FeedMode = FeedMode.Home, tabs: FeedTabState = FeedTabState()): FeedViewModel {
        val config = ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
        return FeedViewModel(
            mode = mode,
            repository = FeedRepository(api, ErrorMapper(json)) { it },
            urlResolver = MediaUrlResolver(config),
            engagement = EngagementStore(NoWrites()),
            shares = EngagementRepository(NoEngagementApi(), ErrorMapper(json)),
            tabState = tabs,
            follows = followGraph(),
            feedEntry = FeedEntry(),
            watchTracker = VideoWatchTracker.disabled(),
            analytics = NoOpAnalyticsRecorder,
            hidden = HiddenPosts(),
        ).also { it.openedAt = OPENED_AT }
    }

    private val loaded = listOf(
        item("old", "2026-10-13T08:00:00Z"),
        item("newest", "2026-10-13T09:30:00.250+05:30"), // 04:00:00.25Z: NOT the newest
        item("mid", "2026-10-13T09:00:00Z"),
    )

    @Test
    fun `the anchor is the newest created_at, compared as instants, sent verbatim`() {
        assertThat(newestCreatedAt(loaded)).isEqualTo("2026-10-13T09:00:00Z")
        assertThat(newestCreatedAt(listOf(item("x", "not a time")))).isNull()
        assertThat(newestCreatedAt(emptyList())).isNull()
    }

    @Test
    fun `the dot asks for the following delta from the moment home opened, until following loads`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 3))
        val vm = viewModel(api)

        vm.checkFollowing()

        assertThat(api.asked).containsExactly(FeedApi.DELTA_FOLLOWING to OPENED_AT)
        assertThat(vm.followingDot.value).isTrue()
    }

    @Test
    fun `once following has loaded, its newest post is the anchor`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 0))
        val tabs = FeedTabState()
        val vm = viewModel(api, tabs = tabs)

        tabs.select(FeedTab.FOLLOWING)
        vm.onRefreshHydrated(loaded)
        tabs.select(FeedTab.FOR_YOU)
        vm.checkFollowing()

        assertThat(api.asked).containsExactly(FeedApi.DELTA_FOLLOWING to "2026-10-13T09:00:00Z")
        assertThat(vm.followingDot.value).isFalse()
    }

    @Test
    fun `a for you refresh does not move the following anchor`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 1))
        val vm = viewModel(api)

        vm.onRefreshHydrated(listOf(item("fresh", "2026-10-13T10:00:00Z")))
        vm.checkFollowing()

        assertThat(api.asked.single().second).isEqualTo(OPENED_AT)
    }

    @Test
    fun `the dot never lights while following is on screen, and picking following puts it out`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 4))
        val vm = viewModel(api)

        vm.checkFollowing()
        assertThat(vm.followingDot.value).isTrue()

        // Picking Following with the dot lit starts it over, and says so.
        assertThat(vm.selectTab(FeedTab.FOLLOWING)).isTrue()
        assertThat(vm.followingDot.value).isFalse()

        vm.checkFollowing()
        assertThat(api.asked).hasSize(1)
        assertThat(vm.followingDot.value).isFalse()

        // Without a dot, picking Following is only a tab switch.
        vm.selectTab(FeedTab.FOR_YOU)
        assertThat(vm.selectTab(FeedTab.FOLLOWING)).isFalse()
    }

    @Test
    fun `nothing new puts the dot out`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 2))
        val vm = viewModel(api)
        vm.checkFollowing()
        assertThat(vm.followingDot.value).isTrue()

        api.answer = FeedDeltaDto(newCount = 0)
        vm.checkFollowing()
        assertThat(vm.followingDot.value).isFalse()
    }

    @Test
    fun `friends has no dot and never asks`() = runTest {
        val api = DeltaApi(FeedDeltaDto(newCount = 9))
        val vm = viewModel(api, mode = FeedMode.Friends)

        vm.watchFollowing() // returns at once off the home timeline
        vm.checkFollowing()

        assertThat(api.asked).isEmpty()
        assertThat(vm.followingDot.value).isFalse()
    }

    @Test
    fun `the dot polls every minute`() {
        assertThat(FOLLOWING_POLL_MILLIS).isEqualTo(60_000L)
    }

    private class NoEngagementApi : com.us.android.core.engagement.data.EngagementApi {
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
}
