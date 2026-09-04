package com.us.android.feature.post.createhub

import com.us.android.core.common.result.AppResult
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
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
 * stash the video → upload it → wait for EXACT ready+passed → upload the
 * cover → wait for it → ONE create through the one create call site. Every
 * step that produces something durable writes it to the [ReelPublishStore]
 * before moving on, and every step is skipped when the record already has
 * its result — so a retry after a failed cover never re-uploads the video,
 * a retry after a failed create keeps both ids and the creation key, and a
 * process death mid-transcode resumes polling the confirmed id.
 *
 * ## TIME
 *
 * A WorkManager run is stopped at ten minutes; a transcode can take thirty.
 * [run] takes a run budget and reports [Outcome.Continue] when the video is
 * still processing as the budget ends, so the worker can enqueue itself
 * again. The 30-minute readiness window runs from the moment the upload was
 * confirmed, persisted, so it is honest across runs and across restarts.
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

        /** The video is still processing and this run's budget is spent. */
        data object Continue : Outcome
        data class Failed(val message: String, val retryable: Boolean) : Outcome
    }

    /** Thrown internally to unwind to [run] with the failure already persisted. */
    private class Stop(val outcome: Outcome) : RuntimeException()

    /**
     * Run from wherever [initial] had got to. [now] and [runBudgetMillis] are
     * parameters so the readiness window can be tested in virtual time.
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
            pending = awaitVideo(pending, now, runUntil)
            pending = uploadCover(pending)
            createFlick(pending)
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
        if (pending.readyVideoId != null || pending.confirmedVideoId != null) return pending
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

    private suspend fun awaitVideo(
        pending: PendingReelPublish,
        now: () -> Long,
        runUntil: Long,
    ): PendingReelPublish {
        if (pending.readyVideoId != null) return pending
        val mediaId = pending.confirmedVideoId ?: fail(pending, "The upload didn't finish. Try again.", true)
        tracker.update(ReelPublishState.Processing)
        // The window runs from the first poll after confirmation and is
        // persisted, so a continuation or a restart keeps the same clock.
        val since = pending.processingSinceMillis ?: now()
        val current = if (pending.processingSinceMillis == null) {
            checkpoint(pending.copy(processingSinceMillis = since))
        } else {
            pending
        }
        val windowEnds = since + ReelMediaUploads.VIDEO_READINESS_WINDOW_MILLIS
        return when (val readiness = uploads.awaitVideoReady(mediaId, min(windowEnds, runUntil), now)) {
            ReelMediaUploads.Readiness.Ready -> checkpoint(current.copy(readyVideoId = mediaId))
            ReelMediaUploads.Readiness.Pending ->
                if (now() >= windowEnds) {
                    fail(current, "Processing is taking too long. Try again in a minute.", retryable = true)
                } else {
                    throw Stop(Outcome.Continue)
                }
            is ReelMediaUploads.Readiness.Failed -> fail(current, readiness.message, readiness.retryable)
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

    private suspend fun createFlick(pending: PendingReelPublish): Outcome {
        tracker.update(ReelPublishState.Posting)
        val videoId = pending.readyVideoId ?: fail(pending, "The upload didn't finish. Try again.", true)
        val request = buildRequest(pending, videoId, pending.readyCoverId)
        return when (val result = repository.createPost(pending.creationKey, request)) {
            is AppResult.Success -> Outcome.Published(result.data)
            is AppResult.Failure ->
                fail(pending, repository.message(result.error), retryable = !repository.isTerminal(result.error))
        }
    }

    // ── Plumbing ────────────────────────────────────────────────────────

    private suspend fun checkpoint(pending: PendingReelPublish): PendingReelPublish {
        store.save(pending)
        return pending
    }

    /** Persist the failure so a restart shows it, then unwind. */
    private suspend fun fail(pending: PendingReelPublish, message: String, retryable: Boolean): Nothing {
        store.save(pending.copy(failure = PendingReelFailure(message, retryable)))
        throw Stop(Outcome.Failed(message, retryable))
    }

    companion object {
        private const val PERCENT = 100

        /**
         * Under WorkManager's ten-minute stop, with room for the cover and
         * the create after the poll returns.
         */
        const val RUN_BUDGET_MILLIS = 8L * 60L * 1_000L

        /**
         * The ONE place the form becomes bytes. The switches go on the wire
         * whatever their value; the optional fields are omitted when unset so
         * an empty category is "none", not `""`.
         */
        fun buildRequest(pending: PendingReelPublish, videoId: String, coverId: String?) = CreatePostRequest(
            text = pending.caption.trim(),
            visibility = pending.visibility,
            contentType = CONTENT_TYPE_FLICK,
            postType = POST_TYPE_VIDEO,
            mediaIds = listOf(videoId),
            language = DEFAULT_LANGUAGE,
            distribution = DistributionRequest(),
            title = "",
            noComments = !pending.allowComments,
            hideShare = pending.hideShare,
            allowDownload = pending.allowDownload,
            remixSetting = if (pending.allowRemix) REMIX_ALLOW else REMIX_DISALLOW,
            category = pending.category.trim().ifBlank { null },
            coverMediaId = coverId,
            taggedUserIds = pending.taggedUserIds.takeIf { it.isNotEmpty() },
            locationName = pending.locationName.trim().ifBlank { null },
        )

        private const val DEFAULT_LANGUAGE = "en"
    }
}
