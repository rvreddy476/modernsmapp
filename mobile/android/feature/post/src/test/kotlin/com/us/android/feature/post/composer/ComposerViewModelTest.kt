package com.us.android.feature.post.composer

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.database.ComposerDraftDao
import com.us.android.core.database.ComposerDraftEntity
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaStatusDto
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Rule
import org.junit.Test
import java.io.ByteArrayInputStream

/**
 * The composer's EFFECTS — C-P0-2, C-P0-3, C-LB-7.2.
 *
 * The reducer tests cover meaning; nothing covered orchestration, and that is
 * where the two launch blockers lived:
 *
 *  - every non-rejected confirmation was treated as ready, so `pending`,
 *    `processing`, `failed` and any unknown status became attachable client
 *    state. The reducer tests could not catch it because they called
 *    `onMediaReady` directly;
 *  - the draft lived in `SavedStateHandle`, so a navigation pop destroyed it —
 *    including the creation key that stops a retry publishing twice — and a
 *    restore with an unfinished upload was permanently stuck.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ComposerViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    // ── Fakes ───────────────────────────────────────────────────────────

    /** Records every write so persistence can be asserted, not assumed. */
    private class FakeDraftDao : ComposerDraftDao {
        var stored: ComposerDraftEntity? = null
        var clears = 0

        /**
         * When set, `clear` suspends on it — Slice C, C-CLB-2.
         *
         * Lets a test hold the delete open and observe what the composer does
         * WHILE it is still in flight. That window is the whole defect: the
         * screen used to navigate inside it, cancelling the delete.
         */
        var clearGate: CompletableDeferred<Unit>? = null

        override fun observe(id: String): Flow<ComposerDraftEntity?> = flowOf(stored)
        override suspend fun load(id: String): ComposerDraftEntity? = stored
        override suspend fun save(draft: ComposerDraftEntity) {
            stored = draft
        }

        override suspend fun clear(id: String) {
            clearGate?.await()
            stored = null
            clears++
        }
    }

    private class FakeUploadApi : MediaUploadApi {
        val statuses = ArrayDeque<MediaStatusDto>()
        var altUpdates = mutableListOf<Pair<String, Boolean>>()
        var altUpdateFails = false
        var initCalls = 0
        var lastInitPurpose: String? = null

        override suspend fun init(
            body: com.us.android.core.media.upload.MediaInitRequest,
        ): ApiEnvelope<MediaInitDto> {
            initCalls++
            lastInitPurpose = body.uploadPurpose
            return ApiEnvelope(MediaInitDto(mediaId = "media-1", uploadUrl = "https://obj/put"))
        }

        override suspend fun confirm(
            body: com.us.android.core.media.upload.MediaConfirmRequest,
        ): ApiEnvelope<MediaAssetDto> =
            ApiEnvelope(MediaAssetDto(id = "media-1", processingStatus = "processing"))

        override suspend fun updateAltText(
            mediaId: String,
            body: com.us.android.core.media.upload.MediaAltTextRequest,
        ): ApiEnvelope<MediaStatusDto> {
            if (altUpdateFails) error("alt update failed")
            altUpdates += body.altText to body.decorative
            return ApiEnvelope(MediaStatusDto(mediaId = mediaId))
        }

        override suspend fun status(mediaId: String): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(if (statuses.size > 1) statuses.removeFirst() else statuses.first())

        override suspend fun delete(mediaId: String): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(MediaStatusDto(mediaId = mediaId))
    }

    private class RecordingRepository(private val json: Json) :
        ComposerRepository(ThrowingPostApi(), ErrorMapper(Json { ignoreUnknownKeys = true })) {
        val keys = mutableListOf<String>()
        var result: AppResult<String> = AppResult.Success("post-1")

        override suspend fun createPost(
            creationKey: String,
            request: CreatePostRequest,
        ): AppResult<String> {
            keys += creationKey
            return result
        }
    }

    private class ThrowingPostApi : com.us.android.feature.post.data.PostApi {
        override suspend fun getPost(postId: String): Nothing = error("not used")
        override suspend fun createPost(
            idempotencyKey: String,
            body: CreatePostRequest,
        ): Nothing = error("not used")
    }

    /**
     * A PUT that always succeeds without touching the network.
     *
     * The real transport is covered by `MediaUploadWireTest`, which drives it
     * against MockWebServer and asserts header isolation. These tests are about
     * ORCHESTRATION, so a real socket here would only make them slow and flaky.
     */
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

    private fun uploader(api: FakeUploadApi) = MediaUploader(
        api = api,
        presigned = AlwaysSucceedingPut(),
        errorMapper = ErrorMapper(json),
    )

    private fun viewModel(
        api: FakeUploadApi = FakeUploadApi(),
        dao: FakeDraftDao = FakeDraftDao(),
        repository: RecordingRepository = RecordingRepository(json),
        resolver: ImageSourceResolver = ImageSourceResolver { uri: String ->
            PickedImage(uri, "image/jpeg", 10, UploadSource { ByteArrayInputStream(ByteArray(10)) })
        },
        keyFactory: CreationKeyFactory = object : CreationKeyFactory {
            var n = 0
            override fun newKey(): String = "key-${++n}"
        },
    ) = ComposerViewModel(
        repository = repository,
        uploader = uploader(api),
        imageSources = resolver,
        drafts = ComposerDraftStore(dao, json),
        keys = keyFactory,
    )

    /** A repository whose publish never succeeds, so the draft survives. */
    private fun failingRepository() = RecordingRepository(json).apply {
        result = AppResult.Failure(AppError.NoNetwork())
    }

    // ── C-P0-2: readiness ───────────────────────────────────────────────

    /**
     * `processing` must NOT become attachable.
     *
     * The old final `else` called `onMediaReady` for every non-rejected status,
     * so the composer enabled Post for an asset the server would then refuse.
     */
    @Test
    fun `a still-processing asset never becomes attachable`() = runTest {
        val api = FakeUploadApi().apply {
            repeat(30) { statuses += MediaStatusDto(processingStatus = "processing") }
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(vm.state.value.mediaId).isNull()
        assertThat(vm.state.value.blockedReason).isNotNull()
    }

    /** `ready` alone is not enough: moderation must have PASSED. */
    @Test
    fun `ready but unmoderated media never becomes attachable`() = runTest {
        val api = FakeUploadApi().apply {
            repeat(30) {
                statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "pending")
            }
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(vm.state.value.mediaId).isNull()
    }

    @Test
    fun `exactly ready and passed makes the asset attachable`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(vm.state.value.mediaId).isEqualTo("media-1")
    }

    /** A rejected verdict is terminal, with no Retry offered. */
    @Test
    fun `a rejected verdict drops the image terminally`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "rejected")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(vm.state.value.imageUri).isNull()
        assertThat(vm.state.value.phase).isInstanceOf(ComposerPhase.TerminalFailure::class.java)
    }

    /** `failed` processing is terminal too — it can never become ready. */
    @Test
    fun `failed processing is terminal`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "failed")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(vm.state.value.phase).isInstanceOf(ComposerPhase.TerminalFailure::class.java)
    }

    /** The lease must be on every composer init, or the asset leaks forever. */
    @Test
    fun `init carries the composer lease`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()

        assertThat(api.lastInitPurpose).isEqualTo("composer")
    }

    // ── C-P0-2: the final accessibility decision ────────────────────────

    /**
     * The decision the user made is what the server stores.
     *
     * `init` runs before the user has typed anything, so whatever it carried is
     * a placeholder. Without this PATCH the composer would require and display a
     * description while the server permanently kept the empty value.
     */
    @Test
    fun `the final description is written before the post is created`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()
        vm.onAltTextChanged("a cat asleep on a keyboard")
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(api.altUpdates).containsExactly("a cat asleep on a keyboard" to false)
    }

    @Test
    fun `a decorative mark is written as an empty description`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }
        val vm = viewModel(api)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()
        vm.onDecorativeChanged(true)
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(api.altUpdates).containsExactly("" to true)
    }

    /**
     * A failed description write BLOCKS the post.
     *
     * Publishing anyway would silently discard a decision the product required
     * and the user made — and the post would be public with the wrong alt text
     * for everyone who reads it.
     */
    @Test
    fun `a failed description write stops the publish`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
            altUpdateFails = true
        }
        val repository = RecordingRepository(json)
        val vm = viewModel(api, repository = repository)

        vm.onImagePicked("content://pick/1")
        advanceUntilIdle()
        vm.onAltTextChanged("a description")
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(repository.keys).isEmpty()
        assertThat(vm.state.value.phase).isNotInstanceOf(ComposerPhase.Published::class.java)
    }

    /** A text-only post makes no accessibility call at all. */
    @Test
    fun `a text post writes no description`() = runTest {
        val api = FakeUploadApi()
        val vm = viewModel(api)

        vm.onTextChanged("just words")
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(api.altUpdates).isEmpty()
    }

    // ── C-P0-3: durable recovery ────────────────────────────────────────

    /** The draft is written to Room, not only held in memory. */
    @Test
    fun `edits are persisted durably`() = runTest {
        val dao = FakeDraftDao()
        val vm = viewModel(dao = dao)

        vm.onTextChanged("half a thought")
        advanceUntilIdle()

        assertThat(dao.stored?.text).isEqualTo("half a thought")
    }

    /**
     * The frozen operation survives, key and bytes together.
     *
     * This is what stops a post-death retry publishing a second copy of a post
     * the server already committed.
     */
    @Test
    fun `the frozen creation key and request are persisted`() = runTest {
        val dao = FakeDraftDao()
        // The publish FAILS, which is the case recovery exists for: the frozen
        // operation must be on disk so the retry can reuse it. A successful
        // publish deletes the draft, and that is asserted separately.
        val vm = viewModel(dao = dao, repository = failingRepository())

        vm.onTextChanged("hello")
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(dao.stored?.creationKey).isEqualTo("key-1")
        assertThat(dao.stored?.frozenRequestJson).contains("hello")
    }

    /** A restored composer comes back with the text and the same key. */
    @Test
    fun `a stored draft is restored with its frozen operation`() = runTest {
        val dao = FakeDraftDao()
        val first = viewModel(dao = dao, repository = failingRepository())
        first.onTextChanged("recovered text")
        first.onPostPressed()
        advanceUntilIdle()

        val second = viewModel(dao = dao, repository = RecordingRepository(json))
        advanceUntilIdle()

        assertThat(second.state.value.text).isEqualTo("recovered text")
        assertThat(second.state.value.frozen?.creationKey).isEqualTo("key-1")
        assertThat(second.state.value.restoredFromDraft).isTrue()
    }

    /**
     * A retry after recovery reuses the ORIGINAL key.
     *
     * Losing it would mean the server sees a new intent and creates a second
     * post — the duplicate this whole mechanism exists to prevent.
     */
    @Test
    fun `a retry after process death sends the original creation key`() = runTest {
        val dao = FakeDraftDao()
        val first = viewModel(dao = dao, repository = failingRepository())
        first.onTextChanged("hello")
        first.onPostPressed()
        advanceUntilIdle()

        val repository = RecordingRepository(json)
        val second = viewModel(dao = dao, repository = repository)
        advanceUntilIdle()
        second.onPostPressed()
        advanceUntilIdle()

        assertThat(repository.keys).containsExactly("key-1")
    }

    /**
     * A restored, unfinished upload RESUMES.
     *
     * Previously restore always returned `Editing` and never restarted the
     * upload, so the user came back to an image that could never become
     * attachable — Post disabled as `MediaNotReady`, and Retry rendered only for
     * `RetryableFailure`. Removing and reselecting the photo was the only way out.
     */
    @Test
    fun `a restored incomplete upload resumes instead of getting stuck`() = runTest {
        val dao = FakeDraftDao().apply {
            stored = ComposerDraftEntity(
                text = "with a photo",
                imageUri = "content://pick/1",
                altText = "a description",
                decorative = false,
                language = "en",
                mediaId = null,
                creationKey = null,
                frozenRequestJson = null,
                updatedAtMillis = 0,
            )
        }
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }

        val vm = viewModel(api, dao)
        advanceUntilIdle()

        assertThat(api.initCalls).isEqualTo(1)
        assertThat(vm.state.value.mediaId).isEqualTo("media-1")
        assertThat(vm.state.value.canPost).isTrue()
    }

    /** A CONFIRMED asset is reused, never re-uploaded. */
    @Test
    fun `a restored confirmed asset is not uploaded again`() = runTest {
        val dao = FakeDraftDao().apply {
            stored = ComposerDraftEntity(
                text = "with a photo",
                imageUri = "content://pick/1",
                altText = "a description",
                decorative = false,
                language = "en",
                mediaId = "media-1",
                creationKey = null,
                frozenRequestJson = null,
                updatedAtMillis = 0,
            )
        }
        val api = FakeUploadApi()

        viewModel(api, dao)
        advanceUntilIdle()

        assertThat(api.initCalls).isEqualTo(0)
    }

    /** Publishing clears the draft; a failure never does. */
    @Test
    fun `a successful publish clears the draft and a failure keeps it`() = runTest {
        val dao = FakeDraftDao()
        val repository = RecordingRepository(json).apply {
            result = AppResult.Failure(AppError.NoNetwork())
        }
        val vm = viewModel(dao = dao, repository = repository)

        vm.onTextChanged("hello")
        vm.onPostPressed()
        advanceUntilIdle()
        assertThat(dao.stored).isNotNull()

        repository.result = AppResult.Success("post-1")
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(dao.stored).isNull()
        assertThat(vm.state.value.phase).isEqualTo(ComposerPhase.Published("post-1"))
    }

    /** Double-tap must produce exactly one create call. */
    @Test
    fun `pressing post twice creates one post`() = runTest {
        val repository = RecordingRepository(json)
        val vm = viewModel(repository = repository)

        vm.onTextChanged("hello")
        vm.onPostPressed()
        vm.onPostPressed()
        advanceUntilIdle()

        assertThat(repository.keys).hasSize(1)
    }

    // ── C-CLB-2: discard is durable BEFORE the screen may leave ─────────

    /**
     * The screen may not leave while the delete is still in flight.
     *
     * This is the ordering contract stated as a testable fact. `discarded` is
     * what the screen navigates on, so it must stay false for exactly as long
     * as the durable delete is unfinished.
     *
     * The defect it closes: the confirm button called `onDiscardConfirmed()`
     * and `onClose()` on the same tap. Popping the destination clears the
     * navigation-owned ViewModel and cancels `viewModelScope`, so the Room
     * delete raced the pop. When it lost, content the user explicitly
     * discarded came back the next time they opened the composer.
     *
     * Note this cannot be caught by watching a fast delete finish — under a
     * test database it almost always wins. What is asserted instead is that
     * the SIGNAL is downstream of the delete, which is true regardless of
     * timing. NC-C2B mutates exactly that and this is what fails.
     */
    @Test
    fun `the discard signal is withheld until the durable delete returns`() = runTest {
        val dao = FakeDraftDao()
        val gate = CompletableDeferred<Unit>()
        val vm = viewModel(dao = dao)

        vm.onTextChanged("about to be thrown away")
        advanceUntilIdle()
        assertThat(dao.stored).isNotNull()

        // Hold the delete open.
        dao.clearGate = gate
        vm.onDiscardConfirmed()
        advanceUntilIdle()

        assertThat(dao.clears).isEqualTo(0)
        assertThat(vm.state.value.discarded).isFalse()

        // Let it finish.
        gate.complete(Unit)
        advanceUntilIdle()

        assertThat(dao.clears).isEqualTo(1)
        assertThat(dao.stored).isNull()
        assertThat(vm.state.value.discarded).isTrue()
    }

    /**
     * A discard that completes leaves nothing to restore.
     *
     * The end state, asserted separately from the ordering so a regression in
     * either one is legible on its own.
     */
    @Test
    fun `a completed discard clears the draft and releases the screen`() = runTest {
        val dao = FakeDraftDao()
        val vm = viewModel(dao = dao)

        vm.onTextChanged("goodbye")
        advanceUntilIdle()

        vm.onDiscardConfirmed()
        advanceUntilIdle()

        assertThat(dao.stored).isNull()
        assertThat(vm.state.value.discarded).isTrue()
        assertThat(vm.state.value.text).isEmpty()
    }

    /**
     * Cancelling the discard releases nothing.
     *
     * The mirror image: without this, "always signal discarded" would pass the
     * test above while throwing away drafts the user chose to keep.
     */
    @Test
    fun `keeping the draft does not signal a discard`() = runTest {
        val dao = FakeDraftDao()
        val vm = viewModel(dao = dao)

        vm.onTextChanged("still writing")
        vm.onDiscardRequested()
        vm.onDiscardCancelled()
        advanceUntilIdle()

        assertThat(vm.state.value.discarded).isFalse()
        assertThat(dao.clears).isEqualTo(0)
        assertThat(dao.stored?.text).isEqualTo("still writing")
    }
}
