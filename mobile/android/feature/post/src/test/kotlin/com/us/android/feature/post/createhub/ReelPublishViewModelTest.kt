package com.us.android.feature.post.createhub

import android.graphics.Bitmap
import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayInputStream

/**
 * The reel form: what it carries into the pending publish it hands over,
 * and the cover it writes first. The wire mapping of that record is
 * [ReelPublishPipelineTest]'s.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelPublishViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    // ── Fakes ───────────────────────────────────────────────────────────

    private class FakeLauncher : ReelPublishLauncher {
        override var isBusy: Boolean = false
        val enqueued = mutableListOf<PendingReelPublish>()
        override suspend fun enqueue(pending: PendingReelPublish) {
            enqueued += pending
        }
    }

    private class FakeFiles : ReelPublishFiles {
        val covers = mutableListOf<ByteArray>()
        var refuseCover = false
        override suspend fun stashVideo(uri: String, creationKey: String): StashedVideo? = null
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
    ) : ReelLookups {
        val queries = mutableListOf<String>()
        override suspend fun categories(): List<ReelCategory>? = categories
        override suspend fun searchPeople(query: String): List<TaggedUser> {
            queries += query
            return people
        }
    }

    /** Six frames; whether they carry a bitmap decides whether a cover exists. */
    private fun frames(withBitmaps: Boolean) = ReelFrameExtractor { _, count ->
        List(count) {
            CoverFrame(index = it, timeUs = it * 1_000L, bitmap = if (withBitmaps) mockk<Bitmap>() else null)
        }
    }

    /** The encoded bytes carry the frame index in their length, so the test can tell frames apart. */
    private val encoder = ReelCoverEncoder { frame -> frame.bitmap?.let { ByteArray(frame.index + 1) } }

    /** What the probe says about every picked video; null values are "could not tell". */
    private fun probe(durationMs: Long? = 30_000L, sizeBytes: Long? = 20L * 1024 * 1024) =
        ReelVideoProbe { VideoProbe(durationMs = durationMs, sizeBytes = sizeBytes) }

    private fun viewModel(
        launcher: FakeLauncher = FakeLauncher(),
        files: FakeFiles = FakeFiles(),
        lookups: FakeLookups = FakeLookups(),
        frames: ReelFrameExtractor = frames(withBitmaps = true),
        probe: ReelVideoProbe = probe(),
        surface: CreateSurface = CreateSurface.Reel,
    ) = ReelPublishViewModel(
        launcher = launcher,
        files = files,
        encoder = encoder,
        frames = frames,
        lookups = lookups,
        probe = probe,
        io = Dispatchers.Unconfined,
        savedStateHandle = SavedStateHandle(mapOf(ReelPublishViewModel.SURFACE_ARG to surface.routeKey)),
    )

    private fun ReelPublishViewModel.pickAndPost(caption: String = "") {
        onVideoPicked("content://video/1")
        onCaptionChanged(caption)
        onPost()
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
    fun `the chosen frame is written as the cover before hand-off`() = runTest {
        val launcher = FakeLauncher()
        val files = FakeFiles()
        val vm = viewModel(launcher = launcher, files = files)

        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()
        vm.onCoverSelected(3)
        vm.onPost()
        advanceUntilIdle()

        assertThat(files.covers.single()).hasLength(4)
        assertThat(launcher.enqueued.single().coverPath).isNotNull()
        assertThat(vm.state.value.coverIndex).isEqualTo(3)
    }

    @Test
    fun `the first frame is the default cover`() = runTest {
        val vm = viewModel()
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        assertThat(vm.state.value.frames).hasSize(6)
        assertThat(vm.state.value.coverIndex).isEqualTo(0)
        assertThat(vm.state.value.cover?.index).isEqualTo(0)
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
        val vm = viewModel(launcher = launcher, files = files, frames = frames(withBitmaps = false))

        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(files.covers).isEmpty()
        assertThat(launcher.enqueued.single().coverPath).isNull()
    }

    // ── Guards ──────────────────────────────────────────────────────────

    @Test
    fun `a post while another reel is still posting is refused`() = runTest {
        val launcher = FakeLauncher().apply { isBusy = true }
        val vm = viewModel(launcher = launcher)

        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(launcher.enqueued).isEmpty()
        val phase = vm.state.value.phase
        assertThat(phase).isInstanceOf(ReelPublishViewModel.Phase.Failure::class.java)
        assertThat((phase as ReelPublishViewModel.Phase.Failure).message).contains("still posting")
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
        vm.onCoverSelected(2)
        advanceUntilIdle()

        assertThat(vm.state.value.gate).isEqualTo(VideoGate.TooLongForReel(360_000L))
        assertThat(vm.state.value.canPost).isFalse()
        vm.onPost()
        advanceUntilIdle()
        assertThat(launcher.enqueued).isEmpty()

        vm.switchToLong()
        assertThat(vm.state.value.kind).isEqualTo(VideoKind.LONG)
        assertThat(vm.state.value.gate).isEqualTo(VideoGate.Ok)
        assertThat(vm.state.value.videoUri).isEqualTo("content://video/1")
        assertThat(vm.state.value.coverIndex).isEqualTo(2)
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

        reel.switchToLong()
        reel.onTitleChanged("Big")
        assertThat(reel.state.value.canPost).isFalse()
    }

    @Test
    fun `an unreadable probe never blocks a post`() = runTest {
        val vm = viewModel(probe = probe(durationMs = null, sizeBytes = null))
        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()

        assertThat(vm.state.value.gate).isEqualTo(VideoGate.Ok)
        assertThat(vm.state.value.canPost).isTrue()
    }
}
