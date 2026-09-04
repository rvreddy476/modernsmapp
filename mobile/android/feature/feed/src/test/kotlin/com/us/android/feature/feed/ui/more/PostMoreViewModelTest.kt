package com.us.android.feature.feed.ui.more

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.CreateCommentRequest
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.FileReportRequest
import com.us.android.core.engagement.data.ReactionRequest
import com.us.android.core.engagement.data.ReportApi
import com.us.android.core.engagement.data.ReportDto
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.engagement.data.RepostRequest
import com.us.android.core.engagement.data.ShareRequest
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.FollowStatus
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.ui.UsPostMoreFollowRow
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReportReason
import com.us.android.feature.feed.data.FeedApi
import com.us.android.feature.feed.data.FeedFeedbackDto
import com.us.android.feature.feed.data.FeedFeedbackRequest
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.HiddenPosts
import com.us.android.feature.feed.data.PollVoteRequest
import com.us.android.feature.feed.data.RecordingGraphApi
import com.us.android.feature.feed.data.dto.FeedAuthorDto
import com.us.android.feature.feed.data.dto.FeedItemDto
import com.us.android.feature.feed.data.followGraph
import com.us.android.feature.feed.ui.FeedMode
import com.us.android.feature.feed.ui.FeedTabState
import com.us.android.feature.feed.ui.FeedViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * What the post "more" sheet's rows do — the rules that would be invisible
 * from the sheet's source:
 *
 *  - "Not interested" removes the post from the list AT ONCE and tells the
 *    server; a refusal puts it back.
 *  - Block removes every post by the author from the list and sends the
 *    block; "Interested" is the undo of an earlier "Not interested".
 *  - A report carries the chosen reason's wire token, and a 409 reads as
 *    "already reported", not as a failure.
 *  - Own post / follow edge → which rows the sheet is built with.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PostMoreViewModelTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }
    private val mapper = ErrorMapper(json)

    /** Serves one home page and records feedback. */
    private class FakeFeedApi(private val page: List<FeedItemDto>) : FeedApi {
        val feedback = mutableListOf<FeedFeedbackRequest>()
        var feedbackFails = false

        override suspend fun getFeed(
            surface: String,
            limit: Int,
            cursor: String?,
            followingOnly: Boolean?,
            circleOnly: Boolean?,
        ): ApiEnvelope<List<FeedItemDto>> = ApiEnvelope(data = page, meta = null)

        override suspend fun feedback(body: FeedFeedbackRequest): ApiEnvelope<FeedFeedbackDto> {
            feedback += body
            if (feedbackFails) throw java.io.IOException("offline")
            return ApiEnvelope(data = FeedFeedbackDto(body.postId, body.signal), meta = null)
        }

        override suspend fun getDelta(feedType: String, anchor: String, limit: Int): Nothing = error("unused")
        override suspend fun votePoll(postId: String, body: PollVoteRequest): Nothing = error("unused")
        override suspend fun getTrendingHashtags(limit: Int): Nothing = error("unused")
        override suspend fun getPostsByHashtag(tag: String, limit: Int, cursor: String?, sort: String): Nothing =
            error("unused")

        override suspend fun getPost(postId: String): Nothing = error("unused")
    }

    private class FakeReportApi : ReportApi {
        val reports = mutableListOf<FileReportRequest>()
        var alreadyReported = false

        override suspend fun fileReport(body: FileReportRequest): ApiEnvelope<ReportDto> {
            reports += body
            if (alreadyReported) {
                throw HttpException(
                    Response.error<Any>(
                        409,
                        """{"error":{"code":"ACTIVE_REPORT_EXISTS","message":"already"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
            return ApiEnvelope(data = ReportDto(id = "r1", status = "open"), meta = null)
        }
    }

    private class AcceptingWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)
        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    private class UnusedEngagementApi : EngagementApi {
        override suspend fun addReaction(postId: String, body: ReactionRequest): Nothing = error("unused")
        override suspend fun removeReaction(postId: String): Nothing = error("unused")
        override suspend fun addBookmark(postId: String): Nothing = error("unused")
        override suspend fun removeBookmark(postId: String): Nothing = error("unused")
        override suspend fun repost(postId: String, body: RepostRequest): Nothing = error("unused")
        override suspend fun removeRepost(postId: String): Nothing = error("unused")
        override suspend fun share(postId: String, body: ShareRequest): Nothing = error("unused")
        override suspend fun getComments(postId: String, limit: Int, cursor: String?): Nothing = error("unused")
        override suspend fun addComment(postId: String, idempotencyKey: String, body: CreateCommentRequest): Nothing =
            error("unused")
    }

    private fun dto(id: String, author: String) = FeedItemDto(
        id = id,
        authorId = author,
        author = FeedAuthorDto(id = author, displayName = author.uppercase(), username = author),
        postType = "text",
        text = "post $id",
    )

    private fun item(id: String, author: String, reason: String = "") = FeedItem(
        id = id,
        authorId = author,
        author = FeedAuthor(id = author, displayName = author.uppercase(), username = author),
        text = "post $id",
        visibility = "public",
        feedContentType = "post",
        postType = "text",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        reasonText = reason,
    )

    private class Harness(page: List<FeedItemDto>) {
        val feedApi = FakeFeedApi(page)
        val reportApi = FakeReportApi()
        val graph = RecordingGraphApi()
        val hidden = HiddenPosts()
    }

    private fun viewModel(h: Harness): PostMoreViewModel {
        val mapper = ErrorMapper(json)
        return PostMoreViewModel(
            engagement = EngagementStore(AcceptingWrites()),
            shares = EngagementRepository(UnusedEngagementApi(), mapper),
            follows = followGraph(h.graph),
            profiles = ProfileRepository(h.graph, mapper),
            feed = FeedRepository(h.feedApi, mapper) { it },
            reports = ReportRepository(h.reportApi, mapper),
            hidden = h.hidden,
        )
    }

    /** The home timeline over the same fakes, so the filter is observed where it matters. */
    private fun feedViewModel(h: Harness) = FeedViewModel(
        mode = FeedMode.Home,
        repository = FeedRepository(h.feedApi, mapper) { it },
        urlResolver = MediaUrlResolver(
            ApiConfig(
                baseUrl = "http://127.0.0.1:8080",
                wsBaseUrl = "ws://127.0.0.1:8093",
                clientVersion = "test",
                environment = "test",
                isDebug = true,
            ),
        ),
        engagement = EngagementStore(AcceptingWrites()),
        shares = EngagementRepository(UnusedEngagementApi(), mapper),
        tabState = FeedTabState(),
        follows = followGraph(h.graph),
        hidden = h.hidden,
    )

    private val page = listOf(dto("p1", "a"), dto("p2", "b"), dto("p3", "a"))

    // ── Not interested ──────────────────────────────────────────────────

    @Test
    fun `not interested removes the post from the list at once and tells the server`() = runTest {
        val h = Harness(page)
        val vm = viewModel(h)
        val feed = feedViewModel(h)
        assertThat(feed.items.asSnapshot().map { it.id }).containsExactly("p1", "p2", "p3").inOrder()

        vm.notInterested(item("p2", "b"))

        assertThat(h.hidden.state.value.postIds).containsExactly("p2")
        assertThat(feed.items.asSnapshot().map { it.id }).containsExactly("p1", "p3").inOrder()
        advanceUntilIdle()
        assertThat(h.feedApi.feedback).containsExactly(FeedFeedbackRequest("p2", "not_interested"))
        assertThat(vm.message.value?.text).isEqualTo("We'll show you fewer posts like this")
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `a refused not interested puts the post back and says so`() = runTest {
        val h = Harness(page).apply { feedApi.feedbackFails = true }
        val vm = viewModel(h)

        vm.notInterested(item("p2", "b"))
        advanceUntilIdle()

        assertThat(h.hidden.state.value.postIds).isEmpty()
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `interested undoes an earlier not interested`() = runTest {
        val h = Harness(page)
        val vm = viewModel(h)
        vm.notInterested(item("p2", "b"))

        vm.interested(item("p2", "b"))
        advanceUntilIdle()

        assertThat(h.hidden.state.value.postIds).isEmpty()
        assertThat(h.feedApi.feedback.map { it.signal }).containsExactly("not_interested", "interested").inOrder()
    }

    // ── Block ───────────────────────────────────────────────────────────

    @Test
    fun `block removes every post by the author and sends the block`() = runTest {
        val h = Harness(page)
        val vm = viewModel(h)
        val feed = feedViewModel(h)

        vm.block(item("p1", "a"))

        assertThat(h.hidden.state.value.authorIds).containsExactly("a")
        assertThat(feed.items.asSnapshot().map { it.id }).containsExactly("p2")
        advanceUntilIdle()
        assertThat(h.graph.blockRequests).containsExactly("a")
        assertThat(vm.message.value?.text).isEqualTo("Blocked @a")
    }

    @Test
    fun `a refused block brings the author's posts back`() = runTest {
        val h = Harness(page).apply { graph.blockFails = true }
        val vm = viewModel(h)

        vm.block(item("p1", "a"))
        advanceUntilIdle()

        assertThat(h.hidden.state.value.authorIds).isEmpty()
        assertThat(vm.message.value?.type).isEqualTo(UsMessageType.Error)
    }

    // ── Report ──────────────────────────────────────────────────────────

    @Test
    fun `a report carries the reason's wire token and the details`() = runTest {
        val h = Harness(page)
        val vm = viewModel(h)

        vm.report(item("p2", "b"), UsReportReason.HATE, "")
        advanceUntilIdle()
        vm.opened()
        vm.report(item("p2", "b"), UsReportReason.OTHER, "it's a duplicate")
        advanceUntilIdle()

        assertThat(h.reportApi.reports).containsExactly(
            FileReportRequest(entityType = "post", entityId = "p2", reason = "hate", details = ""),
            FileReportRequest(entityType = "post", entityId = "p2", reason = "other", details = "it's a duplicate"),
        ).inOrder()
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Sent)
    }

    @Test
    fun `a 409 reads as already reported, not as a failure`() = runTest {
        val h = Harness(page).apply { reportApi.alreadyReported = true }
        val vm = viewModel(h)

        vm.report(item("p2", "b"), UsReportReason.SPAM, "")
        advanceUntilIdle()

        assertThat(vm.report.value).isEqualTo(UsPostReportState.AlreadyReported)
    }

    @Test
    fun `opening the sheet again forgets the last report`() = runTest {
        val h = Harness(page)
        val vm = viewModel(h)
        vm.report(item("p2", "b"), UsReportReason.SPAM, "")
        advanceUntilIdle()

        vm.opened()

        assertThat(vm.report.value).isEqualTo(UsPostReportState.Idle)
    }

    // ── The state the sheet is built with ───────────────────────────────

    @Test
    fun `the viewer's own post is marked own and offers no follow row`() {
        val state = item("p1", "me").toMoreState(EngagementOverlay(), FollowStatus.FOLLOWING, ownUserId = "me")

        assertThat(state.isOwnPost).isTrue()
        assertThat(state.followRow).isEqualTo(UsPostMoreFollowRow.HIDDEN)
    }

    @Test
    fun `the follow row follows the graph's edge`() {
        assertThat(moreFollowRow(FollowStatus.FOLLOWING)).isEqualTo(UsPostMoreFollowRow.UNFOLLOW)
        assertThat(moreFollowRow(FollowStatus.REQUESTED)).isEqualTo(UsPostMoreFollowRow.UNFOLLOW)
        assertThat(moreFollowRow(FollowStatus.NONE)).isEqualTo(UsPostMoreFollowRow.FOLLOW)
        assertThat(moreFollowRow(null)).isEqualTo(UsPostMoreFollowRow.HIDDEN)
    }

    @Test
    fun `the reason sentence, the handle and this session's bookmark reach the sheet`() {
        val state = item("p1", "call_userb", reason = "From someone you follow")
            .toMoreState(EngagementOverlay(bookmarked = true), FollowStatus.NONE, ownUserId = "me")

        assertThat(state.reasonText).isEqualTo("From someone you follow")
        assertThat(state.username).isEqualTo("call_userb")
        assertThat(state.isBookmarked).isTrue()
        assertThat(state.isOwnPost).isFalse()
        assertThat(state.link).isEqualTo("https://momentum.app/p/p1")
    }
}
