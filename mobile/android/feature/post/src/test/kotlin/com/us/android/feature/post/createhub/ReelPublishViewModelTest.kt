package com.us.android.feature.post.createhub

import android.graphics.Bitmap
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_IMAGE
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MediaAltTextRequest
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaConfirmRequest
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaInitRequest
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaStatusDto
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.POST_TYPE_VIDEO
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayInputStream

/**
 * The reel form's ONE mapping to bytes, and the cover discipline.
 *
 * The request tests pin the wire names the server agreed (2026-09-04):
 * `no_comments` inverted from "Allow comments", `hide_share`,
 * `allow_download`, `remix_setting` as `allow`/`disallow`, `visibility`,
 * `category`, `cover_media_id`, `tagged_user_ids`, `location_name`, and
 * `title` empty. The cover tests pin that a cover which fails to upload
 * FAILS the post with a retryable message — it never posts without it.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelPublishViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    // ── Fakes ───────────────────────────────────────────────────────────

    /** Records every `init`, answers ready+passed by default, can refuse one file type. */
    private class FakeUploadApi : MediaUploadApi {
        val inits = mutableListOf<MediaInitRequest>()
        var refuseFileType: String? = null
        val statuses = ArrayDeque<MediaStatusDto>()
        private var nextId = 0

        override suspend fun init(body: MediaInitRequest): ApiEnvelope<MediaInitDto> {
            inits += body
            if (body.fileType == refuseFileType) error("init refused for ${body.fileType}")
            val id = "${body.fileType}-${++nextId}"
            return ApiEnvelope(MediaInitDto(mediaId = id, uploadUrl = "https://obj/put/$id"))
        }

        override suspend fun confirm(body: MediaConfirmRequest): ApiEnvelope<MediaAssetDto> =
            ApiEnvelope(MediaAssetDto(id = body.mediaId, processingStatus = "processing"))

        override suspend fun updateAltText(mediaId: String, body: MediaAltTextRequest): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(MediaStatusDto(mediaId = mediaId))

        override suspend fun status(mediaId: String): ApiEnvelope<MediaStatusDto> {
            val next = if (statuses.size > 1) statuses.removeFirst() else statuses.firstOrNull()
            return ApiEnvelope(
                next?.copy(mediaId = mediaId)
                    ?: MediaStatusDto(mediaId = mediaId, processingStatus = "ready", moderationStatus = "passed"),
            )
        }

        override suspend fun delete(mediaId: String): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(MediaStatusDto(mediaId = mediaId))
    }

    private class AlwaysSucceedingPut : PresignedUploader(OkHttpClient()) {
        override suspend fun put(
            url: String,
            mimeType: String,
            sizeBytes: Long,
            source: UploadSource,
            onProgress: (Long, Long) -> Unit,
        ): PresignedPutResult {
            onProgress(sizeBytes, sizeBytes)
            return PresignedPutResult.Success
        }
    }

    private class RecordingRepository(json: Json) :
        ComposerRepository(ThrowingPostApi(), ErrorMapper(json)) {
        val requests = mutableListOf<CreatePostRequest>()
        val keys = mutableListOf<String>()
        var result: AppResult<String> = AppResult.Success("post-1")

        override suspend fun createPost(creationKey: String, request: CreatePostRequest): AppResult<String> {
            keys += creationKey
            requests += request
            return result
        }
    }

    private class ThrowingPostApi : PostApi {
        override suspend fun getPost(postId: String): Nothing = error("not used")
        override suspend fun createPost(idempotencyKey: String, body: CreatePostRequest): Nothing = error("not used")
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

    private val encoder = ReelCoverEncoder { frame -> frame.bitmap?.let { ByteArray(64) } }

    private val videos = MediaSourceResolver { uri ->
        PickedMedia(uri, "video/mp4", 1_000, UploadSource { ByteArrayInputStream(ByteArray(8)) })
    }

    private fun viewModel(
        api: FakeUploadApi = FakeUploadApi(),
        repository: RecordingRepository = RecordingRepository(json),
        lookups: FakeLookups = FakeLookups(),
        frames: ReelFrameExtractor = frames(withBitmaps = true),
    ) = ReelPublishViewModel(
        repository = repository,
        uploads = ReelMediaUploads(
            uploader = MediaUploader(api = api, presigned = AlwaysSucceedingPut(), errorMapper = ErrorMapper(json)),
            encoder = encoder,
            io = Dispatchers.Unconfined,
        ),
        videoSources = videos,
        frames = frames,
        lookups = lookups,
    )

    private fun ReelPublishViewModel.pickAndPost(caption: String = "") {
        onVideoPicked("content://video/1")
        onCaptionChanged(caption)
        onPost()
    }

    // ── Request mapping ─────────────────────────────────────────────────

    @Test
    fun `the defaults map to the agreed wire values`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.pickAndPost("Sunday skate #longboard with @maya")
        advanceUntilIdle()

        val request = repository.requests.single()
        assertThat(request.text).isEqualTo("Sunday skate #longboard with @maya")
        assertThat(request.title).isEmpty()
        assertThat(request.contentType).isEqualTo(CONTENT_TYPE_FLICK)
        assertThat(request.postType).isEqualTo(POST_TYPE_VIDEO)
        assertThat(request.mediaIds).containsExactly("video-1")
        assertThat(request.visibility).isEqualTo(VISIBILITY_PUBLIC)
        assertThat(request.noComments).isFalse()
        assertThat(request.hideShare).isFalse()
        assertThat(request.allowDownload).isTrue()
        assertThat(request.remixSetting).isEqualTo(REMIX_ALLOW)
        assertThat(request.category).isNull()
        assertThat(request.taggedUserIds).isNull()
        assertThat(request.locationName).isNull()
        assertThat(vm.state.value.phase).isEqualTo(ReelPublishViewModel.Phase.Published("post-1"))
    }

    @Test
    fun `flipping every switch flips every wire field`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.onAllowCommentsChanged(false)
        vm.onHideShareChanged(true)
        vm.onAllowDownloadChanged(false)
        vm.onAllowRemixChanged(false)
        vm.pickAndPost()
        advanceUntilIdle()

        val request = repository.requests.single()
        assertThat(request.noComments).isTrue()
        assertThat(request.hideShare).isTrue()
        assertThat(request.allowDownload).isFalse()
        assertThat(request.remixSetting).isEqualTo(REMIX_DISALLOW)
    }

    @Test
    fun `audience maps to visibility and unknown values are refused`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.onVisibilityChanged(VISIBILITY_FOLLOWERS)
        assertThat(vm.state.value.visibility).isEqualTo(VISIBILITY_FOLLOWERS)
        vm.onVisibilityChanged("unlisted")
        assertThat(vm.state.value.visibility).isEqualTo(VISIBILITY_FOLLOWERS)
        vm.onVisibilityChanged(VISIBILITY_PRIVATE)
        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(repository.requests.single().visibility).isEqualTo(VISIBILITY_PRIVATE)
    }

    @Test
    fun `a chosen category is sent and None is omitted`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.onCategoryChanged("comedy")
        vm.pickAndPost()
        advanceUntilIdle()
        assertThat(repository.requests.last().category).isEqualTo("comedy")

        // A published reel is done; a new video starts a new post with the same form.
        vm.onVideoPicked("content://video/2")
        vm.onCategoryChanged("")
        vm.onPost()
        advanceUntilIdle()
        assertThat(repository.requests).hasSize(2)
        assertThat(repository.requests.last().category).isNull()
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
    fun `tagged people and a location go on the wire`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.onTagUser(TaggedUser("u-1", "Maya", "maya"))
        vm.onTagUser(TaggedUser("u-2", "Ravi", "ravi"))
        vm.onTagUser(TaggedUser("u-1", "Maya again", "maya")) // a duplicate is ignored
        vm.onUntagUser("u-2")
        vm.onTagUser(TaggedUser("u-3", "Zed", ""))
        vm.onLocationChanged("  Marina Beach  ")
        vm.pickAndPost()
        advanceUntilIdle()

        val request = repository.requests.single()
        assertThat(request.taggedUserIds).containsExactly("u-1", "u-3").inOrder()
        assertThat(request.locationName).isEqualTo("Marina Beach")
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
    fun `the chosen frame is uploaded as an image and sent as the cover`() = runTest {
        val api = FakeUploadApi()
        val repository = RecordingRepository(json)
        val vm = viewModel(api = api, repository = repository)

        vm.onVideoPicked("content://video/1")
        advanceUntilIdle()
        vm.onCoverSelected(3)
        vm.onPost()
        advanceUntilIdle()

        assertThat(api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO, FILE_TYPE_IMAGE).inOrder()
        assertThat(api.inits.last().mimeType).isEqualTo("image/jpeg")
        val request = repository.requests.single()
        assertThat(request.mediaIds).containsExactly("video-1")
        assertThat(request.coverMediaId).isEqualTo("image-2")
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
    fun `a failed cover upload fails the post with a retryable message and never posts without it`() = runTest {
        val api = FakeUploadApi().apply { refuseFileType = FILE_TYPE_IMAGE }
        val repository = RecordingRepository(json)
        val vm = viewModel(api = api, repository = repository)

        vm.pickAndPost("with a cover")
        advanceUntilIdle()

        val phase = vm.state.value.phase
        assertThat(phase).isInstanceOf(ReelPublishViewModel.Phase.Failure::class.java)
        assertThat((phase as ReelPublishViewModel.Phase.Failure).retryable).isTrue()
        assertThat(phase.message).contains("cover")
        assertThat(repository.requests).isEmpty()
        assertThat(vm.state.value.canPost).isTrue()
    }

    @Test
    fun `a retry after a cover failure reuses the ready video and does not re-upload it`() = runTest {
        val api = FakeUploadApi().apply { refuseFileType = FILE_TYPE_IMAGE }
        val repository = RecordingRepository(json)
        val vm = viewModel(api = api, repository = repository)

        vm.pickAndPost()
        advanceUntilIdle()
        api.refuseFileType = null
        vm.onPost()
        advanceUntilIdle()

        assertThat(api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO, FILE_TYPE_IMAGE, FILE_TYPE_IMAGE)
        assertThat(repository.requests.single().coverMediaId).isEqualTo("image-2")
        assertThat(repository.requests.single().mediaIds).containsExactly("video-1")
    }

    @Test
    fun `a cover that is not ready and passed is never attached`() = runTest {
        val api = FakeUploadApi().apply {
            // Video: ready+passed. Then the cover sits at ready/pending for the whole window.
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
            repeat(40) { statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "pending") }
        }
        val repository = RecordingRepository(json)
        val vm = viewModel(api = api, repository = repository)

        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(repository.requests).isEmpty()
        assertThat(vm.state.value.phase).isInstanceOf(ReelPublishViewModel.Phase.Failure::class.java)
    }

    @Test
    fun `with no extractable frame the post goes out without a cover`() = runTest {
        val api = FakeUploadApi()
        val repository = RecordingRepository(json)
        val vm = viewModel(api = api, repository = repository, frames = frames(withBitmaps = false))

        vm.pickAndPost()
        advanceUntilIdle()

        assertThat(api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO)
        assertThat(repository.requests.single().coverMediaId).isNull()
    }

    // ── Create failures ─────────────────────────────────────────────────

    @Test
    fun `a failed create keeps both ready ids and the creation key for the retry`() = runTest {
        val api = FakeUploadApi()
        val repository = RecordingRepository(json).apply { result = AppResult.Failure(AppError.NoNetwork()) }
        val vm = viewModel(api = api, repository = repository)

        vm.pickAndPost()
        advanceUntilIdle()
        repository.result = AppResult.Success("post-2")
        vm.onPost()
        advanceUntilIdle()

        assertThat(api.inits).hasSize(2)
        assertThat(repository.keys.distinct()).hasSize(1)
        assertThat(repository.requests).hasSize(2)
        assertThat(vm.state.value.phase).isEqualTo(ReelPublishViewModel.Phase.Published("post-2"))
    }

    @Test
    fun `post is unavailable until a video is chosen and a caption is not required`() = runTest {
        val vm = viewModel()
        assertThat(vm.state.value.canPost).isFalse()

        vm.onVideoPicked("content://video/1")
        assertThat(vm.state.value.canPost).isTrue()
        assertThat(vm.state.value.caption).isEmpty()
    }
}
