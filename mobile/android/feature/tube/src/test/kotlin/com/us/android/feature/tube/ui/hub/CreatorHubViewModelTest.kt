package com.us.android.feature.tube.ui.hub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.copyright.CopyrightReviews
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.CopyrightReviewState
import com.us.android.core.media.ReelsEntry
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.network.ApiConfig
import com.us.android.feature.tube.data.hub.CreatorCommentTools
import com.us.android.feature.tube.data.hub.CreatorHubApi
import com.us.android.feature.tube.data.hub.CreatorHubRepository
import com.us.android.feature.tube.data.hub.HubLibraryRow
import com.us.android.feature.tube.data.hub.HubVisibility
import com.us.android.feature.tube.data.hub.HubDayPoint
import com.us.android.feature.tube.data.hub.HubOpenKind
import com.us.android.feature.tube.data.hub.HubPeriod
import io.mockk.every
import io.mockk.mockk
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * T-31: the hub's states — loading, empty, error with retry — the period
 * parameter, lazy loading per tab, paging, and where a row opens.
 */
class CreatorHubViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = NetworkModule.provideJson()

    private fun el(text: String): JsonElement = Json.parseToJsonElement(text)

    /** Answers from queues; a missing answer is a network failure. Records what was asked. */
    private class FakeApi : CreatorHubApi {
        val insights = ArrayDeque<JsonElement>()
        val uploads = ArrayDeque<ApiEnvelope<List<JsonElement>>>()
        val drafts = ArrayDeque<ApiEnvelope<List<JsonElement>>>()
        val inbox = ArrayDeque<ApiEnvelope<List<JsonElement>>>()
        val calls = mutableListOf<String>()

        override suspend fun creatorInsights(period: String): JsonElement {
            calls += "insights:$period"
            return insights.removeFirstOrNull() ?: throw IOException("offline")
        }

        override suspend fun uploads(kind: String, limit: Int, cursor: String?): ApiEnvelope<List<JsonElement>> {
            calls += "uploads:$kind:$limit:${cursor.orEmpty()}"
            return uploads.removeFirstOrNull() ?: throw IOException("offline")
        }

        override suspend fun drafts(limit: Int, cursor: String?): ApiEnvelope<List<JsonElement>> {
            calls += "drafts:$limit:${cursor.orEmpty()}"
            return drafts.removeFirstOrNull() ?: throw IOException("offline")
        }

        override suspend fun inbox(
            status: String,
            content: String,
            sort: String,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<JsonElement>> {
            calls += "inbox:$status:$content:$sort:$limit:${cursor.orEmpty()}"
            return inbox.removeFirstOrNull() ?: throw IOException("offline")
        }
    }

    private val api = FakeApi()
    private val reelsEntry = ReelsEntry()
    private val follows = mockk<FollowGraph> { every { ownId } returns "me" }

    /** Heart and Pin as the server answers them: each write recorded, and refused while [fails]. */
    private class FakeCommentTools : CreatorCommentTools {
        val writes = mutableListOf<String>()
        var fails = false

        override suspend fun setHearted(commentId: String, hearted: Boolean): AppResult<Unit> {
            writes += "heart:$commentId:$hearted"
            return if (fails) AppResult.Failure(AppError.NoNetwork()) else AppResult.Success(Unit)
        }

        override suspend fun setPinned(commentId: String, pinned: Boolean): AppResult<Unit> {
            writes += "pin:$commentId:$pinned"
            return if (fails) AppResult.Failure(AppError.NoNetwork()) else AppResult.Success(Unit)
        }
    }

    private val tools = FakeCommentTools()
    private val urls = MediaUrlResolver(
        ApiConfig(
            baseUrl = "https://api.test",
            wsBaseUrl = "wss://api.test",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    private fun viewModel() =
        CreatorHubViewModel(CreatorHubRepository(api, ErrorMapper(json)), reelsEntry, follows, tools, urls, copyrightReviews)

    /** Copyright reviews learned this session (2026-10-24): a "Request review" sent anywhere. */
    private val copyrightReviews = CopyrightReviews()

    private fun page(vararg rows: String, next: String? = null) =
        ApiEnvelope(data = rows.map(::el), meta = next?.let { ApiMeta(nextCursor = it) })

    // ── overview ───────────────────────────────────────────────────────

    @Test
    fun `opens on the overview for 28 days, and reads the numbers`() {
        api.insights += el(
            """{"views": 40, "likes": 14, "daily_stats": [{"date": "2026-10-01T00:00:00Z", "views": 10}]}""",
        )

        val vm = viewModel()

        assertThat(api.calls).containsExactly("insights:28d")
        val state = vm.state.value
        assertThat(state.section).isEqualTo(HubSection.OVERVIEW)
        assertThat(state.period).isEqualTo(HubPeriod.DAYS_28)
        val ready = state.overview as OverviewState.Ready
        assertThat(ready.insights.views).isEqualTo(40)
        assertThat(ready.insights.viewsByDay).containsExactly(HubDayPoint("2026-10-01", 10))
        assertThat(vm.ownUserId).isEqualTo("me")
    }

    @Test
    fun `a period change asks again with that period`() {
        api.insights += el("{}")
        api.insights += el("""{"views": 3}""")
        val vm = viewModel()

        vm.onPeriod(HubPeriod.DAYS_7)

        assertThat(api.calls).containsExactly("insights:28d", "insights:7d").inOrder()
        assertThat(vm.state.value.period).isEqualTo(HubPeriod.DAYS_7)
        assertThat((vm.state.value.overview as OverviewState.Ready).insights.views).isEqualTo(3)
    }

    @Test
    fun `choosing the period already shown asks nothing`() {
        api.insights += el("{}")
        val vm = viewModel()

        vm.onPeriod(HubPeriod.DAYS_28)

        assertThat(api.calls).containsExactly("insights:28d")
    }

    @Test
    fun `an overview failure shows the error, and Try again recovers`() {
        val vm = viewModel()
        assertThat(vm.state.value.overview).isEqualTo(OverviewState.Failed(CreatorHubViewModel.OVERVIEW_FAILED))

        api.insights += el("""{"views": 1}""")
        vm.retryOverview()

        assertThat((vm.state.value.overview as OverviewState.Ready).insights.views).isEqualTo(1)
    }

    // ── library ────────────────────────────────────────────────────────

    @Test
    fun `the library loads only when shown, videos first, and each tab once`() {
        api.insights += el("{}")
        api.uploads += page("""{"id": "v1", "title": "One", "content_type": "long_video"}""")
        val vm = viewModel()
        assertThat(vm.state.value.videos.loaded).isFalse()

        vm.onSection(HubSection.LIBRARY)
        vm.onSection(HubSection.OVERVIEW)
        vm.onSection(HubSection.LIBRARY)

        assertThat(api.calls).containsExactly("insights:28d", "uploads:videos:30:").inOrder()
        assertThat(vm.state.value.videos.items.map { it.id }).containsExactly("v1")
        assertThat(vm.state.value.videos.loading).isFalse()
    }

    @Test
    fun `the reels tab reads the flicks list and the drafts tab the drafts route`() {
        api.insights += el("{}")
        api.uploads += page()
        api.uploads += page("""{"id": "r1", "title": "Reel"}""")
        api.drafts += page("""{"id": "d1", "title": "Draft"}""")
        val vm = viewModel()

        vm.onSection(HubSection.LIBRARY)
        vm.onLibraryTab(HubLibraryTab.REELS)
        vm.onLibraryTab(HubLibraryTab.DRAFTS)

        assertThat(api.calls)
            .containsExactly("insights:28d", "uploads:videos:30:", "uploads:flicks:30:", "drafts:20:")
            .inOrder()
        assertThat(vm.state.value.videos.items).isEmpty()
        assertThat(vm.state.value.videos.loaded).isTrue()
        assertThat(vm.state.value.reels.items.single().id).isEqualTo("r1")
        assertThat(vm.state.value.drafts.items.single().title).isEqualTo("Draft")
    }

    @Test
    fun `a library failure is an error with retry, and retry loads the first page again`() {
        api.insights += el("{}")
        val vm = viewModel()
        vm.onSection(HubSection.LIBRARY)
        assertThat(vm.state.value.videos.error).isEqualTo(CreatorHubViewModel.VIDEOS_FAILED)

        api.uploads += page("""{"id": "v1", "title": "One"}""")
        vm.loadLibrary(HubLibraryTab.VIDEOS, more = false)

        assertThat(vm.state.value.videos.error).isNull()
        assertThat(vm.state.value.videos.items.map { it.id }).containsExactly("v1")
    }

    @Test
    fun `Load more appends the next page by its cursor, and stops when the server has no more`() {
        api.insights += el("{}")
        api.uploads += page("""{"id": "v1", "title": "One"}""", next = "c2")
        api.uploads += page("""{"id": "v2", "title": "Two"}""")
        val vm = viewModel()
        vm.onSection(HubSection.LIBRARY)
        assertThat(vm.state.value.videos.hasMore).isTrue()

        vm.loadLibrary(HubLibraryTab.VIDEOS, more = true)
        vm.loadLibrary(HubLibraryTab.VIDEOS, more = true)

        assertThat(api.calls).containsExactly("insights:28d", "uploads:videos:30:", "uploads:videos:30:c2").inOrder()
        assertThat(vm.state.value.videos.items.map { it.id }).containsExactly("v1", "v2").inOrder()
        assertThat(vm.state.value.videos.hasMore).isFalse()
    }

    @Test
    fun `a failed next page keeps the rows already shown`() {
        api.insights += el("{}")
        api.uploads += page("""{"id": "v1", "title": "One"}""", next = "c2")
        val vm = viewModel()
        vm.onSection(HubSection.LIBRARY)

        vm.loadLibrary(HubLibraryTab.VIDEOS, more = true)

        val videos = vm.state.value.videos
        assertThat(videos.items.map { it.id }).containsExactly("v1")
        assertThat(videos.moreFailed).isTrue()
        assertThat(videos.error).isNull()
        assertThat(videos.hasMore).isTrue()
    }

    // ── conversations ──────────────────────────────────────────────────

    @Test
    fun `conversations load the inbox when shown - empty is empty, not an error`() {
        api.insights += el("{}")
        api.inbox += ApiEnvelope(data = null)
        val vm = viewModel()

        vm.onSection(HubSection.CONVERSATIONS)

        assertThat(api.calls).containsExactly("insights:28d", "inbox:all:all:newest:30:").inOrder()
        val inbox = vm.state.value.inbox
        assertThat(inbox.loaded).isTrue()
        assertThat(inbox.items).isEmpty()
        assertThat(inbox.error).isNull()
    }

    @Test
    fun `an inbox failure shows its error, and retry reads the rows`() {
        api.insights += el("{}")
        val vm = viewModel()
        vm.onSection(HubSection.CONVERSATIONS)
        assertThat(vm.state.value.inbox.error).isEqualTo(CreatorHubViewModel.INBOX_FAILED)

        api.inbox += page(
            """{"comment": {"id": "c1", "post_id": "p1", "body": "Nice"}, "post": {"id": "p1", "title": "One"}}""",
        )
        vm.loadInbox(more = false)

        assertThat(vm.state.value.inbox.items.single().body).isEqualTo("Nice")
    }

    // ── creator tools (2026-10-17) ─────────────────────────────────────

    private fun inboxRow(id: String, post: String, pinned: Boolean = false, hearted: Boolean = false, reply: Boolean = false) =
        """{"comment": {"id": "$id", "post_id": "$post", "body": "b", "pinned": $pinned, """ +
            """"hearted_by_author": $hearted, "is_reply": $reply}, "post": {"id": "$post", "title": "T"}}"""

    private fun conversations(vararg rows: String): CreatorHubViewModel {
        api.insights += el("{}")
        api.inbox += page(*rows)
        return viewModel().also { it.onSection(HubSection.CONVERSATIONS) }
    }

    @Test
    fun `the inbox reads each comment's heart and pin`() {
        val vm = conversations(inboxRow("c1", "p1", pinned = true), inboxRow("c2", "p1", hearted = true, reply = true))

        val rows = vm.state.value.inbox.items
        assertThat(rows.map { it.pinned }).containsExactly(true, false).inOrder()
        assertThat(rows.map { it.hearted }).containsExactly(false, true).inOrder()
        assertThat(rows.map { it.isReply }).containsExactly(false, true).inOrder()
    }

    @Test
    fun `heart flips at once and is written`() {
        val vm = conversations(inboxRow("c1", "p1"))

        vm.toggleHeart("c1")

        assertThat(vm.state.value.inbox.items.single().hearted).isTrue()
        assertThat(tools.writes).containsExactly("heart:c1:true")
        vm.toggleHeart("c1")
        assertThat(vm.state.value.inbox.items.single().hearted).isFalse()
        assertThat(tools.writes.last()).isEqualTo("heart:c1:false")
    }

    @Test
    fun `a refused heart is put back and said`() {
        val vm = conversations(inboxRow("c1", "p1"))
        tools.fails = true

        vm.toggleHeart("c1")

        assertThat(vm.state.value.inbox.items.single().hearted).isFalse()
        assertThat(vm.state.value.notice).isEqualTo(CreatorHubViewModel.HEART_FAILED)
        assertThat(vm.state.value.commentBusy).isEmpty()
        vm.dismissNotice()
        assertThat(vm.state.value.notice).isNull()
    }

    /** One pin per post: pinning another comment on the same post takes the first one's away. */
    @Test
    fun `a pin replaces the post's other pin, and leaves other posts alone`() {
        val vm = conversations(inboxRow("c1", "p1", pinned = true), inboxRow("c2", "p1"), inboxRow("c3", "p2", pinned = true))

        vm.togglePin("c2")

        val pinned = vm.state.value.inbox.items.associate { it.commentId to it.pinned }
        assertThat(pinned).containsExactly("c1", false, "c2", true, "c3", true)
        assertThat(tools.writes).containsExactly("pin:c2:true")
    }

    @Test
    fun `a refused pin puts every row of the post back`() {
        val vm = conversations(inboxRow("c1", "p1", pinned = true), inboxRow("c2", "p1"))
        tools.fails = true

        vm.togglePin("c2")

        assertThat(vm.state.value.inbox.items.map { it.pinned }).containsExactly(true, false).inOrder()
        assertThat(vm.state.value.notice).isEqualTo(CreatorHubViewModel.PIN_FAILED)
    }

    @Test
    fun `a reply is never pinned`() {
        val vm = conversations(inboxRow("c1", "p1", reply = true))

        vm.togglePin("c1")

        assertThat(tools.writes).isEmpty()
        assertThat(vm.state.value.inbox.items.single().pinned).isFalse()
    }

    @Test
    fun `a library row's still resolves against the API host`() {
        api.insights += el("{}")
        val vm = viewModel()

        val cover = HubLibraryRow(
            id = "v1", title = "t", contentType = "long_video", visibility = HubVisibility.PUBLIC,
            scheduledAt = null, publishedAt = null, createdAt = "", viewCount = 0, likeCount = 0, commentCount = 0,
            processingStatus = "ready", thumbnailPath = "/v1/media/m1/serve",
        )

        assertThat(vm.thumbnailUrl(cover)).isEqualTo("https://api.test/v1/media/m1/serve")
        assertThat(vm.thumbnailUrl(cover.copy(thumbnailPath = "https://cdn.test/t.jpg"))).isEqualTo("https://cdn.test/t.jpg")
        assertThat(vm.thumbnailUrl(cover.copy(thumbnailPath = null))).isNull()
    }

    // ── opening rows ───────────────────────────────────────────────────

    @Test
    fun `a reel is parked for the Reels tab, a video and a post are not`() {
        api.insights += el("{}")
        val vm = viewModel()

        assertThat(vm.open("r1", "flick", HubOpenKind.POST)).isEqualTo(HubOpenKind.REEL)
        assertThat(reelsEntry.requested.value).isEqualTo("r1")
        reelsEntry.clear()

        assertThat(vm.open("v1", "long_video", HubOpenKind.POST)).isEqualTo(HubOpenKind.VIDEO)
        assertThat(vm.open("p1", "post", HubOpenKind.VIDEO)).isEqualTo(HubOpenKind.POST)
        assertThat(vm.open("x1", "", HubOpenKind.REEL)).isEqualTo(HubOpenKind.REEL)
        assertThat(reelsEntry.requested.value).isEqualTo("x1")
    }

    // ── copyright title review (2026-10-24) ─────────────────────────────

    @Test
    fun `a copyright notice opens the Library's Videos list, read once`() {
        api.insights += el("{}")
        api.uploads += page(
            """{"id": "v1", "title": "Held", "content_type": "long_video", "flags": ["copyright_hold"],
               "copyright_review": {"state": "pending", "matched_title": "Leo", "created_at": "2026-10-24T08:00:01Z"}}""",
        )
        val vm = viewModel()
        vm.onLibraryTab(HubLibraryTab.DRAFTS.also { api.drafts += page() })
        vm.openUploads()

        assertThat(vm.state.value.section).isEqualTo(HubSection.LIBRARY)
        assertThat(vm.state.value.libraryTab).isEqualTo(HubLibraryTab.VIDEOS)
        assertThat(api.calls.count { it.startsWith("uploads:videos") }).isEqualTo(1)
        val row = vm.state.value.videos.items.single()
        assertThat(row.copyrightReview?.state).isEqualTo(CopyrightReviewState.PENDING)
        assertThat(row.heldForCopyright).isTrue()
    }

    @Test
    fun `a request sent this session is what the row shows, over what the list read`() {
        api.insights += el("{}")
        api.uploads += page(
            """{"id": "v1", "title": "Held", "content_type": "long_video",
               "copyright_review": {"state": "pending", "matched_title": "Leo", "created_at": "2026-10-24T08:00:01Z"}}""",
            """{"id": "v2", "title": "Fine", "content_type": "long_video"}""",
        )
        val vm = viewModel()
        vm.openUploads()
        val (held, fine) = vm.state.value.videos.items
        assertThat(vm.reviewOf(held, vm.reviews.value)?.canRequestReview).isTrue()

        copyrightReviews.record(
            "v1",
            CopyrightReview(CopyrightReviewState.PENDING, "Leo", "2026-10-24T08:00:01Z", reviewRequestedAt = "2026-10-24T09:30:00Z"),
        )
        val now = vm.reviewOf(held, vm.reviews.value)!!
        assertThat(now.canRequestReview).isFalse()
        assertThat(now.reviewRequestedAt).isEqualTo("2026-10-24T09:30:00Z")
        assertThat(vm.reviewOf(fine, vm.reviews.value)).isNull()
    }
}
