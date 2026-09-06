package com.us.android.feature.post.studio

import android.content.Context
import androidx.hilt.work.HiltWorker
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.Data
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.us.android.core.creator.engine.CreatorPublisher
import com.us.android.core.creator.engine.ProjectStore
import com.us.android.core.creator.engine.SourceVault
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import java.util.concurrent.TimeUnit

/**
 * Background continuation for a Studio publish.
 *
 * ## WHY WORKMANAGER, AND WHY IT IS SAFE HERE
 *
 * A ten-page publish outlives a screen. WorkManager restarts the work after
 * process death or reboot — and restarting is SAFE only because everything
 * underneath is idempotent: pages already confirmed are skipped (the project
 * document is the checkpoint), the frozen operation replays byte-identical
 * bytes under its existing key, and the live-slot table stops any parallel
 * publish of the same project. The worker adds scheduling, not semantics.
 *
 * ## RETRY POLICY
 *
 * A [CreatorPublisher.PublishResult.Retryable] outcome returns
 * [androidx.work.ListenableWorker.Result.retry] with exponential backoff and a
 * bounded attempt count; a Failed outcome is terminal and surfaces through the
 * project's stored state. WorkManager's KEEP policy means re-tapping Publish
 * while a publish is already queued does not enqueue a second one.
 *
 * ## WHY IT REPORTS TO THE REEL PUBLISH TRACKER
 *
 * Pressing Post now leaves the studio at once and lands on the profile, where
 * the upload shows its progress (founder, 2026-09-06). That progress channel
 * already exists — `ReelPublishTracker`, which the profile grid and the Reels
 * tab read — so this worker writes into it rather than growing a second one.
 * The photo publish keeps its OWN pipeline ([CreatorPublisher], with the page
 * checkpoints and the frozen operation, which a one-video record could not
 * carry); only the progress it announces is shared, which is exactly the seam
 * the tracker was carved out for.
 */
@HiltWorker
class PublishWorker @AssistedInject constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val publisher: CreatorPublisher,
    private val store: ProjectStore,
    private val vault: SourceVault,
    private val tracker: ReelPublishTracker,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val projectId = inputData.getString(KEY_PROJECT_ID)
            ?: return Result.failure(reason("no project id"))

        // Put the tile back. On the first run the ViewModel has already set
        // this and the call is a no-op overwrite; after process death the
        // tracker is empty and the profile has nothing to draw until it is
        // restored from the durable document.
        restorePreview(projectId)

        return when (val outcome = publisher.publish(projectId) { report(it) }) {
            is CreatorPublisher.PublishResult.Published -> {
                tracker.update(projectId, ReelPublishState.Published(outcome.postId))
                Result.success(workDataOf(KEY_POST_ID to outcome.postId))
            }

            is CreatorPublisher.PublishResult.Retryable ->
                if (runAttemptCount < MAX_ATTEMPTS) {
                    Result.retry()
                } else {
                    // The checkpoints hold: nothing is lost, and the Studio's
                    // failure state offers a manual retry with the same
                    // operation and the same bytes.
                    tracker.update(projectId, failure(outcome.reason, retryable = true))
                    Result.failure(reason(outcome.reason))
                }

            is CreatorPublisher.PublishResult.Failed -> {
                tracker.update(projectId, failure(outcome.reason, retryable = false))
                Result.failure(reason(outcome.reason))
            }
        }
    }

    /**
     * The publisher's progress as the queue's own vocabulary.
     *
     * A carousel is N pages and each page is rendered and then uploaded, so
     * the bar counts HALF-PAGES: page i rendering is 2i of 2N done, page i
     * uploading is 2i+1. That is the honest granularity — the publisher does
     * not report bytes within a page — and it moves monotonically, which is
     * what the tracker's ring wants.
     */
    private fun report(progress: CreatorPublisher.PublishProgress) {
        val projectId = inputData.getString(KEY_PROJECT_ID) ?: return
        val state = when (progress) {
            is CreatorPublisher.PublishProgress.RenderingPage ->
                ReelPublishState.Uploading(fraction(2 * progress.index, progress.total))
            is CreatorPublisher.PublishProgress.UploadingPage ->
                ReelPublishState.Uploading(fraction(2 * progress.index + 1, progress.total))
            CreatorPublisher.PublishProgress.CreatingPost -> ReelPublishState.Posting
        }
        tracker.update(projectId, state)
    }

    private fun fraction(halfPagesDone: Int, totalPages: Int): Float =
        if (totalPages <= 0) 0f else halfPagesDone.toFloat() / (2 * totalPages)

    private suspend fun restorePreview(projectId: String) {
        val loaded = store.load(projectId) as? ProjectStore.LoadResult.Loaded ?: return
        tracker.setPreview(studioPublishPreview(loaded.project, vault))
        if (tracker.stateOf(projectId) is ReelPublishState.Idle) {
            tracker.update(projectId, ReelPublishState.Preparing)
        }
    }

    /**
     * The pending tile's failure, in words a person can act on.
     *
     * The publisher's own reasons are diagnostics — "reserve failed",
     * "confirm failed", an `AppError` class name — and they still go into the
     * work's output data, which is where a bug report should read them. What
     * the profile draws has to say what happened and whether trying again is
     * worth it, so the one refusal a retry cannot fix (moderation) is named
     * and everything else is grouped by whether it is worth another go.
     */
    private fun failure(reason: String, retryable: Boolean) = ReelPublishState.Failed(
        message = when {
            reason.startsWith(REJECTED_PREFIX) -> "A photo in this post wasn't allowed."
            retryable -> "That didn't finish uploading. Try again."
            else -> "This post couldn't be published."
        },
        retryable = retryable,
    )

    private fun reason(value: String): Data = workDataOf(KEY_FAILURE_REASON to value)

    companion object {
        const val KEY_PROJECT_ID = "projectId"
        const val KEY_POST_ID = "postId"
        const val KEY_FAILURE_REASON = "reason"
        private const val MAX_ATTEMPTS = 5
        private const val BACKOFF_SECONDS = 10L

        /** How the transport words a moderation or processing rejection. */
        private const val REJECTED_PREFIX = "media was rejected"

        fun uniqueName(projectId: String) = "creator-publish-$projectId"

        /**
         * Enqueue a publish for one project.
         *
         * KEEP, not REPLACE: a second tap while a publish is running must not
         * cancel and restart mid-upload — the running work already owns the
         * live slot and the checkpoints.
         */
        fun enqueue(context: Context, projectId: String) {
            val request = OneTimeWorkRequestBuilder<PublishWorker>()
                .setInputData(workDataOf(KEY_PROJECT_ID to projectId))
                .setConstraints(
                    Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build(),
                )
                .setBackoffCriteria(
                    BackoffPolicy.EXPONENTIAL,
                    BACKOFF_SECONDS,
                    TimeUnit.SECONDS,
                )
                .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork(uniqueName(projectId), ExistingWorkPolicy.KEEP, request)
        }
    }
}
