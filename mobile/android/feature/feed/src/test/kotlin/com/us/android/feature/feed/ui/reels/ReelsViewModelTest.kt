package com.us.android.feature.feed.ui.reels

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.PlaybackKind
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishPreview
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.FollowStatus
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feed.data.FeedApi
import com.us.android.feature.feed.data.FeedFeedbackRequest
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.PollVoteRequest
import com.us.android.feature.feed.data.RecordingGraphApi
import com.us.android.feature.feed.data.dto.FeedDeltaDto
import com.us.android.feature.feed.data.dto.FeedItemDto
import com.us.android.feature.feed.data.dto.FeedMediaDto
import com.us.android.feature.feed.data.followGraph
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

/**
 * The Reels tab's rules: what plays, which tab fetches what, what sits above
 * the feed while the viewer's own reel posts, and which rail controls the
 * author's switches hide.
 *
 * The playback rules are where reels breaks if it breaks. `hls_url` arrives
 * gateway-RELATIVE and authorized; the `variants` values are absolute
 * pre-signed object-store URLs; and since instant reels the server may hand
 * back the ORIGINAL file as `playback_kind: "original"`, which an HLS
 * extractor cannot open. Handing the player the wrong one, or anything at
 * all for an asset with no rendition, produces a playback error where a
 * poster or a spinner belongs.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelsViewModelTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    /** Records how the ranked surface was asked for; answers one empty page. */
    private class RecordingApi(private val post: FeedItemDto? = null) : FeedApi {
        val followingOnly = mutableListOf<Boolean?>()
        val postRequests = mutableListOf<String>()

        override suspend fun getFeed(
            surface: String,
            limit: Int,
            cursor: String?,
            followingOnly: Boolean?,
            circleOnly: Boolean?,
        ): ApiEnvelope<List<FeedItemDto>> {
            this.followingOnly += followingOnly
            return ApiEnvelope(data = emptyList(), meta = null)
        }

        override suspend fun getPost(postId: String): ApiEnvelope<FeedItemDto> {
            postRequests += postId
            return ApiEnvelope(data = post ?: error("no post to serve"), meta = null)
        }

        override suspend fun getTrendingHashtags(limit: Int): Nothing = error("unused")

        override suspend fun getPostsByHashtag(tag: String, limit: Int, cursor: String?, sort: String): Nothing =
            error("unused")

        override suspend fun getDelta(feedType: String, anchor: String, limit: Int): ApiEnvelope<FeedDeltaDto> =
            error("unused")

        override suspend fun votePoll(postId: String, body: PollVoteRequest): Nothing = error("unused")

        override suspend fun feedback(body: FeedFeedbackRequest): Nothing = error("unused")
    }

    private class AcceptingWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)
        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    private class UnusedEngagementApi : EngagementApi {
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

    private class RecordingActions : ReelPublishActions {
        val calls = mutableListOf<String>()
        override fun retry() {
            calls += "retry"
        }

        override fun discard() {
            calls += "discard"
        }

        override fun dismiss() {
            calls += "dismiss"
        }
    }

    private class Harness(
        val api: RecordingApi = RecordingApi(),
        val tracker: ReelPublishTracker = ReelPublishTracker(),
        val actions: RecordingActions = RecordingActions(),
        val graph: RecordingGraphApi = RecordingGraphApi(),
    )

    private fun viewModel(h: Harness = Harness()) = ReelsViewModel(
        repository = FeedRepository(h.api, ErrorMapper(json)) { it },
        urlResolver = resolver,
        engagement = EngagementStore(AcceptingWrites()),
        shares = EngagementRepository(UnusedEngagementApi(), ErrorMapper(json)),
        tracker = h.tracker,
        publishActions = h.actions,
        follows = followGraph(h.graph),
        hidden = HiddenPosts(),
    )

    private fun item(vararg media: FeedMedia, controls: FeedPostControls = FeedPostControls()) = FeedItem(
        id = "p",
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = media.toList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
        controls = controls,
    )

    private fun video(
        status: String = "ready",
        hls: String? = "/v1/media/m/hls/master.m3u8",
        variants: Map<String, String> = emptyMap(),
        processingStatus: String = "",
        playbackUrl: String? = null,
        playbackKind: String = "",
    ) = FeedMedia(
        mediaId = "m",
        kind = "video",
        status = status,
        variants = variants,
        hlsUrl = hls,
        processingStatus = processingStatus,
        playbackUrl = playbackUrl,
        playbackKind = playbackKind,
    )

    // ── Playback ────────────────────────────────────────────────────────

    @Test
    fun `a ready video resolves to an absolute gateway hls url`() {
        val playback = viewModel().playback(item(video()))

        assertThat(playback?.url).isEqualTo("http://127.0.0.1:8080/v1/media/m/hls/master.m3u8")
        assertThat(playback?.kind).isEqualTo(PlaybackKind.Hls)
    }

    /**
     * Instant reels: the server says what to play while it transcodes — the
     * original MP4 — and that must open through the progressive extractor,
     * not the HLS one, which would report a playlist parse error.
     */
    @Test
    fun `the server's original playback plays progressively`() {
        val media = video(
            status = "processing",
            hls = null,
            processingStatus = "processing",
            playbackUrl = "/v1/media/m/original",
            playbackKind = "original",
        )

        val playback = viewModel().playback(item(media))

        assertThat(playback?.url).isEqualTo("http://127.0.0.1:8080/v1/media/m/original")
        assertThat(playback?.kind).isEqualTo(PlaybackKind.Progressive)
    }

    @Test
    fun `the server's hls playback wins over the row's own hls url`() {
        val media = video(playbackUrl = "https://cdn.example/master.m3u8", playbackKind = "hls")

        val playback = viewModel().playback(item(media))

        assertThat(playback?.url).isEqualTo("https://cdn.example/master.m3u8")
        assertThat(playback?.kind).isEqualTo(PlaybackKind.Hls)
    }

    /** A feed-service that has not learned the contract yet: the original variant is the fallback. */
    @Test
    fun `a processing asset with an original variant plays that variant progressively`() {
        val media = video(
            status = "processing",
            hls = null,
            variants = mapOf("original" to "https://store/signed-original.mp4"),
        )

        val playback = viewModel().playback(item(media))

        assertThat(playback?.url).isEqualTo("https://store/signed-original.mp4")
        assertThat(playback?.kind).isEqualTo(PlaybackKind.Progressive)
    }

    /**
     * A still-processing asset with nothing on offer has no rendition.
     * Returning null lets the pager show "still processing" instead of
     * handing the player a URL that 404s.
     */
    @Test
    fun `an unready asset with nothing to play yields no playback`() {
        assertThat(viewModel().playback(item(video(status = "processing", hls = null)))).isNull()
    }

    @Test
    fun `a ready asset with no hls url yields null rather than a guess`() {
        assertThat(viewModel().playback(item(video(hls = null)))).isNull()
    }

    @Test
    fun `a post with no media yields null`() {
        assertThat(viewModel().playback(item())).isNull()
    }

    /** An image attachment is not playable and must not be handed to a player. */
    @Test
    fun `an image attachment is ignored`() {
        val image = FeedMedia(mediaId = "m", kind = "image", status = "ready")

        assertThat(viewModel().playback(item(image))).isNull()
    }

    @Test
    fun `the poster comes from the thumbnail variant`() {
        val media = video(variants = mapOf("thumb_150" to "http://o/thumb", "360p" to "http://o/360"))

        assertThat(viewModel().posterUrl(item(media))).isEqualTo("http://o/thumb")
    }

    @Test
    fun `no thumbnail variant yields no poster`() {
        assertThat(viewModel().posterUrl(item(video()))).isNull()
    }

    /**
     * Mute is held in the ViewModel, not per player. A per-player flag resets
     * the moment the pool recycles that instance, so the sound would silently
     * come back on after four swipes.
     */
    @Test
    fun `reels start muted and the choice survives toggling`() {
        val vm = viewModel()
        assertThat(vm.muted.value).isTrue()

        vm.toggleMuted()
        assertThat(vm.muted.value).isFalse()

        vm.toggleMuted()
        assertThat(vm.muted.value).isTrue()
    }

    // ── The surface ─────────────────────────────────────────────────────

    /**
     * Reels is ONE ranked surface — `following_only` OMITTED, so the request
     * the server has served since day one is unchanged. The For You /
     * Following split was reversed (founder, 2026-09-04).
     */
    @Test
    fun `reels asks for the plain ranked surface`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.items.asSnapshot()

        assertThat(h.api.followingOnly).containsExactly(null)
    }

    // ── Follow ──────────────────────────────────────────────────────────

    /** Settling on a reel learns its author's edge; a follow goes through the graph. */
    @Test
    fun `a shown reel learns its author and a follow is sent`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        val reel = item(video())

        vm.onReelShown(reel)
        advanceUntilIdle()
        assertThat(h.graph.relationshipRequests).containsExactly("me" to "a")
        assertThat(vm.followEdges.value["a"]).isEqualTo(FollowStatus.NONE)

        vm.onFollow("a")
        advanceUntilIdle()
        assertThat(h.graph.followRequests).containsExactly("a")
        assertThat(vm.followEdges.value["a"]).isEqualTo(FollowStatus.FOLLOWING)
    }

    // ── The head: the viewer's own reel while it posts ──────────────────

    private val preview = ReelPublishPreview(creationKey = "key-1", coverPath = "/cache/key-1.jpg", caption = "sunday")

    @Test
    fun `nothing pending means no head`() = runTest {
        val vm = viewModel()
        backgroundScope.launch { vm.head.collect {} }

        assertThat(vm.head.value).isNull()
    }

    /** The pending item is the cover under a loader from Preparing through Posting, with no failure. */
    @Test
    fun `an in-flight publish is a pending item carrying the cover and caption`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.tracker.setPreview(preview)

        listOf(
            ReelPublishState.Preparing,
            ReelPublishState.Uploading(0.4f),
            ReelPublishState.Processing,
            ReelPublishState.Posting,
        ).forEach { state ->
            h.tracker.update(state)
            advanceUntilIdle()
            assertThat(vm.head.value).isEqualTo(
                ReelsHead.Pending(creationKey = "key-1", coverPath = "/cache/key-1.jpg", caption = "sunday"),
            )
        }
    }

    @Test
    fun `a stopped publish keeps the pending item and adds the failure`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.tracker.setPreview(preview)
        h.tracker.update(ReelPublishState.Uploading(0.4f))

        h.tracker.update(ReelPublishState.Failed("Couldn't reach the server.", retryable = true))
        advanceUntilIdle()

        val head = vm.head.value as ReelsHead.Pending
        assertThat(head.coverPath).isEqualTo("/cache/key-1.jpg")
        assertThat(head.failure).isEqualTo(PendingFailure("Couldn't reach the server.", retryable = true))

        vm.retryPublish()
        vm.discardPublish()
        assertThat(h.actions.calls).containsExactly("retry", "discard").inOrder()
    }

    /**
     * A tracker state with no preview cannot be drawn — a restart before the
     * controller restored the record — so it shows nothing rather than a
     * blank page with a loader on it.
     */
    @Test
    fun `a state without a preview shows no head`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }

        h.tracker.update(ReelPublishState.Uploading(0.4f))
        advanceUntilIdle()

        assertThat(vm.head.value).isNull()
    }

    /**
     * The moment the worker reports the post id, the reel is fetched by id
     * and the pending item BECOMES it — no refresh, no banner — and the
     * tracker is let go so the next publish starts clean.
     */
    @Test
    fun `a published post is fetched and becomes the live head`() = runTest {
        val posted = FeedItemDto(
            id = "post-9",
            postType = "video",
            media = listOf(
                FeedMediaDto(
                    mediaId = "m9",
                    kind = "video",
                    playbackUrl = "/v1/media/m9/original",
                    playbackKind = "original",
                ),
            ),
        )
        val h = Harness(api = RecordingApi(post = posted))
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.tracker.setPreview(preview)
        h.tracker.update(ReelPublishState.Posting)

        h.tracker.update(ReelPublishState.Published("post-9"))
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("post-9")
        val head = vm.head.value as ReelsHead.Live
        assertThat(head.item.id).isEqualTo("post-9")
        assertThat(vm.playback(head.item)?.kind).isEqualTo(PlaybackKind.Progressive)
        assertThat(h.actions.calls).containsExactly("dismiss")
    }

    /** The post exists even when it cannot be read back yet; the loader must not outlive the publish. */
    @Test
    fun `a published post that cannot be fetched is let go rather than held as pending`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.tracker.setPreview(preview)

        h.tracker.update(ReelPublishState.Published("post-9"))
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("post-9", "post-9", "post-9")
        assertThat(h.actions.calls).containsExactly("dismiss")
    }

    // ── The rail ────────────────────────────────────────────────────────

    @Test
    fun `the rail shows comment and share by default`() {
        assertThat(FeedPostControls().railVisibility())
            .isEqualTo(ReelRailVisibility(showComment = true, showShare = true))
    }

    @Test
    fun `no_comments hides the comment control and nothing else`() {
        assertThat(FeedPostControls(noComments = true).railVisibility())
            .isEqualTo(ReelRailVisibility(showComment = false, showShare = true))
    }

    @Test
    fun `hide_share hides the share control and nothing else`() {
        assertThat(FeedPostControls(hideShare = true).railVisibility())
            .isEqualTo(ReelRailVisibility(showComment = true, showShare = false))
    }

    @Test
    fun `the rail rule reads the row's controls`() {
        val row = item(video(), controls = FeedPostControls(noComments = true, hideShare = true))

        assertThat(row.controls.railVisibility())
            .isEqualTo(ReelRailVisibility(showComment = false, showShare = false))
    }
}
