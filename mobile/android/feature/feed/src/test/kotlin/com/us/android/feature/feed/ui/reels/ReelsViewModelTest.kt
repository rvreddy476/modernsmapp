package com.us.android.feature.feed.ui.reels

import androidx.paging.PagingDataEvent
import androidx.paging.PagingDataPresenter
import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.analytics.AnalyticsEvent
import com.us.android.core.analytics.AnalyticsEventType
import com.us.android.core.analytics.AnalyticsRecorder
import com.us.android.core.analytics.NegativeSignalReason
import com.us.android.core.analytics.NoOpAnalyticsRecorder
import com.us.android.core.analytics.VideoWatchTracker
import com.us.android.core.analytics.WatchProbe
import com.us.android.core.analytics.WatchSession
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.ReelsAutoScrollStore
import com.us.android.core.datastore.ReelsSoundStore
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementApi
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedFeedbackRequest
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.LibrarySoundUseDto
import com.us.android.core.feed.data.LibraryTracksDto
import com.us.android.core.feed.data.PollVoteRequest
import com.us.android.core.feed.data.SoundReelsDto
import com.us.android.core.feed.data.SoundRowDto
import com.us.android.core.feed.data.SoundsApi
import com.us.android.core.feed.data.SoundsRepository
import com.us.android.core.feed.data.UseSoundDto
import com.us.android.core.feed.data.dto.FeedAuthorDto
import com.us.android.core.feed.data.dto.FeedDeltaDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.FeedMediaDto
import com.us.android.core.feed.data.dto.FeedSoundDto
import com.us.android.core.feed.offline.NoOfflineLibrary
import com.us.android.core.feed.offline.OfflineCopy
import com.us.android.core.feed.offline.OfflineKind
import com.us.android.core.feed.offline.OfflineLibrary
import com.us.android.core.feed.offline.OfflineSaveResult
import com.us.android.core.feed.offline.OfflineSound
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.offline.OfflineStream
import com.us.android.core.media.ChosenSound
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackCaption
import com.us.android.core.media.PlaybackKind
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.SoundEntry
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishPreview
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.ReelSound
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.ui.SPEED_NORMAL
import com.us.android.core.ui.SPEED_QUICKER
import com.us.android.core.ui.UsCaptionOption
import com.us.android.core.ui.UsReelQuality
import com.us.android.feature.feed.data.RecordingChannelApi
import com.us.android.feature.feed.data.RecordingGraphApi
import com.us.android.feature.feed.data.followGraph
import com.us.android.feature.feed.data.subscriptionGraph
import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
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

    /**
     * Records how the ranked surface was asked for. Answers [feedPages] in
     * order, one per request — a null entry is a request that fails on the
     * wire — and an empty page once they run out.
     */
    private class RecordingApi(
        private val post: FeedItemDto? = null,
        feedPages: List<List<String>?> = emptyList(),
    ) : FeedApi {
        val followingOnly = mutableListOf<Boolean?>()
        val cursors = mutableListOf<String?>()
        val postRequests = mutableListOf<String>()
        private val pages = ArrayDeque(feedPages)

        /** Holds every feed request until completed, to keep a refresh on the wire. */
        var gate: CompletableDeferred<Unit>? = null

        override suspend fun getFeed(
            surface: String,
            limit: Int,
            cursor: String?,
            followingOnly: Boolean?,
            circleOnly: Boolean?,
            feedMode: String?,
        ): ApiEnvelope<List<FeedItemDto>> {
            this.followingOnly += followingOnly
            cursors += cursor
            gate?.await()
            val ids = if (pages.isEmpty()) emptyList() else pages.removeFirst() ?: throw IOException("offline")
            return ApiEnvelope(data = ids.map { FeedItemDto(id = it, postType = "video") }, meta = null)
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
        override fun retry(creationKey: String) {
            calls += "retry:$creationKey"
        }

        override fun discard(creationKey: String) {
            calls += "discard:$creationKey"
        }

        override fun dismiss(creationKey: String) {
            calls += "dismiss:$creationKey"
        }
    }

    /**
     * The viewer's stored choice of sound, in memory. [stored] null is a file
     * that has not answered yet: the read is still on its way.
     */
    private class FakeSoundStore(initial: Boolean? = false) : ReelsSoundStore {
        val stored = MutableStateFlow(initial)
        val writes = mutableListOf<Boolean>()

        override val soundOn: Flow<Boolean> = stored.filterNotNull()

        override suspend fun setSoundOn(on: Boolean) {
            writes += on
            stored.value = on
        }
    }

    /** "Auto scroll", stored in memory (T-32). [stored] null is a file that has not answered yet. */
    private class FakeAutoScrollStore(initial: Boolean? = false) : ReelsAutoScrollStore {
        val stored = MutableStateFlow(initial)
        val writes = mutableListOf<Boolean>()

        override val autoScroll: Flow<Boolean> = stored.filterNotNull()

        override suspend fun setAutoScroll(on: Boolean) {
            writes += on
            stored.value = on
        }
    }

    /** Answers "use this sound" with a sound or a refusal, and counts the asks. */
    private class FakeSoundsApi(
        private val sound: FeedSoundDto? = FeedSoundDto(id = "s-made", title = "Original sound - Ada", artist = "Ada"),
        private val refusal: String? = null,
    ) : SoundsApi {
        val useRequests = mutableListOf<String>()

        override suspend fun useSound(postId: String): ApiEnvelope<UseSoundDto> {
            useRequests += postId
            if (refusal != null) {
                return ApiEnvelope(error = ApiErrorBody(code = refusal, message = "not the words shown"))
            }
            return ApiEnvelope(data = UseSoundDto(sound = sound))
        }

        override suspend fun reelsBySound(soundId: String, limit: Int, cursor: String?): ApiEnvelope<SoundReelsDto> =
            error("unused")

        override suspend fun sound(soundId: String): ApiEnvelope<SoundRowDto> = error("unused")

        override suspend fun audioLibrary(genre: String?, limit: Int, offset: Int): ApiEnvelope<LibraryTracksDto> =
            error("unused")

        override suspend fun useLibrarySound(libraryId: String): ApiEnvelope<LibrarySoundUseDto> = error("unused")
    }

    private class RecordingRecorder : AnalyticsRecorder {
        val events = mutableListOf<AnalyticsEvent>()

        override fun record(event: AnalyticsEvent?): Job? {
            if (event != null) events += event
            return null
        }

        override suspend fun recordNow(event: AnalyticsEvent?) {
            record(event)
        }

        override fun recordEngagement(type: String, session: WatchSession) = Unit
        override fun recordNegativeSignal(type: String, session: WatchSession, reason: NegativeSignalReason) = Unit
        override fun flush() = Unit
    }

    private class Harness(
        val api: RecordingApi = RecordingApi(),
        val tracker: ReelPublishTracker = ReelPublishTracker(),
        val actions: RecordingActions = RecordingActions(),
        val graph: RecordingGraphApi = RecordingGraphApi(),
        val channels: RecordingChannelApi = RecordingChannelApi(),
        val entry: ReelsEntry = ReelsEntry(),
        val sounds: FakeSoundsApi = FakeSoundsApi(),
        val soundEntry: SoundEntry = SoundEntry(),
        val soundStore: FakeSoundStore = FakeSoundStore(),
        val autoScrollStore: FakeAutoScrollStore = FakeAutoScrollStore(),
        val watch: VideoWatchTracker = VideoWatchTracker.disabled(),
        val writes: EngagementWrites = AcceptingWrites(),
        val offline: OfflineLibrary = NoOfflineLibrary,
    )

    private fun viewModel(h: Harness = Harness(), store: EngagementStore = EngagementStore(h.writes)) = ReelsViewModel(
        repository = FeedRepository(h.api, ErrorMapper(json)) { it },
        urlResolver = resolver,
        engagement = store,
        shares = EngagementRepository(UnusedEngagementApi(), ErrorMapper(json)),
        tracker = h.tracker,
        publishActions = h.actions,
        follows = followGraph(h.graph),
        subscriptions = subscriptionGraph(h.channels),
        reelsEntry = h.entry,
        watchTracker = h.watch,
        analytics = NoOpAnalyticsRecorder,
        sounds = SoundsRepository(h.sounds, ErrorMapper(json)) { it },
        soundEntry = h.soundEntry,
        soundStore = h.soundStore,
        autoScrollStore = h.autoScrollStore,
        offline = h.offline,
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

    // ── A long video is not a reel ──────────────────────────────────────

    /**
     * Founder, 2026-09-06 (cap raised to ten minutes 2026-10-21): a video over the cap "should not appear in
     * the reels section". The client already refuses to POST one as a reel;
     * this is the other half — a row the server hands back as a `flick`
     * anyway is judged on its transcode length, not on its label.
     */
    @Test
    fun `a row longer than ten minutes does not belong in Reels`() {
        val tenMinutes = 10L * 60L * 1_000L

        assertThat(item(video().copy(durationMs = 5L * 60L * 1_000L + 1)).belongsInReels()).isTrue()
        assertThat(item(video().copy(durationMs = tenMinutes)).belongsInReels()).isTrue()
        assertThat(item(video().copy(durationMs = tenMinutes + 1)).belongsInReels()).isFalse()
    }

    @Test
    fun `a long video row never belongs in Reels and an unknown length always may`() {
        assertThat(item(video()).copy(feedContentType = "long_video").belongsInReels()).isFalse()
        // duration_ms absent (0) is "not known", and never hides a post.
        assertThat(item(video()).belongsInReels()).isTrue()
        assertThat(item().belongsInReels()).isTrue()
    }

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

    // ── Full mode ───────────────────────────────────────────────────────

    /** A double-tap toggles between the two modes; nothing else about the reel changes. */
    @Test
    fun `reels open in normal mode and a double-tap toggles full mode`() {
        val vm = viewModel()
        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)

        vm.toggleMode()
        assertThat(vm.mode.value).isEqualTo(ReelsMode.FULL)

        vm.toggleMode()
        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)
    }

    /**
     * Reels opens in normal mode, playing, EVERY time: leaving the tab in
     * full mode or paused must not bring the viewer back to a bare, still
     * video.
     */
    @Test
    fun `leaving the screen resets full mode and the pause so the next visit opens normal`() {
        val vm = viewModel()
        vm.toggleMode()
        vm.togglePaused()

        vm.resetView()

        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)
        assertThat(vm.paused.value).isFalse()
    }

    /** Resetting an already-normal screen is a no-op, not a toggle. */
    @Test
    fun `reset from normal stays normal`() {
        val vm = viewModel()

        vm.resetView()

        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)
        assertThat(vm.paused.value).isFalse()
    }

    @Test
    fun `normal mode shows every piece of chrome`() {
        assertThat(ReelsMode.NORMAL.chrome())
            .isEqualTo(ReelsChrome(showHeader = true, showRail = true, showAuthor = true, showBottomBar = true))
    }

    /** Full mode (2026-10-19) is the video alone: no description, no name, no rail, no header, no bar. */
    @Test
    fun `full mode is the video alone`() {
        assertThat(ReelsMode.FULL.chrome()).isEqualTo(
            ReelsChrome(showHeader = false, showRail = false, showAuthor = false, showBottomBar = false, showName = false),
        )
    }

    /** The description, the name and the rail come and go together. */
    @Test
    fun `the description, the name and the rail go together`() {
        ReelsMode.entries.forEach { mode ->
            assertThat(mode.chrome().showAuthor).isEqualTo(mode.chrome().showRail)
            assertThat(mode.chrome().showName).isEqualTo(mode.chrome().showRail)
        }
    }

    // ── Pause ───────────────────────────────────────────────────────────

    /** A single tap on the video holds the frame; a second one lets it go. Nothing else changes. */
    @Test
    fun `reels open playing and a single tap toggles paused`() {
        val vm = viewModel()
        assertThat(vm.paused.value).isFalse()

        vm.togglePaused()
        assertThat(vm.paused.value).isTrue()
        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)

        vm.togglePaused()
        assertThat(vm.paused.value).isFalse()
    }

    /** A pause belongs to the reel it was made on: swiping to the next reel plays it. */
    @Test
    fun `settling on another reel clears the pause`() = runTest {
        val vm = viewModel()
        vm.togglePaused()

        vm.onReelShown(item(video()))

        assertThat(vm.paused.value).isFalse()
    }

    /** A double-tap is about the frame, never the playback: full mode does not pause. */
    @Test
    fun `toggling the mode leaves the pause alone`() {
        val vm = viewModel()

        vm.toggleMode()

        assertThat(vm.paused.value).isFalse()
    }

    @Test
    fun `toggling is its own inverse`() {
        ReelsMode.entries.forEach { mode ->
            assertThat(mode.toggled()).isNotEqualTo(mode)
            assertThat(mode.toggled().toggled()).isEqualTo(mode)
        }
    }

    // ── Sound: muted until the viewer says otherwise, and then kept ─────
    //
    // founder, 2026-09-30: reels open MUTED, like the web. Once the viewer
    // turns the sound on it STAYS on — across reels, across leaving and
    // re-entering Reels, and across app restarts — until they mute again:
    // "Once user makes it on on the sound keep it on." These replace the two
    // tests that pinned the 2026-09-05 decision ("reels start unmuted and the
    // choice survives toggling", "leaving the screen keeps a mute" — a mute
    // held for the session only).

    /**
     * Held in the ViewModel, not per player — a per-player flag resets the
     * moment the pool recycles that instance, so the choice would silently
     * come undone after four swipes.
     */
    @Test
    fun `reels open muted for a viewer who never chose`() {
        val h = Harness()

        val vm = viewModel(h)

        assertThat(vm.muted.value).isTrue()
        assertThat(vm.soundChoiceRead.value).isTrue()
        // Reading the choice is not making one: nothing is written back.
        assertThat(h.soundStore.writes).isEmpty()
    }

    @Test
    fun `the rule is muted unless the sound was turned on, and an unread choice is muted`() {
        assertThat(reelsMuted(soundOn = null)).isTrue()
        assertThat(reelsMuted(soundOn = false)).isTrue()
        assertThat(reelsMuted(soundOn = true)).isFalse()
    }

    @Test
    fun `turning the sound on is stored`() {
        val h = Harness()
        val vm = viewModel(h)

        vm.toggleMuted()

        assertThat(vm.muted.value).isFalse()
        assertThat(h.soundStore.writes).containsExactly(true)
        assertThat(h.soundStore.stored.value).isTrue()
    }

    /** The restart: a ViewModel that has never seen the first one reads what it left. */
    @Test
    fun `a new ViewModel reads the choice back as on`() {
        val store = FakeSoundStore()
        viewModel(Harness(soundStore = store)).toggleMuted()

        val next = viewModel(Harness(soundStore = store))

        assertThat(next.muted.value).isFalse()
        assertThat(next.soundChoiceRead.value).isTrue()
    }

    @Test
    fun `muting again is stored too, and a new ViewModel opens muted`() {
        val store = FakeSoundStore(initial = true)
        val vm = viewModel(Harness(soundStore = store))
        assertThat(vm.muted.value).isFalse()

        vm.toggleMuted()

        assertThat(vm.muted.value).isTrue()
        assertThat(store.writes).containsExactly(false)
        assertThat(viewModel(Harness(soundStore = store)).muted.value).isTrue()
    }

    @Test
    fun `the choice survives every toggle, each one stored`() {
        val h = Harness()
        val vm = viewModel(h)

        vm.toggleMuted()
        vm.toggleMuted()
        vm.toggleMuted()

        assertThat(vm.muted.value).isFalse()
        assertThat(h.soundStore.writes).containsExactly(true, false, true).inOrder()
    }

    /** Leaving the screen resets the mode and the pause, never the sound. */
    @Test
    fun `leaving the screen keeps the sound on`() {
        val h = Harness()
        val vm = viewModel(h)
        vm.toggleMuted()

        vm.resetView()

        assertThat(vm.muted.value).isFalse()
        assertThat(h.soundStore.stored.value).isTrue()
    }

    /**
     * The first reel waits for the stored choice: a viewer who chose sound
     * must never hear a muted first reel flip on. Until the file answers the
     * reel is muted AND held; when it answers "on", it starts with sound.
     */
    @Test
    fun `until the stored choice is read reels stay muted and the first reel is held`() {
        val store = FakeSoundStore(initial = null)
        val vm = viewModel(Harness(soundStore = store))

        assertThat(vm.muted.value).isTrue()
        assertThat(vm.soundChoiceRead.value).isFalse()
        assertThat(reelMayPlay(paused = vm.paused.value, soundChoiceRead = vm.soundChoiceRead.value)).isFalse()

        store.stored.value = true

        assertThat(vm.muted.value).isFalse()
        assertThat(vm.soundChoiceRead.value).isTrue()
        assertThat(reelMayPlay(paused = vm.paused.value, soundChoiceRead = vm.soundChoiceRead.value)).isTrue()
    }

    @Test
    fun `a reel may play only when it is not paused and the choice of sound is known`() {
        assertThat(reelMayPlay(paused = false, soundChoiceRead = true)).isTrue()
        assertThat(reelMayPlay(paused = true, soundChoiceRead = true)).isFalse()
        assertThat(reelMayPlay(paused = false, soundChoiceRead = false)).isFalse()
        assertThat(reelMayPlay(paused = true, soundChoiceRead = false)).isFalse()
    }

    /** A tap that beats the read is the newer choice: the file's older answer must not undo it. */
    @Test
    fun `a tap before the stored choice is read stands, and is what is stored`() {
        val store = FakeSoundStore(initial = null)
        val vm = viewModel(Harness(soundStore = store))

        vm.toggleMuted()

        assertThat(vm.muted.value).isFalse()
        assertThat(vm.soundChoiceRead.value).isTrue()
        assertThat(store.writes).containsExactly(true)
    }

    // ── Analytics says what was true ────────────────────────────────────

    private fun uuid() = java.util.UUID.randomUUID().toString()

    private fun playing() = WatchProbe(
        playheadMs = 900L,
        isPlaying = true,
        isBuffering = false,
        renderedFirstFrame = true,
    )

    private fun watchable() = item(video().copy(durationMs = 20_000L)).copy(
        id = uuid(),
        author = FeedAuthor(id = uuid(), displayName = "Ada"),
    )

    /**
     * `play_start` used to say `is_muted: false` whatever the speaker said,
     * and never a position. It now carries the mute the view started under
     * and the pager's page, 1-based.
     */
    @Test
    fun `a view reports the real mute state and the page it sat on`() = runTest {
        val recorder = RecordingRecorder()
        val watch = VideoWatchTracker(recorder, backgroundScope, StandardTestDispatcher(testScheduler))
        val vm = viewModel(Harness(watch = watch))

        vm.onReelShown(watchable(), probe = { playing() }, page = 2)
        advanceTimeBy(1_100)

        val start = recorder.events.single { it.type == AnalyticsEventType.PLAY_START }
        assertThat(start.payload.getValue("is_muted").jsonPrimitive.boolean).isTrue()
        assertThat(start.payload.getValue("position").jsonPrimitive.int).isEqualTo(3)
    }

    @Test
    fun `a view started with the sound on reports it un-muted`() = runTest {
        val recorder = RecordingRecorder()
        val watch = VideoWatchTracker(recorder, backgroundScope, StandardTestDispatcher(testScheduler))
        val vm = viewModel(Harness(watch = watch, soundStore = FakeSoundStore(initial = true)))

        vm.onReelShown(watchable(), probe = { playing() }, page = 0)
        advanceTimeBy(1_100)

        val start = recorder.events.single { it.type == AnalyticsEventType.PLAY_START }
        assertThat(start.payload.getValue("is_muted").jsonPrimitive.boolean).isFalse()
        assertThat(start.payload.getValue("position").jsonPrimitive.int).isEqualTo(1)
    }

    @Test
    fun `the position is the page counted from one, and no page is no position`() {
        assertThat(reelPosition(0)).isEqualTo(1)
        assertThat(reelPosition(7)).isEqualTo(8)
        assertThat(reelPosition(null)).isNull()
        assertThat(reelPosition(-1)).isNull()
    }

    // ── Use this sound ──────────────────────────────────────────────────

    private val added = ReelSound(
        id = "s-added",
        title = "Original sound - Asha",
        artist = "Asha",
        startMs = 1_500L,
        durationMs = 28_400L,
        useCount = 3,
        sourcePostId = "p0",
        creatorUserId = "u0",
    )

    /** A reel that already plays an added sound offers THAT sound: the viewer hears it, the server is not asked. */
    @Test
    fun `use this sound on a reel that plays one goes to create with it, without asking the server`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onUseSound(item(video()).copy(sound = added), SoundIntent.CREATE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).isEmpty()
        assertThat(vm.soundDestination.value).isEqualTo(SoundDestination.Create)
        // The chosen sound starts at 0 in the new reel, wherever it started in this one.
        assertThat(h.soundEntry.chosen.value).isEqualTo(
            ChosenSound(id = "s-added", title = "Original sound - Asha", artist = "Asha", durationMs = 28_400L),
        )
    }

    @Test
    fun `the sound line of a reel that plays one opens that sound's page and chooses nothing`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onUseSound(item(video()).copy(sound = added), SoundIntent.PAGE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).isEmpty()
        assertThat(vm.soundDestination.value).isEqualTo(SoundDestination.Page("s-added"))
        assertThat(h.soundEntry.chosen.value).isNull()
    }

    @Test
    fun `use this sound on a reel's own audio asks the server for its sound first`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onUseSound(item(video()), SoundIntent.CREATE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).containsExactly("p")
        assertThat(vm.soundDestination.value).isEqualTo(SoundDestination.Create)
        assertThat(h.soundEntry.chosen.value?.id).isEqualTo("s-made")
        assertThat(vm.soundMessage.value).isNull()
    }

    @Test
    fun `original sound's line makes the sound and then opens its page`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onUseSound(item(video()), SoundIntent.PAGE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).containsExactly("p")
        assertThat(vm.soundDestination.value).isEqualTo(SoundDestination.Page("s-made"))
        assertThat(h.soundEntry.chosen.value).isNull()
    }

    @Test
    fun `a refusal is one line, by the server's code, and goes nowhere`() = runTest {
        val cases = mapOf(
            "SOUND_REUSE_NOT_ALLOWED" to "The creator has turned off reuse for this reel.",
            "NOT_READY" to "This reel is still processing. Try again in a moment.",
            "TOO_LONG" to "Only reels up to 5 minutes can be used as a sound.",
            "NO_AUDIO" to "This reel has no sound to use.",
            "NOT_FOUND" to "This reel is no longer available.",
            "RATE_LIMITED" to "That is a lot of sounds in one hour. Try again later.",
            "SOUND_UNAVAILABLE" to "Please try again.",
        )
        for ((code, line) in cases) {
            val h = Harness(sounds = FakeSoundsApi(refusal = code))
            val vm = viewModel(h)

            vm.onUseSound(item(video()), SoundIntent.CREATE)
            advanceUntilIdle()

            assertThat(vm.soundMessage.value?.text).isEqualTo(line)
            assertThat(vm.soundDestination.value).isNull()
            assertThat(h.soundEntry.chosen.value).isNull()

            vm.dismissSoundMessage()
            assertThat(vm.soundMessage.value).isNull()
        }
    }

    /** The row is not drawn for these; a call that arrives anyway does nothing. */
    @Test
    fun `a reel whose sound is not offered is not asked for`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onUseSound(item(video()).copy(soundReuseAllowed = false), SoundIntent.CREATE)
        vm.onUseSound(item(video()).copy(isProcessing = true), SoundIntent.CREATE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).isEmpty()
        assertThat(vm.soundDestination.value).isNull()
    }

    /** The author may always reuse their own reel's audio, whatever they set for others. */
    @Test
    fun `the viewer's own reel is offered even when reuse is turned off`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        val own = item(video()).copy(soundReuseAllowed = false, author = FeedAuthor(id = "me", displayName = "Me"))

        vm.onUseSound(own, SoundIntent.CREATE)
        advanceUntilIdle()

        assertThat(h.sounds.useRequests).containsExactly("p")
        assertThat(vm.soundDestination.value).isEqualTo(SoundDestination.Create)
    }

    @Test
    fun `a destination is taken once`() = runTest {
        val vm = viewModel()
        vm.onUseSound(item(video()).copy(sound = added), SoundIntent.PAGE)

        vm.onSoundDestinationTaken()

        assertThat(vm.soundDestination.value).isNull()
    }

    // ── The entry from a feed ───────────────────────────────────────────

    /** No feed tap: nothing to resolve, nothing fetched, nothing to scroll to. */
    @Test
    fun `with no entry the tab opens as it was left`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.resolveEntry(listOf("r1", "r2"))
        advanceUntilIdle()

        assertThat(vm.entryTarget.value).isNull()
        assertThat(h.api.postRequests).isEmpty()
    }

    /**
     * The tapped reel is already in the loaded pages: the pager scrolls to
     * it, nothing is fetched, and the request is cleared so the next visit
     * from the tab does not scroll there again.
     */
    @Test
    fun `an entry already in the pages is scrolled to, not fetched`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.entry.open("r2")
        assertThat(vm.entry.value).isEqualTo("r2")

        vm.resolveEntry(listOf("r1", "r2", "r3"))
        advanceUntilIdle()

        assertThat(vm.entryTarget.value).isEqualTo("r2")
        assertThat(h.api.postRequests).isEmpty()
        assertThat(vm.head.value).isNull()
        assertThat(h.entry.requested.value).isNull()
        assertThat(vm.entry.value).isNull()

        vm.onEntryShown()
        assertThat(vm.entryTarget.value).isNull()
    }

    /**
     * The tapped reel is NOT in the pages (Home is chronological, Reels is
     * ranked): it is fetched by id and pinned as the head, so it shows first
     * with the ranked reels after it, and the pager is sent to page 0.
     */
    @Test
    fun `an entry absent from the pages is fetched and shown first`() = runTest {
        val posted = FeedItemDto(
            id = "from-feed",
            postType = "video",
            media = listOf(FeedMediaDto(mediaId = "m1", kind = "video", hlsUrl = "/v1/media/m1/hls/master.m3u8")),
        )
        val h = Harness(api = RecordingApi(post = posted))
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.entry.open("from-feed")

        vm.resolveEntry(listOf("r1", "r2"))
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("from-feed")
        val head = vm.head.value as ReelsHead.Live
        assertThat(head.item.id).isEqualTo("from-feed")
        assertThat(vm.entryTarget.value).isEqualTo("from-feed")
        assertThat(h.entry.requested.value).isNull()
        // The publish tracker was never involved: nothing to dismiss.
        assertThat(h.actions.calls).isEmpty()
    }

    /** A fetch that fails leaves nothing to scroll to; the tab simply opens. The request is still spent. */
    @Test
    fun `an entry that cannot be fetched is dropped`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.entry.open("gone")

        vm.resolveEntry(emptyList())
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("gone")
        assertThat(vm.entryTarget.value).isNull()
        assertThat(vm.head.value).isNull()
        assertThat(h.entry.requested.value).isNull()
    }

    // ── Offline copies (2026-10-02) ─────────────────────────────────────

    /** Copies "on this device", scripted: what is playable right now, by post. */
    private class StoredCopies(vararg copies: OfflineCopy) : OfflineLibrary {
        val held = copies.associateBy { it.postId }.toMutableMap()
        override val state = MutableStateFlow(OfflineState(loaded = true))
        override val notices = MutableSharedFlow<String>()
        override val wifiOnly = MutableStateFlow(true)
        override suspend fun setWifiOnly(enabled: Boolean) = Unit
        override suspend fun ensureLoaded() = Unit
        override suspend fun save(item: FeedItem): OfflineSaveResult = OfflineSaveResult.Started
        override suspend fun remove(postId: String) {
            held -= postId
        }
        override suspend fun removeAll() = Unit
        override suspend fun refresh(force: Boolean) = Unit
        override fun playable(postId: String): OfflineCopy? = held[postId]
    }

    private fun storedCopy(postId: String = "p", withSound: Boolean = false) = OfflineCopy(
        postId = postId,
        kind = OfflineKind.REEL,
        title = "Kept reel",
        channelName = "Ada",
        durationMs = 28_400L,
        expiresAtMs = Long.MAX_VALUE,
        recheckAfterSeconds = 172_800L,
        grantedAtMs = 0L,
        lastCheckedAtMs = 0L,
        stored = true,
        video = OfflineStream("$postId/video", "http://127.0.0.1:8080/v1/media/m/serve/480p", "video/mp4", 100L),
        sound = if (withSound) {
            OfflineSound(
                stream = OfflineStream("$postId/sound", "http://127.0.0.1:8080/v1/audio/s1/serve", "audio/mp4", 0L),
                startMs = 1_500L,
                soundId = "s1",
            )
        } else {
            null
        },
    )

    private val addedSound = ReelSound(
        id = "s1",
        title = "Monsoon",
        artist = "Ada",
        startMs = 1_500L,
        durationMs = 28_400L,
        useCount = 0,
        sourcePostId = null,
        creatorUserId = null,
    )

    @Test
    fun `a reel with a copy on the device plays the copy, not the network`() {
        val vm = viewModel(Harness(offline = StoredCopies(storedCopy())))
        val reel = item(video())

        val playback = vm.playback(reel)!!

        assertThat(playback.kind).isEqualTo(PlaybackKind.Offline)
        assertThat(playback.cacheKey).isEqualTo("p/video")
    }

    @Test
    fun `a reel with no copy plays from the network, as before`() {
        val vm = viewModel(Harness(offline = StoredCopies()))
        val reel = item(video())

        assertThat(vm.playback(reel)!!.kind).isEqualTo(PlaybackKind.Hls)
    }

    /** The pool re-prepares a page whose playback changes; a swap would restart the reel from the top. */
    @Test
    fun `a copy that finishes saving mid-visit does not swap the source under the reel`() {
        val copies = StoredCopies()
        val vm = viewModel(Harness(offline = copies))
        val reel = item(video())
        val before = vm.playback(reel)

        copies.held["p"] = storedCopy()

        assertThat(vm.playback(reel)).isEqualTo(before)
        assertThat(vm.playback(reel)!!.kind).isEqualTo(PlaybackKind.Hls)
    }

    @Test
    fun `a copy removed mid-visit sends the reel back to the network`() {
        val copies = StoredCopies(storedCopy())
        val vm = viewModel(Harness(offline = copies))
        val reel = item(video())
        assertThat(vm.playback(reel)!!.kind).isEqualTo(PlaybackKind.Offline)

        copies.held.clear()

        assertThat(vm.playback(reel)!!.kind).isEqualTo(PlaybackKind.Hls)
    }

    /** No network, or a post the server no longer serves: the copy kept on the device still opens. */
    @Test
    fun `an entry that cannot be fetched opens from the copy kept on the device`() = runTest {
        val h = Harness(offline = StoredCopies(storedCopy("kept")))
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.entry.open("kept")

        vm.resolveEntry(emptyList())
        advanceUntilIdle()

        val head = vm.head.value as ReelsHead.Live
        assertThat(head.item.id).isEqualTo("kept")
        assertThat(head.item.title).isEqualTo("Kept reel")
        assertThat(vm.entryTarget.value).isEqualTo("kept")
        assertThat(vm.playback(head.item)!!.kind).isEqualTo(PlaybackKind.Offline)
    }

    @Test
    fun `a stored reel's added sound is played from the device, where the row says it starts`() {
        val vm = viewModel(Harness(offline = StoredCopies(storedCopy(withSound = true))))
        val reel = item(video()).copy(sound = addedSound)

        val track = vm.soundTrack(reel)!!

        assertThat(track.id).isEqualTo("s1")
        assertThat(track.startMs).isEqualTo(1_500L)
        assertThat(track.stored!!.kind).isEqualTo(PlaybackKind.Offline)
        assertThat(track.stored!!.cacheKey).isEqualTo("p/sound")
    }

    @Test
    fun `a sound is played from the network when the reel has no copy, and not at all when the row has none`() {
        val online = viewModel(Harness(offline = StoredCopies()))
        val stored = viewModel(Harness(offline = StoredCopies(storedCopy(withSound = true))))

        assertThat(online.soundTrack(item(video()).copy(sound = addedSound))!!.stored).isNull()
        // The row decides WHETHER a sound plays; the copy only decides where it is read from.
        assertThat(stored.soundTrack(item(video()))).isNull()
    }

    /** An entry that is already the head — tapped twice from the feed — is a scroll to page 0, no fetch. */
    @Test
    fun `an entry that is already the head is not fetched again`() = runTest {
        val posted = FeedItemDto(id = "from-feed", postType = "video")
        val h = Harness(api = RecordingApi(post = posted))
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.entry.open("from-feed")
        vm.resolveEntry(emptyList())
        advanceUntilIdle()
        vm.onEntryShown()

        h.entry.open("from-feed")
        vm.resolveEntry(emptyList())
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("from-feed")
        assertThat(vm.entryTarget.value).isEqualTo("from-feed")
    }

    // ── The page an entry sits on ───────────────────────────────────────

    @Test
    fun `the head is page 0 and the ranked reels shift past it`() {
        assertThat(entryPage("h", headId = "h", rankedIds = listOf("a", "b"))).isEqualTo(0)
        assertThat(entryPage("b", headId = "h", rankedIds = listOf("a", "b"))).isEqualTo(2)
    }

    @Test
    fun `without a head the ranked index is the page`() {
        assertThat(entryPage("b", headId = null, rankedIds = listOf("a", "b"))).isEqualTo(1)
    }

    @Test
    fun `a reel the pager does not hold has no page`() {
        assertThat(entryPage("z", headId = "h", rankedIds = listOf("a", "b"))).isNull()
        assertThat(entryPage("z", headId = null, rankedIds = emptyList())).isNull()
    }

    // ── Quality ─────────────────────────────────────────────────────────

    /**
     * The more sheet's pick is a SESSION setting: it outlives the reel it
     * was made on and the reset that leaving the screen does, because a
     * viewer who chose 360p on a thin connection wants every reel at 360p.
     */
    @Test
    fun `reels start on auto and a picked quality survives leaving the screen`() {
        val vm = viewModel()
        assertThat(vm.quality.value).isEqualTo(UsReelQuality.Auto)

        vm.selectQuality(UsReelQuality.Height(360))
        vm.toggleMode()
        vm.resetView()

        assertThat(vm.quality.value).isEqualTo(UsReelQuality.Height(360))
        assertThat(vm.mode.value).isEqualTo(ReelsMode.NORMAL)

        vm.selectQuality(UsReelQuality.Auto)
        assertThat(vm.quality.value).isEqualTo(UsReelQuality.Auto)
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

    // ── Refresh (founder, 2026-10-21) ───────────────────────────────────

    /** What the screen shows: the presenter `LazyPagingItems` is built on, so a kept list is really kept. */
    private class ShownReels : PagingDataPresenter<FeedItem>() {
        override suspend fun presentPagingDataEvent(event: PagingDataEvent<FeedItem>) = Unit

        val ids: List<String> get() = snapshot().items.map { it.id }
    }

    private fun TestScope.show(vm: ReelsViewModel): ShownReels {
        val shown = ShownReels()
        backgroundScope.launch { vm.items.collectLatest(shown::collectFrom) }
        return shown
    }

    /**
     * Lets the paging chain run. A refresh resumes it on the unconfined test
     * Main, and those resumptions wait on the test thread's event loop until
     * the test body really suspends: neither `advanceUntilIdle` from the
     * running body nor a `yield` (the body is runnable again at once) gets
     * there. A one-millisecond virtual delay does.
     */
    private suspend fun TestScope.settle() {
        delay(1)
        advanceUntilIdle()
    }

    private fun reelDto(id: String, authorId: String) =
        FeedItemDto(id = id, authorId = authorId, author = FeedAuthorDto(id = authorId), postType = "video")

    /**
     * A pull at the first reel: the feed is asked again from its first page
     * (no cursor), the fresh batch replaces the reels, and the pager is sent
     * to the top. The screen's first load is not a refresh and sends nothing.
     */
    @Test
    fun `a refresh reloads from the first page, replaces the reels and goes to the top`() = runTest {
        val h = Harness(api = RecordingApi(feedPages = listOf(listOf("r1", "r2"), listOf("n1", "n2", "n3"))))
        val vm = viewModel(h)
        val shown = show(vm)
        settle()
        assertThat(shown.ids).containsExactly("r1", "r2").inOrder()
        assertThat(vm.scrollToTop.value).isFalse()

        vm.refresh()
        settle()

        assertThat(h.api.cursors).containsExactly(null, null)
        assertThat(shown.ids).containsExactly("n1", "n2", "n3").inOrder()
        assertThat(vm.scrollToTop.value).isTrue()
        assertThat(vm.refreshing.value).isFalse()
        assertThat(vm.refreshFailure.value).isNull()

        vm.onScrolledToTop()
        assertThat(vm.scrollToTop.value).isFalse()
    }

    /** While the fresh batch is on the wire the reels on screen stay, and nothing moves the pager. */
    @Test
    fun `the reels stay on screen until the fresh batch is in`() = runTest {
        val h = Harness(api = RecordingApi(feedPages = listOf(listOf("r1"), listOf("n1"))))
        val vm = viewModel(h)
        val shown = show(vm)
        settle()
        val gate = CompletableDeferred<Unit>()
        h.api.gate = gate

        vm.refresh()
        settle()

        assertThat(vm.refreshing.value).isTrue()
        assertThat(shown.ids).containsExactly("r1")
        assertThat(vm.scrollToTop.value).isFalse()

        gate.complete(Unit)
        settle()

        assertThat(shown.ids).containsExactly("n1")
        assertThat(vm.refreshing.value).isFalse()
        assertThat(vm.scrollToTop.value).isTrue()
    }

    /**
     * A refresh that fails never empties the screen: the old reels stay,
     * the pager stays where it is, and one line says so. The next pull
     * still works.
     */
    @Test
    fun `a failed refresh keeps the reels and says so`() = runTest {
        val h = Harness(api = RecordingApi(feedPages = listOf(listOf("r1", "r2"), null, listOf("n1"))))
        val vm = viewModel(h)
        val shown = show(vm)
        settle()

        vm.refresh()
        settle()

        assertThat(shown.ids).containsExactly("r1", "r2").inOrder()
        assertThat(vm.refreshFailure.value).isNotNull()
        assertThat(vm.scrollToTop.value).isFalse()
        assertThat(vm.refreshing.value).isFalse()

        vm.dismissRefreshFailure()
        assertThat(vm.refreshFailure.value).isNull()

        vm.refresh()
        settle()

        assertThat(shown.ids).containsExactly("n1")
        assertThat(vm.scrollToTop.value).isTrue()
    }

    /** A second pull, or a tab tap, while a refresh is on the wire is the same wish: one request. */
    @Test
    fun `a refresh while one is running is ignored`() = runTest {
        val h = Harness(api = RecordingApi(feedPages = listOf(listOf("r1"), listOf("n1"), listOf("x1"))))
        val vm = viewModel(h)
        val shown = show(vm)
        settle()
        val gate = CompletableDeferred<Unit>()
        h.api.gate = gate

        vm.refresh()
        settle()
        vm.refresh()
        settle()
        gate.complete(Unit)
        settle()

        assertThat(h.api.cursors).hasSize(2)
        assertThat(shown.ids).containsExactly("n1")
    }

    /**
     * A reel a feed tap pinned above the ranked reels goes with the old
     * batch: the viewer asked for new reels, not the one they were on.
     */
    @Test
    fun `a refresh lets go of a reel a feed tap pinned`() = runTest {
        val api = RecordingApi(post = reelDto("from-feed", authorId = "a"), feedPages = listOf(listOf("r1"), listOf("n1")))
        val h = Harness(api = api)
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        show(vm)
        h.entry.open("from-feed")
        vm.resolveEntry(listOf("r1"))
        settle()
        assertThat(vm.head.value).isInstanceOf(ReelsHead.Live::class.java)

        vm.refresh()
        settle()

        assertThat(vm.head.value).isNull()
    }

    /** The viewer's OWN just-posted reel stays pinned: until the ranked feed carries it, that slot is where it shows. */
    @Test
    fun `a refresh keeps the viewer's own pinned reel`() = runTest {
        val api = RecordingApi(post = reelDto("mine", authorId = "me"), feedPages = listOf(listOf("r1"), listOf("n1")))
        val h = Harness(api = api)
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        show(vm)
        h.entry.open("mine")
        vm.resolveEntry(listOf("r1"))
        settle()

        vm.refresh()
        settle()

        assertThat((vm.head.value as ReelsHead.Live).item.id).isEqualTo("mine")
    }

    // ── Follow ──────────────────────────────────────────────────────────

    /**
     * A reel that carries its author's channel learns the SUBSCRIPTION edge
     * beside the follow, and its pill subscribes rather than follows: the
     * server makes the follow inside the subscribe, so no follow request
     * of the client's own goes out.
     */
    @Test
    fun `a shown reel with a channel learns its subscription and a subscribe is sent`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        val reel = item().copy(channel = FeedChannel(userId = "chan", name = "Ada's channel", handle = "ada"))

        vm.onReelShown(reel)
        advanceUntilIdle()

        assertThat(h.channels.subscriptionReads).containsExactly("chan")
        assertThat(h.graph.relationshipRequests).containsExactly("me" to "a")
        assertThat(vm.subscriptionEdges.value["chan"]).isEqualTo(ChannelSubscription.NOT_SUBSCRIBED)

        vm.onSubscribe("chan")
        advanceUntilIdle()

        assertThat(h.channels.subscribeRequests).containsExactly("chan")
        assertThat(h.graph.followRequests).isEmpty()
        assertThat(vm.subscriptionEdges.value["chan"]?.subscribed).isTrue()
    }

    /** A reel without a channel never asks the channel routes: there is nothing there to subscribe to. */
    @Test
    fun `a shown reel without a channel leaves the subscription graph alone`() = runTest {
        val h = Harness()
        val vm = viewModel(h)

        vm.onReelShown(item())
        advanceUntilIdle()

        assertThat(h.channels.subscriptionReads).isEmpty()
        assertThat(vm.subscriptionEdges.value).isEmpty()
    }

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
            h.tracker.update("key-1", state)
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
        h.tracker.update("key-1", ReelPublishState.Uploading(0.4f))

        h.tracker.update("key-1", ReelPublishState.Failed("Couldn't reach the server.", retryable = true))
        advanceUntilIdle()

        val head = vm.head.value as ReelsHead.Pending
        assertThat(head.coverPath).isEqualTo("/cache/key-1.jpg")
        assertThat(head.failure).isEqualTo(PendingFailure("Couldn't reach the server.", retryable = true))

        vm.retryPublish()
        vm.discardPublish()
        assertThat(h.actions.calls).containsExactly("retry:key-1", "discard:key-1").inOrder()
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

        h.tracker.update("key-1", ReelPublishState.Uploading(0.4f))
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
        h.tracker.update("key-1", ReelPublishState.Posting)

        h.tracker.update("key-1", ReelPublishState.Published("post-9"))
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("post-9")
        val head = vm.head.value as ReelsHead.Live
        assertThat(head.item.id).isEqualTo("post-9")
        assertThat(vm.playback(head.item)?.kind).isEqualTo(PlaybackKind.Progressive)
        assertThat(h.actions.calls).containsExactly("dismiss:key-1")
    }

    /** The post exists even when it cannot be read back yet; the loader must not outlive the publish. */
    @Test
    fun `a published post that cannot be fetched is let go rather than held as pending`() = runTest {
        val h = Harness()
        val vm = viewModel(h)
        backgroundScope.launch { vm.head.collect {} }
        h.tracker.setPreview(preview)

        h.tracker.update("key-1", ReelPublishState.Published("post-9"))
        advanceUntilIdle()

        assertThat(h.api.postRequests).containsExactly("post-9", "post-9", "post-9")
        assertThat(h.actions.calls).containsExactly("dismiss:key-1")
    }

    // ── Save on the rail (2026-10-02) ───────────────────────────────────

    /** A server for the rail's writes that keeps its truth and can refuse. */
    private class RailServer : EngagementWrites {
        var saved = false
        var liked = false
        var refuse = false
        val calls = mutableListOf<String>()

        private fun answer(name: String, apply: () -> Unit): AppResult<Unit> {
            calls += name
            if (refuse) return AppResult.Failure(com.us.android.core.common.error.AppError.NoNetwork())
            apply()
            return AppResult.Success(Unit)
        }

        override suspend fun react(postId: String, reaction: String) = answer("POST reactions") { liked = true }
        override suspend fun unreact(postId: String) = answer("DELETE reactions") { liked = false }
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) =
            answer(if (bookmarked) "POST bookmark" else "DELETE bookmark") { saved = bookmarked }
        override suspend fun repost(postId: String) = answer("POST repost") {}
        override suspend fun removeRepost(postId: String) = answer("DELETE repost") {}
    }

    @Test
    fun `saving a reel lights the rail, reaches the server, and a second tap removes it`() {
        val server = RailServer()
        val viewModel = viewModel(Harness(writes = server))

        viewModel.onBookmark("p", serverBookmarked = false)

        assertThat(viewModel.overlays.value["p"]?.bookmarked).isTrue()
        assertThat(server.saved).isTrue()
        assertThat(viewModel.engagementMessage.value).isNull()

        viewModel.onBookmark("p", serverBookmarked = false)

        assertThat(viewModel.overlays.value["p"]?.bookmarked).isFalse()
        assertThat(server.saved).isFalse()
        assertThat(server.calls).containsExactly("POST bookmark", "DELETE bookmark").inOrder()
    }

    /** Before 2026-10-02 this rollback was silent: the glyph went back and the reel said nothing. */
    @Test
    fun `a refused save puts the rail back and says so over the reel`() {
        val server = RailServer().apply { refuse = true }
        val viewModel = viewModel(Harness(writes = server))

        viewModel.onBookmark("p", serverBookmarked = false)

        assertThat(viewModel.overlays.value["p"]?.bookmarked).isFalse()
        assertThat(viewModel.engagementMessage.value?.text).isEqualTo("Couldn't save this reel. Try again.")

        viewModel.dismissEngagementMessage()
        assertThat(viewModel.engagementMessage.value).isNull()
    }

    @Test
    fun `a refused like puts the heart back and says so`() {
        val server = RailServer().apply { refuse = true }
        val viewModel = viewModel(Harness(writes = server))

        viewModel.onReact("p", serverReacted = false)

        assertThat(viewModel.overlays.value["p"]?.reacted).isFalse()
        assertThat(viewModel.engagementMessage.value?.text).isEqualTo("Couldn't save your like. Try again.")
    }

    /**
     * The reel is saved, Reels is left, and the same reel comes round again
     * from a page that still says "not saved": a second screen over the same
     * store shows it saved, and its tap un-saves rather than saving twice.
     */
    @Test
    fun `a saved reel is still saved when the reels screen is opened again`() {
        val server = RailServer()
        val store = EngagementStore(server)
        val first = viewModel(Harness(), store)
        first.onBookmark("p", serverBookmarked = false)

        val reopened = viewModel(Harness(), store)

        assertThat(reopened.overlays.value["p"]?.bookmarked).isTrue()
        reopened.onBookmark("p", serverBookmarked = false)
        assertThat(server.saved).isFalse()
        assertThat(server.calls).containsExactly("POST bookmark", "DELETE bookmark").inOrder()
    }

    @Test
    fun `each rail action is worded by what it was`() {
        assertThat(reelEngagementRefusal(EngagementAction.BOOKMARK)).isEqualTo("Couldn't save this reel. Try again.")
        assertThat(reelEngagementRefusal(EngagementAction.REACTION)).isEqualTo("Couldn't save your like. Try again.")
        assertThat(reelEngagementRefusal(EngagementAction.REPOST)).isEqualTo("Couldn't repost that. Try again.")
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

    // ── The player's rows (T-32) ────────────────────────────────────────

    /** Speed is a SESSION choice, like quality: it outlives the reel it was made on and leaving the screen. */
    @Test
    fun `reels start at 1x and a picked speed is held for the session`() {
        val vm = viewModel()
        assertThat(vm.speed.value).isEqualTo(SPEED_NORMAL)

        vm.selectSpeed(SPEED_QUICKER)
        vm.onReelShown(item(video()))
        vm.resetView()

        assertThat(vm.speed.value).isEqualTo(SPEED_QUICKER)
    }

    /** What every page's player is set to: the session's speed, on every page alike. */
    @Test
    fun `the picked speed is what each player is set to`() {
        val vm = viewModel()
        vm.selectSpeed(SPEED_QUICKER)

        val settings = reelPlayerSettings(
            speed = vm.speed.value,
            autoScroll = vm.autoScroll.value,
            captionLanguage = vm.captionLanguage.value,
            offeredLanguages = emptyList(),
        )

        assertThat(settings.speed).isEqualTo(SPEED_QUICKER)
        assertThat(settings.loops).isTrue()
        assertThat(settings.captionLanguage).isNull()
    }

    @Test
    fun `a reel from the network has no caption tracks, so the sheet has no captions row`() {
        assertThat(viewModel().captions(item(video()))).isEmpty()
        assertThat(reelCaptions(null)).isEmpty()
    }

    @Test
    fun `a stored copy's caption tracks are the reel's captions`() {
        val stored = Playback.offline(
            url = "https://cdn/r.mp4",
            cacheKey = "k",
            captions = listOf(PlaybackCaption(uri = "file:///c/en.vtt", language = "en", label = "English")),
        )

        assertThat(reelCaptions(stored)).containsExactly(UsCaptionOption(language = "en", label = "English"))
    }

    /** The long video's rule: a choice is carried to a reel with the same language, off for one without. */
    @Test
    fun `a caption choice is carried only to a reel that has that language`() {
        val vm = viewModel()
        vm.selectCaption("en")

        // A reel from the network has no tracks: captions go off, they never come back on by themselves.
        vm.onReelShown(item(video()))

        assertThat(vm.captionLanguage.value).isNull()
        assertThat(reelPlayerSettings(SPEED_NORMAL, false, "en", listOf("en")).captionLanguage).isEqualTo("en")
        assertThat(reelPlayerSettings(SPEED_NORMAL, false, "en", listOf("hi")).captionLanguage).isNull()
    }

    @Test
    fun `auto scroll is off for a viewer who never chose, and nothing is written`() {
        val h = Harness()

        val vm = viewModel(h)

        assertThat(vm.autoScroll.value).isFalse()
        assertThat(h.autoScrollStore.writes).isEmpty()
    }

    @Test
    fun `turning auto scroll on is stored, and a new ViewModel reads it back on`() {
        val store = FakeAutoScrollStore()
        viewModel(Harness(autoScrollStore = store)).toggleAutoScroll()

        assertThat(store.writes).containsExactly(true)
        assertThat(viewModel(Harness(autoScrollStore = store)).autoScroll.value).isTrue()
    }

    /** A tap that beats the read is the newer choice: the file's older answer must not undo it. */
    @Test
    fun `an auto scroll tap before the stored choice is read stands`() {
        val store = FakeAutoScrollStore(initial = null)
        val vm = viewModel(Harness(autoScrollStore = store))

        vm.toggleAutoScroll()
        store.stored.value = false

        assertThat(vm.autoScroll.value).isTrue()
    }

    /** Off, a reel loops and never ends; on, it ends and the pager moves on — to the next page only. */
    @Test
    fun `the pager advances on a reel's end only when auto scroll is on and there is a next reel`() {
        assertThat(autoScrollTarget(autoScroll = false, current = 0, pageCount = 5)).isNull()
        assertThat(autoScrollTarget(autoScroll = true, current = 0, pageCount = 5)).isEqualTo(1)
        assertThat(autoScrollTarget(autoScroll = true, current = 3, pageCount = 5)).isEqualTo(4)
        // The last reel loaded has nowhere to go: it plays again.
        assertThat(autoScrollTarget(autoScroll = true, current = 4, pageCount = 5)).isNull()
        assertThat(autoScrollTarget(autoScroll = true, current = 0, pageCount = 0)).isNull()
    }

    @Test
    fun `players loop unless auto scroll is on`() {
        assertThat(reelPlayerSettings(SPEED_NORMAL, autoScroll = false, null, emptyList()).loops).isTrue()
        assertThat(reelPlayerSettings(SPEED_NORMAL, autoScroll = true, null, emptyList()).loops).isFalse()
    }

    /** founder, 2026-09-30: reels open MUTED. Auto scroll, a speed or captions never change that. */
    @Test
    fun `the player's rows leave the muted start alone`() {
        val h = Harness(autoScrollStore = FakeAutoScrollStore(initial = true))
        val vm = viewModel(h)

        vm.selectSpeed(SPEED_QUICKER)
        vm.selectCaption("en")
        vm.toggleAutoScroll()

        assertThat(vm.muted.value).isTrue()
        assertThat(h.soundStore.writes).isEmpty()
    }
}
