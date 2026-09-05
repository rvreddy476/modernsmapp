package com.us.android.feature.post.createhub

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelRepository
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
import com.us.android.feature.post.data.dto.CONTENT_TYPE_LONG_VIDEO
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_VIDEO
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.math.min

/**
 * The reel publish, resumable from any checkpoint.
 *
 * ## THE PIPELINE
 *
 * stash the video → upload it → confirm → upload the cover (short wait) →
 * ONE create through the one create call site, IMMEDIATELY. Every step that
 * produces something durable writes it to the [ReelPublishStore] before
 * moving on, and every step is skipped when the record already has its
 * result — so a retry after a failed cover never re-uploads the video, and a
 * retry after a failed create keeps both ids and the creation key.
 *
 * ## WHY THERE IS NO WAIT FOR THE TRANSCODE
 *
 * Instant reels (founder, 2026-09-04): "once the user posts a video it
 * should upload immediately, like Instagram". post-service now creates a
 * flick as soon as the video is CONFIRMED — no `ready`, no `passed` — and
 * shows it to its author at once with `is_processing: true`, playing the
 * original file until the ladder exists. So the thirty-minute readiness
 * window is gone from the happy path: the post goes out the moment the bytes
 * and the cover are up, which on a good connection is seconds.
 *
 * ## THE FALLBACK
 *
 * A post-service that has NOT yet landed that change still answers the
 * create with `MEDIA_NOT_READY`. That one code is not treated as terminal
 * here: the pipeline falls back to the old behaviour — poll the confirmed
 * id under the run budget ([Outcome.Continue] hands off to the next
 * WorkManager run) and create again when the video is ready+passed. The
 * fallback reports [ReelPublishState.Processing] so the pending item keeps
 * its loader instead of showing a failure the user cannot act on.
 *
 * A cover that fails to upload FAILS THE POST with a retryable message —
 * posting without the cover the user chose would be publishing something
 * other than what they approved.
 */
@Singleton
class ReelPublishPipeline @Inject constructor(
    private val repository: ComposerRepository,
    private val uploads: ReelMediaUploads,
    private val files: ReelPublishFiles,
    private val store: ReelPublishStore,
    private val tracker: ReelPublishTracker,
) {

    sealed interface Outcome {
        data class Published(val postId: String) : Outcome

        /** The fallback poll is still waiting and this run's budget is spent. */
        data object Continue : Outcome
        data class Failed(val message: String, val retryable: Boolean, val needsChannel: Boolean = false) : Outcome
    }

    /** Thrown internally to unwind to [run] with the failure already persisted. */
    private class Stop(val outcome: Outcome) : RuntimeException()

    /**
     * Run from wherever [initial] had got to. [now] and [runBudgetMillis] are
     * parameters so the fallback's window can be tested in virtual time.
     */
    suspend fun run(
        initial: PendingReelPublish,
        now: () -> Long = System::currentTimeMillis,
        runBudgetMillis: Long = RUN_BUDGET_MILLIS,
    ): Outcome {
        val runUntil = now() + runBudgetMillis
        var pending = initial.copy(failure = null)
        return try {
            pending = stashVideo(pending)
            pending = uploadVideo(pending)
            pending = uploadCover(pending)
            createFlick(pending, now, runUntil)
        } catch (stop: Stop) {
            stop.outcome
        }
    }

    // ── Steps ───────────────────────────────────────────────────────────

    private suspend fun stashVideo(pending: PendingReelPublish): PendingReelPublish {
        if (pending.videoPath != null) return pending
        tracker.update(ReelPublishState.Preparing)
        val stashed = files.stashVideo(pending.videoUri, pending.creationKey)
            ?: fail(pending, "That video can't be read. Pick it again.", retryable = false)
        return checkpoint(pending.copy(videoPath = stashed.path, videoMimeType = stashed.mimeType))
    }

    private suspend fun uploadVideo(pending: PendingReelPublish): PendingReelPublish {
        if (pending.confirmedVideoId != null) return pending
        val picked = files.openVideo(pending.videoPath.orEmpty(), pending.videoMimeType.orEmpty())
            ?: fail(pending, "That video can't be read. Pick it again.", retryable = false)
        tracker.update(ReelPublishState.Uploading(0f))
        var lastPercent = -1
        val outcome = uploads.uploadVideo(picked) { fraction ->
            // One state per percent: the PUT reports every buffer, and a
            // StateFlow update per 8 KB of a 300 MB video is churn for nothing.
            val percent = (fraction * PERCENT).toInt()
            if (percent != lastPercent) {
                lastPercent = percent
                tracker.update(ReelPublishState.Uploading(fraction))
            }
        }
        return when (outcome) {
            is ReelMediaUploads.Outcome.Ready -> checkpoint(
                pending.copy(confirmedVideoId = outcome.mediaId, processingSinceMillis = null),
            )
            is ReelMediaUploads.Outcome.Failed -> fail(pending, outcome.message, outcome.retryable)
        }
    }

    private suspend fun uploadCover(pending: PendingReelPublish): PendingReelPublish {
        val path = pending.coverPath ?: return pending
        if (pending.readyCoverId != null) return pending
        tracker.update(ReelPublishState.Posting)
        val bytes = files.readBytes(path)
            ?: fail(pending, "That cover frame couldn't be prepared. Pick another.", retryable = false)
        return when (val outcome = uploads.uploadCover(bytes)) {
            is ReelMediaUploads.Outcome.Ready -> checkpoint(pending.copy(readyCoverId = outcome.mediaId))
            is ReelMediaUploads.Outcome.Failed -> fail(pending, outcome.message, outcome.retryable)
        }
    }

    /**
     * The create, straight away. On `MEDIA_NOT_READY` — the pre-instant
     * server — wait for the transcode the old way and create once more.
     */
    private suspend fun createFlick(pending: PendingReelPublish, now: () -> Long, runUntil: Long): Outcome {
        tracker.update(ReelPublishState.Posting)
        val videoId = pending.confirmedVideoId ?: fail(pending, "The upload didn't finish. Try again.", true)
        val request = buildRequest(pending, videoId, pending.readyCoverId)
        return when (val result = repository.createPost(pending.creationKey, request)) {
            is AppResult.Success -> Outcome.Published(result.data)
            is AppResult.Failure -> if (result.error.isNotReady()) {
                val ready = awaitVideo(pending, videoId, now, runUntil)
                createReadyFlick(ready, request)
            } else {
                refused(pending, result.error)
            }
        }
    }

    private suspend fun createReadyFlick(pending: PendingReelPublish, request: CreatePostRequest): Outcome =
        when (val result = repository.createPost(pending.creationKey, request)) {
            is AppResult.Success -> Outcome.Published(result.data)
            is AppResult.Failure -> refused(pending, result.error)
        }

    /**
     * The create was refused. `CHANNEL_REQUIRED` (channel before video,
     * 2026-09-05) is not terminal even though it is a 403: the pending tile
     * opens the create-channel sheet and retries once there is one.
     */
    private suspend fun refused(pending: PendingReelPublish, error: AppError): Nothing =
        if (ChannelRepository.requiresChannel(error)) {
            fail(pending, CHANNEL_REQUIRED_MESSAGE, retryable = true, needsChannel = true)
        } else {
            fail(pending, repository.message(error), retryable = !repository.isTerminal(error))
        }

    /**
     * FALLBACK ONLY. The window runs from the first poll and is persisted,
     * so a continuation or a restart keeps the same clock.
     */
    private suspend fun awaitVideo(
        pending: PendingReelPublish,
        mediaId: String,
        now: () -> Long,
        runUntil: Long,
    ): PendingReelPublish {
        tracker.update(ReelPublishState.Processing)
        val since = pending.processingSinceMillis ?: now()
        val current = if (pending.processingSinceMillis == null) {
            checkpoint(pending.copy(processingSinceMillis = since))
        } else {
            pending
        }
        val windowEnds = since + ReelMediaUploads.VIDEO_READINESS_WINDOW_MILLIS
        return when (val readiness = uploads.awaitVideoReady(mediaId, min(windowEnds, runUntil), now)) {
            ReelMediaUploads.Readiness.Ready -> current
            ReelMediaUploads.Readiness.Pending ->
                if (now() >= windowEnds) {
                    fail(current, "Processing is taking too long. Try again in a minute.", retryable = true)
                } else {
                    throw Stop(Outcome.Continue)
                }
            is ReelMediaUploads.Readiness.Failed -> fail(current, readiness.message, readiness.retryable)
        }
    }

    // ── Plumbing ────────────────────────────────────────────────────────

    private suspend fun checkpoint(pending: PendingReelPublish): PendingReelPublish {
        store.save(pending)
        return pending
    }

    /** Persist the failure so a restart shows it, then unwind. */
    private suspend fun fail(
        pending: PendingReelPublish,
        message: String,
        retryable: Boolean,
        needsChannel: Boolean = false,
    ): Nothing {
        store.save(pending.copy(failure = PendingReelFailure(message, retryable, needsChannel)))
        throw Stop(Outcome.Failed(message, retryable, needsChannel))
    }

    private fun AppError.isNotReady(): Boolean = this is AppError.Server && code == MEDIA_NOT_READY

    companion object {
        private const val PERCENT = 100

        /** post-service's refusal of a confirmed-but-untranscoded video. */
        const val MEDIA_NOT_READY = "MEDIA_NOT_READY"

        /** What the pending tile says under a 403 `CHANNEL_REQUIRED`. */
        const val CHANNEL_REQUIRED_MESSAGE = "Create a channel to post videos."

        /**
         * Under WorkManager's ten-minute stop, with room for the create after
         * a fallback poll returns.
         */
        const val RUN_BUDGET_MILLIS = 8L * 60L * 1_000L

        /**
         * The ONE place the form becomes bytes, for BOTH kinds of video (Tube,
         * 2026-09-05). The switches go on the wire whatever their value; the
         * optional fields are omitted when unset so an empty category is
         * "none", not `""`.
         *
         * A reel is a `flick` with an empty title and its remix switch. A long
         * video is a `long_video` with the title the form required and NO
         * `remix_setting` at all — the server has no remix for long form, and
         * sending one would record a control nothing enforces.
         */
        fun buildRequest(pending: PendingReelPublish, videoId: String, coverId: String?) = CreatePostRequest(
            text = pending.caption.trim(),
            visibility = pending.visibility,
            contentType = when (pending.kind) {
                VideoKind.REEL -> CONTENT_TYPE_FLICK
                VideoKind.LONG -> CONTENT_TYPE_LONG_VIDEO
            },
            postType = POST_TYPE_VIDEO,
            mediaIds = listOf(videoId),
            language = DEFAULT_LANGUAGE,
            distribution = DistributionRequest(),
            title = when (pending.kind) {
                VideoKind.REEL -> ""
                VideoKind.LONG -> pending.title.trim()
            },
            noComments = !pending.allowComments,
            hideShare = pending.hideShare,
            allowDownload = pending.allowDownload,
            remixSetting = when (pending.kind) {
                VideoKind.REEL -> if (pending.allowRemix) REMIX_ALLOW else REMIX_DISALLOW
                VideoKind.LONG -> null
            },
            category = pending.category.trim().ifBlank { null },
            coverMediaId = coverId,
            taggedUserIds = pending.taggedUserIds.takeIf { it.isNotEmpty() },
            locationName = pending.locationName.trim().ifBlank { null },
        )

        private const val DEFAULT_LANGUAGE = "en"
    }
}
