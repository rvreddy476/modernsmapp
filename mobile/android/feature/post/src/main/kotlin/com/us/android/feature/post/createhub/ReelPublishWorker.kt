package com.us.android.feature.post.createhub

import android.content.Context
import androidx.hilt.work.HiltWorker
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishPreview
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import dagger.Binds
import dagger.Module
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Background continuation for a reel publish — the Studio's [PublishWorker]
 * shape over [ReelPublishPipeline].
 *
 * ## WHY THIS IS SAFE TO RESTART
 *
 * WorkManager restarts the work after process death or reboot, and every
 * step underneath is checkpointed in [ReelPublishStore]: a confirmed video
 * id is polled, not re-uploaded; a ready cover id is reused; the creation
 * key makes a repeated create idempotent server-side. The worker adds
 * scheduling, not semantics.
 *
 * ## WHY IT CHAINS ITSELF
 *
 * Only on the fallback path: a server that still refuses a confirmed video
 * is polled, a run is stopped at ten minutes and a transcode can take
 * thirty, so a run whose budget ends mid-poll returns success and APPENDS
 * another run under the same unique name. The record's
 * `processingSinceMillis` keeps the 30-minute window honest across those runs.
 *
 * A failure is terminal for the chain — the pending reel item offers Retry,
 * which enqueues a fresh chain over the same record — rather than
 * WorkManager's silent backoff, so "Couldn't post" is shown the moment it is
 * known and the user decides.
 */
@HiltWorker
class ReelPublishWorker @AssistedInject constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val pipeline: ReelPublishPipeline,
    private val store: ReelPublishStore,
    private val files: ReelPublishFiles,
    private val tracker: ReelPublishTracker,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val key = inputData.getString(KEY_CREATION_KEY) ?: return Result.failure()
        // A record for a different key means this work is stale — the user
        // discarded that publish and started another. Nothing to do.
        val pending = store.load()?.takeIf { it.creationKey == key } ?: return Result.failure()

        return when (val outcome = pipeline.run(pending)) {
            is ReelPublishPipeline.Outcome.Published -> {
                // The record as the pipeline left it, not as this run loaded
                // it: the stashed copy's path was written by a checkpoint
                // and would be missed (and leak) if read from `pending`.
                val final = store.load() ?: pending
                store.clear()
                files.delete(listOf(final.videoPath, final.coverPath))
                tracker.update(ReelPublishState.Published(outcome.postId))
                Result.success(workDataOf(KEY_POST_ID to outcome.postId))
            }
            ReelPublishPipeline.Outcome.Continue -> {
                enqueue(applicationContext, key, ExistingWorkPolicy.APPEND_OR_REPLACE)
                Result.success()
            }
            is ReelPublishPipeline.Outcome.Failed -> {
                tracker.update(ReelPublishState.Failed(outcome.message, outcome.retryable))
                Result.failure(workDataOf(KEY_FAILURE_REASON to outcome.message))
            }
        }
    }

    companion object {
        const val KEY_CREATION_KEY = "creationKey"
        const val KEY_POST_ID = "postId"
        const val KEY_FAILURE_REASON = "reason"

        fun uniqueName(creationKey: String) = "reel-publish-$creationKey"

        /**
         * KEEP for a fresh publish: a second tap while one is queued must not
         * restart it. APPEND for a continuation: run after the current one.
         */
        fun enqueue(context: Context, creationKey: String, policy: ExistingWorkPolicy) {
            val request = OneTimeWorkRequestBuilder<ReelPublishWorker>()
                .setInputData(workDataOf(KEY_CREATION_KEY to creationKey))
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build()
            WorkManager.getInstance(context).enqueueUniqueWork(uniqueName(creationKey), policy, request)
        }
    }
}

/** What the reel form hands over on Post. */
interface ReelPublishLauncher {
    /** True while a publish is in flight — the form refuses a second one. */
    val isBusy: Boolean

    suspend fun enqueue(pending: PendingReelPublish)
}

/**
 * Owns the ONE pending publish: enqueues it, restores it after a restart,
 * and answers the pending reel item's Retry and Discard.
 *
 * Created the first time something injects it — the Reels tab's ViewModel,
 * or the reel form — which is early enough: the worker itself only needs the
 * tracker, so a WorkManager restart reports progress whether or not this
 * object exists yet.
 */
@Singleton
class ReelPublishController @Inject constructor(
    @ApplicationContext private val context: Context,
    private val store: ReelPublishStore,
    private val files: ReelPublishFiles,
    private val tracker: ReelPublishTracker,
    @ApplicationScope private val scope: CoroutineScope,
) : ReelPublishLauncher, ReelPublishActions {

    override val isBusy: Boolean
        get() = tracker.isActive

    init {
        scope.launch { restore() }
    }

    override suspend fun enqueue(pending: PendingReelPublish) {
        // A new reel replaces a finished (failed or dismissed) one: its
        // cached copy and cover would otherwise sit in cacheDir for good.
        store.load()?.takeIf { it.creationKey != pending.creationKey }?.let { previous ->
            files.delete(listOf(previous.videoPath, previous.coverPath))
        }
        store.save(pending)
        // The preview first, then the state: the Reels tab draws the pending
        // item the moment the state turns active, and wants the cover then.
        tracker.setPreview(pending.preview())
        tracker.update(ReelPublishState.Preparing)
        ReelPublishWorker.enqueue(context, pending.creationKey, ExistingWorkPolicy.KEEP)
    }

    override fun retry() {
        scope.launch {
            val pending = store.load() ?: return@launch
            // A fresh readiness window: the retry is a new decision by the
            // user, not the tail of the one that timed out.
            store.save(pending.copy(failure = null, processingSinceMillis = null))
            tracker.setPreview(pending.preview())
            tracker.update(ReelPublishState.Preparing)
            ReelPublishWorker.enqueue(context, pending.creationKey, ExistingWorkPolicy.KEEP)
        }
    }

    override fun discard() {
        scope.launch {
            val pending = store.load()
            if (pending != null) {
                WorkManager.getInstance(context).cancelUniqueWork(ReelPublishWorker.uniqueName(pending.creationKey))
                files.delete(listOf(pending.videoPath, pending.coverPath))
            }
            store.clear()
            tracker.reset()
        }
    }

    override fun dismiss() = tracker.dismiss()

    /**
     * After a process restart: a failed record shows its failure; a record
     * still in flight either has WorkManager work running (which reports
     * itself) or lost it, in which case it is enqueued again.
     */
    private suspend fun restore() {
        val pending = store.load() ?: return
        if (tracker.preview.value == null) tracker.setPreview(pending.preview())
        val failure = pending.failure
        if (failure != null) {
            tracker.restoreIfIdle(ReelPublishState.Failed(failure.message, failure.retryable))
            return
        }
        val name = ReelPublishWorker.uniqueName(pending.creationKey)
        val infos = WorkManager.getInstance(context).getWorkInfosForUniqueWorkFlow(name).first()
        val alive = infos.any { !it.state.isFinished }
        if (!alive) {
            ReelPublishWorker.enqueue(context, pending.creationKey, ExistingWorkPolicy.KEEP)
        }
        val resumed = if (pending.confirmedVideoId != null) ReelPublishState.Posting else ReelPublishState.Preparing
        tracker.restoreIfIdle(resumed)
    }

    private fun PendingReelPublish.preview() = ReelPublishPreview(
        creationKey = creationKey,
        coverPath = coverPath,
        caption = caption,
        kind = kind,
        title = title,
    )
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ReelPublishModule {
    @Binds
    @Singleton
    abstract fun bindStore(implementation: FileReelPublishStore): ReelPublishStore

    @Binds
    @Singleton
    abstract fun bindFiles(implementation: AndroidReelPublishFiles): ReelPublishFiles

    @Binds
    @Singleton
    abstract fun bindLauncher(implementation: ReelPublishController): ReelPublishLauncher

    @Binds
    @Singleton
    abstract fun bindActions(implementation: ReelPublishController): ReelPublishActions
}
