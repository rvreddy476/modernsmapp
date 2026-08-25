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
 */
@HiltWorker
class PublishWorker @AssistedInject constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val publisher: CreatorPublisher,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val projectId = inputData.getString(KEY_PROJECT_ID)
            ?: return Result.failure(reason("no project id"))

        return when (val outcome = publisher.publish(projectId)) {
            is CreatorPublisher.PublishResult.Published ->
                Result.success(workDataOf(KEY_POST_ID to outcome.postId))

            is CreatorPublisher.PublishResult.Retryable ->
                if (runAttemptCount < MAX_ATTEMPTS) {
                    Result.retry()
                } else {
                    // The checkpoints hold: nothing is lost, and the Studio's
                    // failure state offers a manual retry with the same
                    // operation and the same bytes.
                    Result.failure(reason(outcome.reason))
                }

            is CreatorPublisher.PublishResult.Failed ->
                Result.failure(reason(outcome.reason))
        }
    }

    private fun reason(value: String): Data = workDataOf(KEY_FAILURE_REASON to value)

    companion object {
        const val KEY_PROJECT_ID = "projectId"
        const val KEY_POST_ID = "postId"
        const val KEY_FAILURE_REASON = "reason"
        private const val MAX_ATTEMPTS = 5
        private const val BACKOFF_SECONDS = 10L

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
