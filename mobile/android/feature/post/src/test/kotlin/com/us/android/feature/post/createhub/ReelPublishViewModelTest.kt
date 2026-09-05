package com.us.android.feature.post.createhub

import android.graphics.Bitmap
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.ChannelApi
import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.CreateChannelRequest
import com.us.android.core.feed.data.HandleAvailabilityDto
import com.us.android.core.feed.data.UpdateChannelRequest
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.ByteArrayInputStream

/**
 * The reel form: what it carries into the pending publish it hands over,
 * the cover it writes first, and the channel gate. The wire mapping of the
 * record is [ReelPublishPipelineTest]'s.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelPublishViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    // ── Fakes ───────────────────────────────────────────────────────────

    private class FakeLauncher : ReelPublishLauncher {
        val enqueued = mutableListOf<PendingReelPublish>()
        override suspend fun enqueue(pending: PendingReelPublish) {
            enqueued += pending
        }
    }

    private class FakeFiles : ReelPublishFiles {
        val covers = mutableListOf<ByteArray>()
        var refuseCover = false
        override suspend fun stashVideo(uri: String, creationKey: String): StashedVideo? = null
        override fun exportTarget(creationKey: String): String = "/cache/$creationKey.video"
        override suspend fun writeCover(bytes: ByteArray, creationKey: String): String? {
            if (refuseCover) return null
            covers += bytes
            return "/cache/$creationKey.jpg"
        }

        override fun openVideo(path: String, mimeType: String): PickedMedia =
            PickedMedia(path, mimeType, 1, UploadSource { ByteArrayInputStream(ByteArray(1)) })

        override suspend fun readBytes(path: String): ByteArray? = null
        override suspend fun delete(paths: List<String?>) = Unit
    }

    private class FakeLookups(
        var categories: List<ReelCategory>? = null,
        var people: List<TaggedUser> = emptyList(),
        var hashtags: List<String> = emptyList(),
    ) : ReelLookups {
        val queries = mutableListOf<String>()
        val hashtagQueries = mutableListOf<String>()
        override suspend fun categories(): List<ReelCategory>? = categories
        override suspend fun searchPeople(query: String): List<TaggedUser> {
            queries += query
            return people
        }
        override suspend fun suggestHashtags(query: String): List<String> {
            hashtagQueries += query
            return hashtags
        }
    }

    /** `v1/channels/me`: a channel, or a 404 — the two answers the gate reads. */
    private class FakeChannelApi(var hasChannel: Boolean = true) : ChannelApi {
        var meCalls = 0
        override suspend fun create(body: CreateChannelRequest): ApiEnvelope<ChannelDto> = error("not under test")
        override suspend fun me(): ApiEnvelope<ChannelDto> {
            meCalls++
            if (!hasChannel) throw HttpException(Response.error<Any>(NOT_FOUND, "".toResponseBody()))
            return ApiEnvelope(ChannelDto(userId = "me", name = "Ada", handle = "ada"))
        }
        override suspend fun update(body: UpdateChannelRequest): ApiEnvelope<ChannelDto> = error("not under test")
        override suspend fun get(key: String): ApiEnvelope<ChannelDto> = error("not under test")
        override suspend fun handleAvailable(handle: String): ApiEnvelope<HandleAvailabilityDto> =
            error("not under test")
    }

    /** The strip's thumbnails; whether they carry a bitmap decides whether a fallback cover exists. */
    private fun frames(withBitmaps: Boolean) = ReelFrameExtractor { _, count ->
        Filmstrip.timestampsUs(TEN_SECONDS_US, count).mapIndexed { index, timeUs ->
            CoverFrame(index = index, timeUs = timeUs, bitmap = if (withBitmaps) mockk<Bitmap>() else null)
        }
    }

    /** The exact-frame seeker: a frame for every instant, or none at all. */
    private fun seeker(answers: Boolean) = ReelFrameSeeker { _, _ -> if (answers) mockk<Bitmap>() else null }

    private val images = ReelCoverImageLoader { _, _ -> mockk<Bitmap>() }

    /** The encoded bytes carry the frame index in their length, so the test can tell frames apart. */
    private val encoder = ReelCoverEncoder { frame -> frame.bitmap?.let { ByteArray(frame.index + 1) } }

    /** What the probe says about every picked video; null values are "could not tell". */
    private fun probe(durationMs: Long? = 10_000L, sizeBytes: Long? = 20L * 1024 * 1024) =
        ReelVideoProbe { VideoProbe(durationMs = durationMs, sizeBytes = sizeBytes) }

    private fun channels(api: FakeChannelApi = FakeChannelApi()) =
        ChannelRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true }))

    private fun viewModel(
        launcher: FakeLauncher = FakeLauncher(),
        files: FakeFiles = FakeFiles(),
        lookups: FakeLookups = FakeLookups(),
        frames: ReelFrameExtractor = frames(withBitmaps = true),
        seeker: ReelFrameSeeker = seeker(answers = true),
        probe: ReelVideoProbe = probe(),
        channels: ChannelRepository = channels(),
        surface: CreateSurface = CreateSurface.Reel,
    ) = ReelPublishViewModel(
        launcher = launcher,
        files = files,
        encoder = encoder,
        frames = frames,
        seeker = seeker,
        images = images,
        lookups = lookups,
        probe = probe,
        channels = channels,
        io = Dispatchers.Unconfined,
        savedStateHandle = SavedStateHandle(mapOf(ReelPublishViewModel.SURFACE_ARG to surface.routeKey)),
    )

    private fun ReelPublishViewModel.pickAndPost(caption: String = "") {
        onVideoPicked("content://video/1")
        onCaptionChanged(caption)
        onPost()
    }

    /** Opens the picker, drags to the strip's [stripIndex]th frame, confirms. */
    private suspend fun ReelPublishViewModel.chooseFrame(stripIndex: Int) {
        openCoverPicker()
        onScrub(state.value.frames[stripIndex].timeUs)
        kotlinx.coroutines.delay(SETTLE_MILLIS)
        confirmCover()
    }

    // ── Hand-off ────────────────────────────────────────────────────────

    @Test
    fun `post hands the whole form over and reports enqueued so the surface can close`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.pickAndPost("Sunday skate #longboard with @maya")
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.creationKey).isNotEmpty()
        assertThat(pending.videoUri).isEqualTo("content://video/1")
        assertThat(pending.caption).isEqualTo("Sunday skate #longboard with @maya")
        assertThat(pending.coverPath).isEqualTo("/cache/${pending.creationKey}.jpg")
        assertThat(pending.visibility).isEqualTo(VISIBILITY_PUBLIC)
        assertThat(pending.allowComments).isTrue()
        assertThat(pending.hideShare).isFalse()
        assertThat(pending.allowDownload).isTrue()
        assertThat(pending.allowRemix).isTrue()
        assertThat(pending.category).isEmpty()
        assertThat(pending.taggedUserIds).isEmpty()
        assertThat(pending.locationName).isEmpty()
        assertThat(pending.confirmedVideoId).isNull()
        assertThat(vm.state.value.phase).isEqualTo(ReelPublishViewModel.Phase.Enqueued)
        assertThat(vm.state.value.isBusy).isTrue()
    }

    @Test
    fun `flipping every switch is carried into the pending record`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onAllowCommentsChanged(false)
        vm.onHideShareChanged(true)
        vm.onAllowDownloadChanged(false)
        vm.onAllowRemixChanged(false)
        vm.pickAndPost()
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.allowComments).isFalse()
        assertThat(pending.hideShare).isTrue()
        assertThat(pending.allowDownload).isFalse()
        assertThat(pending.allowRemix).isFalse()
    }

    @Test
    fun `audience maps to visibility and unknown values are refused`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onVisibilityChanged(VISIBILITY_FOLLOWERS)
        assertThat(vm.state.value.visibility).isEqualTo(VISIBILITY_FOLLOWERS)
        vm.onVisibilityChanged("unlisted")
        assertThat(vm.state.value.visibility).isEqualTo(VISIBILITY_FOLLOWERS)
        vm.onVisibilityChanged(VISIBILITY_PRIVATE)
        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(launcher.enqueued.single().visibility).isEqualTo(VISIBILITY_PRIVATE)
    }

    @Test
    fun `a chosen category is carried and a new video starts a new post with a new key`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onCategoryChanged("comedy")
        vm.pickAndPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued.last().category).isEqualTo("comedy")

        vm.onVideoPicked("content://video/2")
        vm.onCategoryChanged("")
        vm.onPost()
        advanceUntilIdle()

        assertThat(launcher.enqueued).hasSize(2)
        assertThat(launcher.enqueued.last().category).isEmpty()
        assertThat(launcher.enqueued.map { it.creationKey }.distinct()).hasSize(2)
    }

    @Test
    fun `the server category list replaces the fallback when it loads`() = runTest {
        val loaded = listOf(ReelCategory("skits", "Skits"))
        val vm = viewModel(lookups = FakeLookups(categories = loaded))
        advanceUntilIdle()

        assertThat(vm.state.value.categories).isEqualTo(loaded)
    }

    @Test
    fun `the fallback list stays when the categories endpoint is unavailable`() = runTest {
        val vm = viewModel(lookups = FakeLookups(categories = null))
        advanceUntilIdle()

        assertThat(vm.state.value.categories).isEqualTo(FallbackReelCategories)
        assertThat(vm.state.value.categories.map { it.id }).contains("comedy")
    }

    @Test
    fun `tagged people and a location are carried`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onTagUser(TaggedUser("u-1", "Maya", "maya"))
        vm.onTagUser(TaggedUser("u-2", "Ravi", "ravi"))
        vm.onTagUser(TaggedUser("u-1", "Maya again", "maya")) // a duplicate is ignored
        vm.onUntagUser("u-2")
        vm.onTagUser(TaggedUser("u-3", "Zed", ""))
        vm.onLocationChanged("  Marina Beach  ")
        vm.pickAndPost()
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.taggedUserIds).containsExactly("u-1", "u-3").inOrder()
        assertThat(pending.locationName).isEqualTo("  Marina Beach  ")
    }

    @Test
    fun `no more than twenty people can be tagged`() = runTest {
        val vm = viewModel()

        repeat(25) { vm.onTagUser(TaggedUser("u-$it", "Person $it", "p$it")) }

        assertThat(vm.state.value.taggedUsers).hasSize(MAX_TAGGED_PEOPLE)
        assertThat(vm.state.value.canTagMore).isFalse()
    }

    @Test
    fun `people search is debounced and needs two characters`() = runTest {
        val lookups = FakeLookups(people = listOf(TaggedUser("u-9", "Call UserB", "")))
        val vm = viewModel(lookups = lookups)

        vm.onPeopleQueryChanged("c")
        advanceUntilIdle()
        assertThat(lookups.queries).isEmpty()

        vm.onPeopleQueryChanged("ca")
        vm.onPeopleQueryChanged("cal")
        advanceUntilIdle()

        assertThat(lookups.queries).containsExactly("cal")
        assertThat(vm.state.value.peopleResults.map { it.id }).containsExactly("u-9")
        assertThat(vm.state.value.peopleSearching).isFalse()
    }

    // ── Cover ───────────────────────────────────────────────────────────

    @Test
    fun `the first frame is the default cover and the strip has two dozen thumbnails`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        assertThat(vm.state.value.frames).hasSize(Filmstrip.FRAME_COUNT)
        assertThat(vm.state.value.cover?.index).isEqualTo(0)
        assertThat(vm.state.value.cover?.timeUs).isEqualTo(0L)
        assertThat(vm.state.value.coverSource).isEqualTo(ReelPublishViewModel.CoverSource.Frame)
        assertThat(vm.state.value.durationUs).isEqualTo(TEN_SECONDS_US)
    }

    @Test
    fun `the frame under the handle becomes the cover and is written before hand-off`() = runTest {
        val launcher = FakeLauncher()
        val files = FakeFiles()
        val vm = viewModel(launcher = launcher, files = files)

        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()
        vm.chooseFrame(3)
        advanceUntilIdle()
        vm.onPost()
        advanceUntilIdle()

        assertThat(vm.state.value.cover?.index).isEqualTo(3)
        assertThat(vm.state.value.cover?.timeUs).isEqualTo(vm.state.value.frames[3].timeUs)
        assertThat(vm.state.value.picker).isNull()
        assertThat(files.covers.single()).hasLength(4)
        assertThat(launcher.enqueued.single().coverPath).isNotNull()
    }

    @Test
    fun `scrubbing moves the readout at once and the frame follows the newest instant`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        vm.openCoverPicker()
        vm.onScrub(4_000_000L)
        assertThat(vm.state.value.picker?.timeUs).isEqualTo(4_000_000L)
        assertThat(vm.state.value.picker?.seeking).isTrue()
        vm.onScrub(6_000_000L)
        advanceUntilIdle()

        assertThat(vm.state.value.picker?.timeUs).isEqualTo(6_000_000L)
        assertThat(vm.state.value.picker?.seeking).isFalse()
        assertThat(vm.state.value.picker?.preview).isNotNull()
    }

    @Test
    fun `the handle cannot pass the end of the video`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        vm.openCoverPicker()
        vm.onScrub(99_000_000L)

        assertThat(vm.state.value.picker?.timeUs).isEqualTo(TEN_SECONDS_US - Filmstrip.TAIL_MARGIN_US)
    }

    @Test
    fun `closing the picker keeps the cover that was there`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        vm.openCoverPicker()
        vm.onScrub(5_000_000L)
        advanceUntilIdle()
        vm.closeCoverPicker()

        assertThat(vm.state.value.picker).isNull()
        assertThat(vm.state.value.cover?.timeUs).isEqualTo(0L)
    }

    @Test
    fun `an uploaded image becomes the cover, marked as uploaded`() = runTest {
        val launcher = FakeLauncher()
        val files = FakeFiles()
        val vm = viewModel(launcher = launcher, files = files)
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        vm.onCoverImagePicked("content://image/7")
        advanceUntilIdle()
        vm.onPost()
        advanceUntilIdle()

        assertThat(vm.state.value.coverSource).isEqualTo(ReelPublishViewModel.CoverSource.Upload)
        assertThat(vm.state.value.cover?.index).isEqualTo(CoverFrame.UPLOADED)
        assertThat(files.covers).hasSize(1)
        assertThat(launcher.enqueued.single().coverPath).isNotNull()
    }

    @Test
    fun `a cover that cannot be prepared fails the post and hands nothing over`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher, files = FakeFiles().apply { refuseCover = true })

        vm.pickAndPost("with a cover")
        advanceUntilIdle()

        val phase = vm.state.value.phase
        assertThat(phase).isInstanceOf(ReelPublishViewModel.Phase.Failure::class.java)
        assertThat((phase as ReelPublishViewModel.Phase.Failure).message).contains("cover")
        assertThat(launcher.enqueued).isEmpty()
        assertThat(vm.state.value.canPost).isTrue()
    }

    @Test
    fun `with no extractable frame the post is handed over without a cover`() = runTest {
        val launcher = FakeLauncher()
        val files = FakeFiles()
        val vm = viewModel(
            launcher = launcher,
            files = files,
            frames = frames(withBitmaps = false),
            seeker = seeker(answers = false),
        )

        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(vm.state.value.cover).isNull()
        assertThat(files.covers).isEmpty()
        assertThat(launcher.enqueued.single().coverPath).isNull()
    }

    @Test
    fun `when the exact seek fails the strip's first thumbnail is the cover`() = runTest {
        val vm = viewModel(frames = frames(withBitmaps = true), seeker = seeker(answers = false))

        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        assertThat(vm.state.value.cover?.index).isEqualTo(0)
        assertThat(vm.state.value.cover?.bitmap).isNotNull()
    }

    // ── Guards ──────────────────────────────────────────────────────────

    /** The queue takes several (2026-09-05): a second reel is handed over even while the first uploads. */
    @Test
    fun `a second post while another reel is still posting is queued, not refused`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.pickAndPost("first")
        advanceUntilIdle()
        vm.onVideoPicked("content://video/2")
        vm.onCaptionChanged("second")
        vm.onPost()
        advanceUntilIdle()

        assertThat(launcher.enqueued.map { it.caption }).containsExactly("first", "second").inOrder()
        assertThat(launcher.enqueued.map { it.creationKey }.distinct()).hasSize(2)
        assertThat(vm.state.value.phase).isEqualTo(ReelPublishViewModel.Phase.Enqueued)
    }

    @Test
    fun `post is unavailable until a video is chosen and a caption is not required`() = runTest {
        val vm = viewModel()
        assertThat(vm.state.value.canPost).isFalse()

        vm.onVideoPicked("content://video/1")
        assertThat(vm.state.value.canPost).isTrue()
        assertThat(vm.state.value.caption).isEmpty()
    }

    // ── Kind and the gate (Tube, 2026-09-05) ────────────────────────────

    @Test
    fun `the Video tile opens the form as a long video and every other way in is a reel`() {
        assertThat(viewModel(surface = CreateSurface.Video).state.value.kind).isEqualTo(VideoKind.LONG)
        assertThat(viewModel(surface = CreateSurface.Reel).state.value.kind).isEqualTo(VideoKind.REEL)
        assertThat(ReelPublishViewModel.videoKindForSurface(null)).isEqualTo(VideoKind.REEL)
        assertThat(ReelPublishViewModel.videoKindForSurface("video")).isEqualTo(VideoKind.LONG)
    }

    @Test
    fun `a long video needs a title and carries it and its kind into the record`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher, surface = CreateSurface.Video)

        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()
        assertThat(vm.state.value.canPost).isFalse()
        assertThat(vm.state.value.hasRequiredText).isFalse()

        vm.onTitleChanged("   ")
        assertThat(vm.state.value.canPost).isFalse()
        vm.onTitleChanged("How the feed ranks")
        assertThat(vm.state.value.canPost).isTrue()
        vm.onPost()
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.kind).isEqualTo(VideoKind.LONG)
        assertThat(pending.title).isEqualTo("How the feed ranks")
    }

    @Test
    fun `the title is clamped to a hundred characters`() {
        val vm = viewModel(surface = CreateSurface.Video)
        vm.onTitleChanged("x".repeat(140))
        assertThat(vm.state.value.title).hasLength(ReelPublishViewModel.MAX_TITLE_LENGTH)
    }

    @Test
    fun `a reel over five minutes cannot post until it is switched to a video, keeping the selection`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher, probe = probe(durationMs = 6L * 60L * 1_000L))

        vm.onVideoPicked("content://video/1")
        vm.onCaptionChanged("six minutes")
        advanceUntilIdle()
        vm.chooseFrame(2)
        advanceUntilIdle()

        assertThat(vm.state.value.gate).isEqualTo(VideoGate.TooLongForReel(360_000L))
        assertThat(vm.state.value.canPost).isFalse()
        vm.onPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued).isEmpty()

        vm.switchToLong()
        advanceUntilIdle()
        assertThat(vm.state.value.kind).isEqualTo(VideoKind.LONG)
        assertThat(vm.state.value.gate).isEqualTo(VideoGate.Ok)
        assertThat(vm.state.value.videoUri).isEqualTo("content://video/1")
        assertThat(vm.state.value.cover?.index).isEqualTo(2)
        assertThat(vm.state.value.caption).isEqualTo("six minutes")
        assertThat(vm.state.value.canPost).isFalse() // a video still needs its title

        vm.onTitleChanged("Six minutes")
        vm.onPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued.single().kind).isEqualTo(VideoKind.LONG)
        assertThat(launcher.enqueued.single().caption).isEqualTo("six minutes")
    }

    @Test
    fun `a file over 500 MB is refused for either kind`() = runTest {
        val tooBig = 501L * 1024 * 1024
        val reel = viewModel(probe = probe(sizeBytes = tooBig))
        reel.onVideoPicked("content://video/1")
        advanceUntilIdle()
        assertThat(reel.state.value.gate).isEqualTo(VideoGate.TooLarge(tooBig))
        assertThat(reel.state.value.canPost).isFalse()

        val video = viewModel(probe = probe(sizeBytes = tooBig), surface = CreateSurface.Video)
        video.onVideoPicked("content://video/1")
        video.onTitleChanged("Big")
        advanceUntilIdle()
        assertThat(video.state.value.gate).isEqualTo(VideoGate.TooLarge(tooBig))
        assertThat(video.state.value.canPost).isFalse()
    }

    // ── Channel before video (2026-09-05) ───────────────────────────────

    @Test
    fun `a video asks for the channel once and a reel never does`() = runTest {
        val api = FakeChannelApi(hasChannel = true)
        val video = viewModel(surface = CreateSurface.Video, channels = channels(api))
        advanceUntilIdle()
        assertThat(video.state.value.channel).isInstanceOf(ChannelState.Present::class.java)
        assertThat(api.meCalls).isEqualTo(1)

        val reelApi = FakeChannelApi(hasChannel = true)
        viewModel(surface = CreateSurface.Reel, channels = channels(reelApi))
        advanceUntilIdle()
        assertThat(reelApi.meCalls).isEqualTo(0)
    }

    @Test
    fun `a 404 from the channel endpoint means none, so the sheet comes first`() = runTest {
        val vm = viewModel(surface = CreateSurface.Video, channels = channels(FakeChannelApi(hasChannel = false)))
        advanceUntilIdle()

        assertThat(vm.state.value.channel).isEqualTo(ChannelState.None)
    }

    @Test
    fun `switching a reel to a video asks for the channel`() = runTest {
        val api = FakeChannelApi(hasChannel = true)
        val vm = viewModel(surface = CreateSurface.Reel, channels = channels(api))
        advanceUntilIdle()
        assertThat(api.meCalls).isEqualTo(0)

        vm.switchToLong()
        advanceUntilIdle()

        assertThat(api.meCalls).isEqualTo(1)
        assertThat(vm.state.value.channel).isInstanceOf(ChannelState.Present::class.java)
    }

    private companion object {
        const val TEN_SECONDS_US = 10_000_000L
        const val NOT_FOUND = 404
        const val SETTLE_MILLIS = 100L
    }

    // ── The details step's fields (2026-09-05) ─────────────────────────

    @Test
    fun `typing a space or comma after a tag makes a chip, and the chips go on the record`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onHashtagInputChanged("#longboard ")
        vm.onHashtagInputChanged("sunday, ")
        vm.onHashtagInputChanged("Longboard ") // a duplicate, ignoring case
        vm.onHashtagInputChanged("skate") // left in the field, no separator yet

        assertThat(vm.state.value.hashtags).containsExactly("longboard", "sunday").inOrder()
        assertThat(vm.state.value.hashtagInput).isEqualTo("skate")

        vm.pickAndPost("caption only")
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.caption).isEqualTo("caption only")
        assertThat(pending.hashtags).containsExactly("longboard", "sunday", "skate").inOrder()
    }

    @Test
    fun `hashtag suggestions arrive from two characters and a tapped one becomes a chip`() = runTest {
        val lookups = FakeLookups(hashtags = listOf("longboard", "longride"))
        val vm = viewModel(lookups = lookups)

        vm.onHashtagInputChanged("l")
        advanceUntilIdle()
        assertThat(lookups.hashtagQueries).isEmpty()

        vm.onHashtagInputChanged("#lo")
        advanceUntilIdle()
        assertThat(lookups.hashtagQueries).containsExactly("lo")
        assertThat(vm.state.value.hashtagSuggestions).containsExactly("longboard", "longride").inOrder()

        vm.onHashtagSuggestionPicked("longboard")
        assertThat(vm.state.value.hashtags).containsExactly("longboard")
        assertThat(vm.state.value.hashtagInput).isEmpty()
        assertThat(vm.state.value.hashtagSuggestions).isEmpty()

        vm.removeHashtag("longboard")
        assertThat(vm.state.value.hashtags).isEmpty()
    }

    @Test
    fun `mentioned people go on the record as ids and as usernames`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)

        vm.onTagUser(TaggedUser("u-1", "Maya", "maya"))
        vm.onTagUser(TaggedUser("u-3", "Zed", "")) // no handle: an id only
        vm.pickAndPost()
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(pending.taggedUserIds).containsExactly("u-1", "u-3").inOrder()
        assertThat(pending.mentions).containsExactly("maya")
    }

    @Test
    fun `a schedule is carried as RFC 3339 and cleared back to post now`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)
        val at = java.time.Instant.parse("2026-09-06T13:00:00Z")

        vm.onScheduleChanged(at)
        assertThat(vm.state.value.publishAt).isEqualTo(at)
        vm.pickAndPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued.last().publishAt).isEqualTo("2026-09-06T13:00:00Z")

        vm.onScheduleChanged(null)
        vm.onVideoPicked("content://video/2")
        vm.onPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued.last().publishAt).isNull()
    }

    // ── The studio's hand-off (2026-09-05) ─────────────────────────────

    /** The export lands under the form's key and is uploaded from there: no stash, no copy. */
    @Test
    fun `an exported reel keeps its key and hands the file path over as the video to upload`() = runTest {
        val launcher = FakeLauncher()
        val vm = viewModel(launcher = launcher)
        val target = vm.exportTargetPath()

        vm.onReelExported(target)
        advanceUntilIdle()
        assertThat(vm.state.value.frames).hasSize(Filmstrip.FRAME_COUNT)
        assertThat(vm.state.value.cover).isNotNull()
        vm.onPost()
        advanceUntilIdle()

        val pending = launcher.enqueued.single()
        assertThat(target).endsWith("${pending.creationKey}.video")
        assertThat(pending.videoPath).isEqualTo(target)
        assertThat(pending.videoMimeType).isEqualTo("video/mp4")
        assertThat(pending.videoUri).endsWith(".video")
    }

    @Test
    fun `change video forgets the video and its cover and keeps the fields`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        vm.onCaptionChanged("kept")
        vm.onHashtagInputChanged("kept ")
        advanceUntilIdle()

        vm.clearVideo()

        assertThat(vm.state.value.videoUri).isNull()
        assertThat(vm.state.value.cover).isNull()
        assertThat(vm.state.value.frames).isEmpty()
        assertThat(vm.state.value.canPost).isFalse()
        assertThat(vm.state.value.caption).isEqualTo("kept")
        assertThat(vm.state.value.hashtags).containsExactly("kept")
    }
}
