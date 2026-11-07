package com.us.android.feature.post.createhub

import android.util.Log
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import kotlinx.coroutines.CancellationException

/**
 * One worker run of a reel publish, minus WorkManager: the pipeline, a
 * backstop that no exception escapes, and what the run ends in. Split out
 * of [ReelPublishWorker] (2026-11-07) so it is tested on the JVM.
 *
 * ## NOTHING ESCAPES
 *
 * The worker is a link in ONE chain, and a link that fails cancels every
 * publish queued behind it. A lapsed Photo Picker grant used to throw
 * `SecurityException` out of the copy step (audit 2026-10-30): the run
 * failed, the queue behind it was cancelled, and the record — with no
 * failure written — was re-enqueued as "Preparing" on every launch, to
 * crash again. Now any exception from the pipeline is RECORDED as a
 * retryable failure, so the tile offers Retry and the queue goes on.
 *
 * ## THE NETWORK'S FAILURES ARE RETRIED BY THEMSELVES
 *
 * A [ReelPublishPipeline.Outcome.Failed] marked transient (no connection,
 * a timeout, the server's 5xx) is retried with WorkManager's backoff, up to
 * [AUTO_RETRIES] times, while the tile keeps its loader; the user taps
 * Retry only when that runs out or the failure is the file's or the form's.
 *
 * ## A LONG UPLOAD THAT KEEPS MOVING IS NEVER GIVEN UP ON (audit M4, 2026-11-07)
 *
 * A multi-GB long video goes up part by part for hours, and a phone loses
 * signal more than three times in that (a lift, a tunnel). A transient
 * failure AFTER parts landed in this run is not counted against
 * [AUTO_RETRIES]: the run ends in [End.Continue], a fresh run that waits for
 * the network and resumes from the last confirmed part. Each such run must
 * land at least one part to earn the next, so it is bounded by the part
 * count; a run that lands nothing falls back to the counted retries.
 */
internal class ReelPublishRun(
    private val pipeline: suspend (PendingReelPublish, isDiscarded: () -> Boolean) -> ReelPublishPipeline.Outcome,
    private val store: ReelPublishStore,
    private val files: ReelPublishFiles,
    private val tracker: ReelPublishTracker,
    private val discards: ReelPublishDiscards,
) {

    sealed interface End {
        /** Done with this record for now; [failure] is the message when it stopped. */
        data class Finished(val postId: String? = null, val failure: String? = null) : End

        /**
         * Not done and not failed: append a fresh run. The fallback poll is
         * still waiting, or a resumable upload moved on before the network
         * went and picks up from its last part once it is back.
         */
        data object Continue : End

        /** The network failed it: run again after the backoff. */
        data object RetryLater : End
    }

    /** Runs [pending]; [attempt] is how many times WorkManager has already retried this run. */
    suspend fun run(pending: PendingReelPublish, attempt: Int): End {
        val key = pending.creationKey
        return when (val outcome = runSafely(pending)) {
            is ReelPublishPipeline.Outcome.Published -> {
                // The record as the pipeline left it, not as this run loaded
                // it: the stashed copy's path was written by a checkpoint
                // and would be missed (and leak) if read from `pending`.
                val final = store.load(key) ?: pending
                store.remove(key)
                files.delete(listOf(final.videoPath, final.coverPath))
                tracker.update(key, publishedState(outcome, final.publishAt))
                End.Finished(postId = outcome.postId)
            }
            ReelPublishPipeline.Outcome.Continue -> End.Continue
            ReelPublishPipeline.Outcome.Discarded -> End.Finished()
            is ReelPublishPipeline.Outcome.Failed -> when {
                continuesAfterProgress(outcome) -> notFailedYet(key, End.Continue)
                retriesAutomatically(outcome, attempt) -> notFailedYet(key, End.RetryLater)
                else -> {
                    val failed = ReelPublishState.Failed(outcome.message, outcome.retryable, outcome.needsChannel)
                    tracker.update(key, failed)
                    End.Finished(failure = outcome.message)
                }
            }
        }
    }

    /**
     * The pipeline wrote the failure down; the record must not keep it, or the
     * next run would take it as finished and stop. The tile keeps its loader.
     */
    private suspend fun notFailedYet(key: String, end: End): End {
        store.load(key)?.let { store.save(it.copy(failure = null)) }
        tracker.update(key, ReelPublishState.Preparing)
        return end
    }

    @Suppress("TooGenericExceptionCaught") // The backstop: whatever a step throws, the queue must go on.
    private suspend fun runSafely(pending: PendingReelPublish): ReelPublishPipeline.Outcome {
        val key = pending.creationKey
        return try {
            pipeline(pending) { discards.isDiscarded(key) }
        } catch (e: CancellationException) {
            // WorkManager stopping the run: it reruns it from the checkpoints.
            throw e
        } catch (e: Exception) {
            if (discards.isDiscarded(key)) return ReelPublishPipeline.Outcome.Discarded
            // The type only: a message may carry a path or a URI.
            Log.w(TAG, "Reel publish stopped on ${e.javaClass.simpleName}")
            val failure = PendingReelFailure(UNEXPECTED_FAILURE, retryable = true)
            store.load(key)?.let { store.save(it.copy(failure = failure)) }
            ReelPublishPipeline.Outcome.Failed(UNEXPECTED_FAILURE, retryable = true)
        }
    }

    companion object {
        private const val TAG = "ReelPublishRun"

        /** Automatic tries of a transient failure before the tile asks the user. */
        const val AUTO_RETRIES = 3

        /** What the tile says when a step threw rather than answered. */
        const val UNEXPECTED_FAILURE = "Something went wrong preparing this video. Try again."

        /** Whether [outcome] is retried without the user: the network's, and tries are left. */
        fun retriesAutomatically(outcome: ReelPublishPipeline.Outcome.Failed, attempt: Int): Boolean =
            outcome.transient && attempt < AUTO_RETRIES

        /** Whether [outcome] starts a fresh, uncounted run: the network's, after the upload moved on. */
        fun continuesAfterProgress(outcome: ReelPublishPipeline.Outcome.Failed): Boolean =
            outcome.transient && outcome.progressed
    }
}
