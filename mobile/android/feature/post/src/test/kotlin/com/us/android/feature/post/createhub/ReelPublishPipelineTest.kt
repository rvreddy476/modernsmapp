package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.media.upload.FILE_TYPE_IMAGE
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MediaAltTextRequest
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaConfirmRequest
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaInitRequest
import com.us.android.core.media.upload.MediaStatusDto
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
import com.us.android.feature.post.data.dto.CONTENT_TYPE_LONG_VIDEO
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.POST_TYPE_VIDEO
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Test
import java.io.ByteArrayInputStream

/**
 * The reel's ONE mapping to bytes, the instant create, the cover
 * discipline, the fallback, and resumption.
 *
 * The request tests pin the wire names the server agreed (2026-09-04):
 * `no_comments` inverted from "Allow comments", `hide_share`,
 * `allow_download`, `remix_setting` as `allow`/`disallow`, `visibility`,
 * `category`, `cover_media_id`, `tagged_user_ids`, `location_name`, and
 * `title` empty. The instant tests pin that the post is created from the
 * CONFIRMED video with no readiness poll at all. The cover tests pin that a
 * cover which fails to upload FAILS the post with a retryable message — it
 * never posts without it — and that the cover's short readiness wait stays.
 * The fallback tests pin that `MEDIA_NOT_READY` alone sends the pipeline
 * polling, and that a continuation and a restart never re-upload a video
 * the server already has.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelPublishPipelineTest {

    private val json = Json { ignoreUnknownKeys = true }

    // ── Fakes ───────────────────────────────────────────────────────────

    /** Records every `init`, answers ready+passed by default, can refuse one file type. */
    private class FakeUploadApi : MediaUploadApi {
        val inits = mutableListOf<MediaInitRequest>()
        var refuseFileType: String? = null
        val statuses = ArrayDeque<MediaStatusDto>()
        var statusCalls = 0
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
            statusCalls++
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
            onProgress(sizeBytes / 2, sizeBytes)
            onProgress(sizeBytes, sizeBytes)
            return PresignedPutResult.Success
        }
    }

    private class RecordingRepository(json: Json) :
        ComposerRepository(ThrowingPostApi(), ErrorMapper(json)) {
        val requests = mutableListOf<CreatePostRequest>()
        val keys = mutableListOf<String>()
        var result: AppResult<String> = AppResult.Success("post-1")

        /** Answers for the next creates, in order, before [result] takes over. */
        val queued = ArrayDeque<AppResult<String>>()

        override suspend fun createPost(creationKey: String, request: CreatePostRequest): AppResult<String> {
            keys += creationKey
            requests += request
            return queued.removeFirstOrNull() ?: result
        }
    }

    private val notReady: AppResult<String> =
        AppResult.Failure(AppError.Server(statusCode = 409, code = ReelPublishPipeline.MEDIA_NOT_READY))

    private class ThrowingPostApi : PostApi {
        override suspend fun getPost(postId: String): Nothing = error("not used")
        override suspend fun createPost(idempotencyKey: String, body: CreatePostRequest): Nothing = error("not used")
    }

    /** The queue in memory: records in save order, a known key updated in place. */
    private class InMemoryStore : ReelPublishStore {
        val records = mutableListOf<PendingReelPublish>()

        /** The one record most of these tests save under "key-1". */
        val record: PendingReelPublish? get() = records.firstOrNull()

        override suspend fun loadAll(): List<PendingReelPublish> = records.toList()
        override suspend fun save(pending: PendingReelPublish) {
            val index = records.indexOfFirst { it.creationKey == pending.creationKey }
            if (index < 0) records += pending else records[index] = pending
        }
        override suspend fun remove(creationKey: String) {
            records.removeAll { it.creationKey == creationKey }
        }
    }

    private class FakeFiles : ReelPublishFiles {
        var unreadable = false
        val stashed = mutableListOf<String>()
        override suspend fun stashVideo(uri: String, creationKey: String): StashedVideo? {
            if (unreadable) return null
            stashed += uri
            return StashedVideo(path = "/cache/$creationKey.video", mimeType = "video/mp4")
        }

        override fun exportTarget(creationKey: String): String = "/cache/$creationKey.video"

        override suspend fun writeCover(bytes: ByteArray, creationKey: String): String = "/cache/$creationKey.jpg"

        override fun openVideo(path: String, mimeType: String): PickedMedia =
            PickedMedia(path, mimeType, 1_000, UploadSource { ByteArrayInputStream(ByteArray(8)) })

        override suspend fun readBytes(path: String): ByteArray = ByteArray(64)

        override suspend fun delete(paths: List<String?>) = Unit
    }

    private class Harness(
        val api: FakeUploadApi = FakeUploadApi(),
        val repository: RecordingRepository,
        val store: InMemoryStore = InMemoryStore(),
        val files: FakeFiles = FakeFiles(),
        val tracker: ReelPublishTracker = ReelPublishTracker(),
        json: Json,
    ) {
        val pipeline = ReelPublishPipeline(
            repository = repository,
            uploads = ReelMediaUploads(
                uploader = MediaUploader(api = api, presigned = AlwaysSucceedingPut(), errorMapper = ErrorMapper(json)),
                io = Dispatchers.Unconfined,
            ),
            files = files,
            store = store,
            tracker = tracker,
        )
    }

    private fun harness(
        api: FakeUploadApi = FakeUploadApi(),
        repository: RecordingRepository = RecordingRepository(json),
        store: InMemoryStore = InMemoryStore(),
        files: FakeFiles = FakeFiles(),
    ) = Harness(api = api, repository = repository, store = store, files = files, json = json)

    private fun pending(
        caption: String = "",
        withCover: Boolean = true,
        visibility: String = VISIBILITY_PUBLIC,
        category: String = "",
    ) = PendingReelPublish(
        creationKey = "key-1",
        videoUri = "content://video/1",
        coverPath = if (withCover) "/cache/key-1.jpg" else null,
        caption = caption,
        visibility = visibility,
        category = category,
    )

    /** Run in virtual time so the readiness clocks are deterministic. */
    private suspend fun TestScope.run(
        harness: Harness,
        pending: PendingReelPublish,
        budgetMillis: Long = ReelPublishPipeline.RUN_BUDGET_MILLIS,
    ) = harness.pipeline.run(pending, now = { testScheduler.currentTime }, runBudgetMillis = budgetMillis)

    // ── Request mapping ─────────────────────────────────────────────────

    @Test
    fun `the defaults map to the agreed wire values`() = runTest {
        val h = harness()

        val outcome = run(h, pending("Sunday skate #longboard with @maya"))

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        val request = h.repository.requests.single()
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
        assertThat(h.repository.keys).containsExactly("key-1")
    }

    /**
     * Tube (2026-09-05): the same record as a LONG video is a `long_video`
     * with the title the form required and NO `remix_setting` — the server
     * keeps no remix for long form. Everything else is the reel's mapping.
     */
    @Test
    fun `a long video maps to long_video with its title and no remix`() = runTest {
        val h = harness()

        val outcome = run(
            h,
            pending("Full walkthrough").copy(
                kind = VideoKind.LONG,
                title = "  How the feed ranks  ",
                allowRemix = false,
                hideShare = true,
            ),
        )

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        val request = h.repository.requests.single()
        assertThat(request.contentType).isEqualTo(CONTENT_TYPE_LONG_VIDEO)
        assertThat(request.title).isEqualTo("How the feed ranks")
        assertThat(request.remixSetting).isNull()
        assertThat(request.postType).isEqualTo(POST_TYPE_VIDEO)
        assertThat(request.text).isEqualTo("Full walkthrough")
        assertThat(request.hideShare).isTrue()
        assertThat(request.mediaIds).containsExactly("video-1")
        assertThat(request.coverMediaId).isEqualTo("image-2")
    }

    /** The builder alone, both kinds side by side: the ONE place the form becomes bytes. */
    @Test
    fun `buildRequest differs between the kinds in exactly three fields`() {
        val reel = ReelPublishPipeline.buildRequest(pending("hi"), "v", "c")
        val long = ReelPublishPipeline.buildRequest(pending("hi").copy(kind = VideoKind.LONG, title = "T"), "v", "c")

        assertThat(reel.contentType).isEqualTo(CONTENT_TYPE_FLICK)
        assertThat(long.contentType).isEqualTo(CONTENT_TYPE_LONG_VIDEO)
        assertThat(reel.title).isEmpty()
        assertThat(long.title).isEqualTo("T")
        assertThat(reel.remixSetting).isEqualTo(REMIX_ALLOW)
        assertThat(long.remixSetting).isNull()
        assertThat(long.copy(contentType = reel.contentType, title = reel.title, remixSetting = reel.remixSetting))
            .isEqualTo(reel)
    }

    @Test
    fun `flipping every switch flips every wire field`() = runTest {
        val h = harness()

        run(h, pending().copy(allowComments = false, hideShare = true, allowDownload = false, allowRemix = false))

        val request = h.repository.requests.single()
        assertThat(request.noComments).isTrue()
        assertThat(request.hideShare).isTrue()
        assertThat(request.allowDownload).isFalse()
        assertThat(request.remixSetting).isEqualTo(REMIX_DISALLOW)
    }

    @Test
    fun `audience goes on the wire as visibility`() = runTest {
        val h = harness()

        run(h, pending(visibility = VISIBILITY_PRIVATE))

        assertThat(h.repository.requests.single().visibility).isEqualTo(VISIBILITY_PRIVATE)
    }

    @Test
    fun `a chosen category is sent and None is omitted`() = runTest {
        val h = harness()

        run(h, pending(category = "comedy"))
        run(h, pending(category = "  ").copy(creationKey = "key-2"))

        assertThat(h.repository.requests).hasSize(2)
        assertThat(h.repository.requests.first().category).isEqualTo("comedy")
        assertThat(h.repository.requests.last().category).isNull()
    }

    @Test
    fun `tagged people and a location go on the wire`() = runTest {
        val h = harness()

        run(h, pending().copy(taggedUserIds = listOf("u-1", "u-3"), locationName = "  Marina Beach  "))

        val request = h.repository.requests.single()
        assertThat(request.taggedUserIds).containsExactly("u-1", "u-3").inOrder()
        assertThat(request.locationName).isEqualTo("Marina Beach")
    }

    // ── Instant: the create waits for nothing ───────────────────────────

    @Test
    fun `the post is created from the confirmed video without a single readiness poll`() = runTest {
        val api = FakeUploadApi().apply {
            // Were anything to poll the video it would see this and wait.
            statuses += MediaStatusDto(processingStatus = "processing", moderationStatus = "pending")
        }
        val h = harness(api = api)

        val outcome = run(h, pending(withCover = false))

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        assertThat(api.statusCalls).isEqualTo(0)
        assertThat(h.repository.requests.single().mediaIds).containsExactly("video-1")
        assertThat(h.store.record?.confirmedVideoId).isEqualTo("video-1")
        assertThat(h.store.record?.processingSinceMillis).isNull()
        assertThat(testScheduler.currentTime).isEqualTo(0L)
    }

    @Test
    fun `with a cover only the cover is polled and the video is still not`() = runTest {
        val h = harness()

        run(h, pending())

        // One status call: the cover's. FakeUploadApi answers ready+passed.
        assertThat(h.api.statusCalls).isEqualTo(1)
        assertThat(h.repository.requests.single().coverMediaId).isEqualTo("image-2")
    }

    // ── Cover ───────────────────────────────────────────────────────────

    @Test
    fun `the chosen cover is uploaded as an image and sent as the cover`() = runTest {
        val h = harness()

        run(h, pending())

        assertThat(h.api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO, FILE_TYPE_IMAGE).inOrder()
        assertThat(h.api.inits.last().mimeType).isEqualTo("image/jpeg")
        val request = h.repository.requests.single()
        assertThat(request.mediaIds).containsExactly("video-1")
        assertThat(request.coverMediaId).isEqualTo("image-2")
    }

    @Test
    fun `a failed cover upload fails the post with a retryable message and never posts without it`() = runTest {
        val h = harness(api = FakeUploadApi().apply { refuseFileType = FILE_TYPE_IMAGE })

        val outcome = run(h, pending("with a cover"))

        assertThat(outcome).isInstanceOf(ReelPublishPipeline.Outcome.Failed::class.java)
        assertThat((outcome as ReelPublishPipeline.Outcome.Failed).retryable).isTrue()
        assertThat(outcome.message).contains("cover")
        assertThat(h.repository.requests).isEmpty()
        // The failure is persisted with the confirmed video, so a restart
        // shows it and a retry does not start over.
        assertThat(h.store.record?.failure?.retryable).isTrue()
        assertThat(h.store.record?.confirmedVideoId).isEqualTo("video-1")
    }

    @Test
    fun `a retry after a cover failure reuses the ready video and does not re-upload it`() = runTest {
        val api = FakeUploadApi().apply { refuseFileType = FILE_TYPE_IMAGE }
        val h = harness(api = api)

        run(h, pending())
        api.refuseFileType = null
        val outcome = run(h, h.store.record!!)

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        assertThat(api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO, FILE_TYPE_IMAGE, FILE_TYPE_IMAGE)
        assertThat(h.files.stashed).hasSize(1)
        assertThat(h.repository.requests.single().coverMediaId).isEqualTo("image-2")
        assertThat(h.repository.requests.single().mediaIds).containsExactly("video-1")
    }

    /** The cover's short readiness wait is kept: the video is instant, the cover is not. */
    @Test
    fun `a cover that is not ready and passed is never attached`() = runTest {
        val api = FakeUploadApi().apply {
            // The cover sits at ready/pending for the whole window.
            repeat(40) { statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "pending") }
        }
        val h = harness(api = api)

        val outcome = run(h, pending())

        assertThat(h.repository.requests).isEmpty()
        assertThat(outcome).isInstanceOf(ReelPublishPipeline.Outcome.Failed::class.java)
        assertThat((outcome as ReelPublishPipeline.Outcome.Failed).message).contains("cover")
    }

    @Test
    fun `with no extractable frame the post goes out without a cover`() = runTest {
        val h = harness()

        run(h, pending(withCover = false))

        assertThat(h.api.inits.map { it.fileType }).containsExactly(FILE_TYPE_VIDEO)
        assertThat(h.repository.requests.single().coverMediaId).isNull()
    }

    // ── Create failures and resumption ──────────────────────────────────

    @Test
    fun `a failed create keeps both ready ids and the creation key for the retry`() = runTest {
        val repository = RecordingRepository(json).apply { result = AppResult.Failure(AppError.NoNetwork()) }
        val h = harness(repository = repository)

        val first = run(h, pending())
        repository.result = AppResult.Success("post-2")
        val second = run(h, h.store.record!!)

        assertThat((first as ReelPublishPipeline.Outcome.Failed).retryable).isTrue()
        assertThat(second).isEqualTo(ReelPublishPipeline.Outcome.Published("post-2"))
        assertThat(h.api.inits).hasSize(2)
        assertThat(repository.keys.distinct()).containsExactly("key-1")
        assertThat(repository.requests).hasSize(2)
    }

    @Test
    fun `a restart after confirmation creates from the confirmed id and never re-uploads`() = runTest {
        val h = harness()
        val resumed = pending().copy(
            videoPath = "/cache/key-1.video",
            videoMimeType = "video/mp4",
            confirmedVideoId = "video-9",
        )

        val outcome = run(h, resumed)

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        assertThat(h.files.stashed).isEmpty()
        assertThat(h.api.inits.map { it.fileType }).containsExactly(FILE_TYPE_IMAGE)
        assertThat(h.repository.requests.single().mediaIds).containsExactly("video-9")
    }

    // ── Fallback: a server that still wants ready+passed ────────────────

    @Test
    fun `MEDIA_NOT_READY alone sends the pipeline polling and it creates again when the video is ready`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "processing", moderationStatus = "pending")
            statuses += MediaStatusDto(processingStatus = "processing", moderationStatus = "pending")
            statuses += MediaStatusDto(processingStatus = "ready", moderationStatus = "passed")
        }
        val repository = RecordingRepository(json).apply { queued += notReady }
        val h = harness(api = api, repository = repository)
        val seen = mutableListOf<ReelPublishState>()
        val watching = launch(Dispatchers.Unconfined) {
            h.tracker.items.collect { seen += it.firstOrNull()?.state ?: ReelPublishState.Idle }
        }

        val outcome = run(h, pending(withCover = false))

        watching.cancel()
        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Published("post-1"))
        assertThat(repository.requests).hasSize(2)
        assertThat(repository.keys.distinct()).containsExactly("key-1")
        assertThat(api.statusCalls).isEqualTo(3)
        assertThat(seen).containsAtLeast(ReelPublishState.Posting, ReelPublishState.Processing).inOrder()
        assertThat(h.store.record?.processingSinceMillis).isEqualTo(0L)
    }

    @Test
    fun `any other create failure is not polled`() = runTest {
        val repository = RecordingRepository(json).apply {
            result = AppResult.Failure(AppError.Server(statusCode = 409, code = "MEDIA_NOT_OWNED"))
        }
        val h = harness(repository = repository)

        val outcome = run(h, pending(withCover = false))

        assertThat(outcome).isInstanceOf(ReelPublishPipeline.Outcome.Failed::class.java)
        assertThat((outcome as ReelPublishPipeline.Outcome.Failed).retryable).isFalse()
        assertThat(h.api.statusCalls).isEqualTo(0)
        assertThat(repository.requests).hasSize(1)
    }

    @Test
    fun `a fallback run whose budget ends mid-transcode hands off to a continuation with the checkpoint saved`() =
        runTest {
            val api = FakeUploadApi().apply {
                statuses += MediaStatusDto(processingStatus = "processing", moderationStatus = "pending")
            }
            val repository = RecordingRepository(json).apply { result = notReady }
            val h = harness(api = api, repository = repository)

            val outcome = run(h, pending(withCover = false), budgetMillis = 30_000L)

            assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Continue)
            assertThat(h.store.record?.confirmedVideoId).isEqualTo("video-1")
            assertThat(h.store.record?.processingSinceMillis).isNotNull()
            assertThat(h.store.record?.failure).isNull()
            assertThat(h.tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Processing)
            assertThat(repository.requests).hasSize(1)
        }

    @Test
    fun `the fallback window closes after thirty minutes with a retryable failure`() = runTest {
        val api = FakeUploadApi().apply {
            statuses += MediaStatusDto(processingStatus = "processing", moderationStatus = "pending")
        }
        val repository = RecordingRepository(json).apply { result = notReady }
        val h = harness(api = api, repository = repository)

        val outcome = run(h, pending(withCover = false), budgetMillis = 60L * 60L * 1_000L)

        assertThat(outcome).isInstanceOf(ReelPublishPipeline.Outcome.Failed::class.java)
        assertThat((outcome as ReelPublishPipeline.Outcome.Failed).retryable).isTrue()
        assertThat(outcome.message).contains("too long")
        assertThat(testScheduler.currentTime).isAtLeast(ReelMediaUploads.VIDEO_READINESS_WINDOW_MILLIS)
        assertThat(h.store.record?.confirmedVideoId).isEqualTo("video-1")
    }

    @Test
    fun `an unreadable video fails without a retry and uploads nothing`() = runTest {
        val h = harness(files = FakeFiles().apply { unreadable = true })

        val outcome = run(h, pending())

        assertThat(outcome).isEqualTo(
            ReelPublishPipeline.Outcome.Failed("That video can't be read. Pick it again.", retryable = false),
        )
        assertThat(h.api.inits).isEmpty()
        assertThat(h.tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Preparing)
    }

    @Test
    fun `progress reaches the tracker one percent at a time`() = runTest {
        val h = harness()
        val seen = mutableListOf<ReelPublishState>()
        val watching = launch(Dispatchers.Unconfined) {
            h.tracker.items.collect { seen += it.firstOrNull()?.state ?: ReelPublishState.Idle }
        }

        run(h, pending())

        watching.cancel()
        // A StateFlow conflates, so the exact percents seen depend on the
        // collector's timing; the phases and the direction of travel do not.
        assertThat(seen).containsAtLeast(
            ReelPublishState.Preparing,
            ReelPublishState.Uploading(0f),
            ReelPublishState.Posting,
        ).inOrder()
        // Instant: the post never sits in Processing on the happy path.
        assertThat(seen).doesNotContain(ReelPublishState.Processing)
        val fractions = seen.filterIsInstance<ReelPublishState.Uploading>().map { it.fraction }
        assertThat(fractions).isNotEmpty()
        assertThat(fractions).isInOrder()
        assertThat(fractions.last()).isAtMost(1f)
    }

    // ── The details step's fields (2026-09-05) ─────────────────────────

    @Test
    fun `hashtags, mentions and a schedule go on the wire as their own fields`() = runTest {
        val h = harness()

        run(
            h,
            pending("no tags in here").copy(
                hashtags = listOf("longboard", "Sunday"),
                mentions = listOf("maya"),
                taggedUserIds = listOf("u-1"),
                publishAt = "2026-09-06T13:00:00Z",
            ),
        )

        val request = h.repository.requests.single()
        assertThat(request.text).isEqualTo("no tags in here")
        assertThat(request.hashtags).containsExactly("longboard", "Sunday").inOrder()
        assertThat(request.mentions).containsExactly("maya")
        assertThat(request.taggedUserIds).containsExactly("u-1")
        assertThat(request.publishAt).isEqualTo("2026-09-06T13:00:00Z")
    }

    /** Absent fields stay absent: a post with none of them is byte-identical to before they existed. */
    @Test
    fun `empty chips and no schedule are omitted rather than sent empty`() {
        val request = ReelPublishPipeline.buildRequest(pending("hi"), "v", null)

        assertThat(request.hashtags).isNull()
        assertThat(request.mentions).isNull()
        assertThat(request.publishAt).isNull()
        assertThat(json.encodeToString(CreatePostRequest.serializer(), request)).doesNotContain("publish_at")
    }

    /** A 400 the client cannot name — the server refusing a `publish_at` — is shown in the server's words. */
    @Test
    fun `a refused schedule fails with the server's own message and no retry`() = runTest {
        val repository = RecordingRepository(json).apply {
            result = AppResult.Failure(
                AppError.Unknown(
                    code = "INVALID_PUBLISH_AT",
                    statusCode = 400,
                    message = "publish_at must be at least 5 minutes ahead",
                ),
            )
        }
        val h = harness(repository = repository)

        val outcome = run(h, pending(withCover = false).copy(publishAt = "2026-09-05T00:00:00Z"))

        assertThat(outcome).isEqualTo(
            ReelPublishPipeline.Outcome.Failed("publish_at must be at least 5 minutes ahead", retryable = false),
        )
        assertThat(h.store.record?.failure?.message).isEqualTo("publish_at must be at least 5 minutes ahead")
    }

    // ── The queue (2026-09-05) ──────────────────────────────────────────

    /** A publish discarded while it uploads stops, persists nothing, and leaves the store to the discard. */
    @Test
    fun `a discard mid-upload stops the run without writing the record back`() = runTest {
        val h = harness()
        h.store.save(pending())
        var discarded = false
        val api = h.api

        val outcome = h.pipeline.run(
            pending(),
            now = { testScheduler.currentTime },
            isDiscarded = {
                // The first progress tick discards; everything after must stop.
                if (api.inits.isNotEmpty()) discarded = true
                discarded
            },
        )

        assertThat(outcome).isEqualTo(ReelPublishPipeline.Outcome.Discarded)
        assertThat(h.repository.requests).isEmpty()
        assertThat(h.store.record?.confirmedVideoId).isNull()
        assertThat(h.store.record?.failure).isNull()
    }

    /** Two records saved in order come back in that order — the worker runs them first to last. */
    @Test
    fun `the store keeps two pending publishes in the order they were saved`() = runTest {
        val h = harness()

        h.store.save(pending("first"))
        h.store.save(pending("second").copy(creationKey = "key-2"))
        h.store.save(pending("first, updated"))

        assertThat(h.store.loadAll().map { it.creationKey }).containsExactly("key-1", "key-2").inOrder()
        assertThat(h.store.load("key-1")?.caption).isEqualTo("first, updated")

        run(h, h.store.load("key-1")!!)
        run(h, h.store.load("key-2")!!)

        assertThat(h.repository.keys).containsExactly("key-1", "key-2").inOrder()
        assertThat(h.tracker.items.value.map { it.creationKey }).containsExactly("key-1", "key-2").inOrder()
    }
}
