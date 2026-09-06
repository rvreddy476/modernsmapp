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
import com.us.android.feature.post.studio.PublishWorker
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
import java.util.Collections
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
 * ## ONE QUEUE, IN ORDER (2026-09-05)
 *
 * Every publish is appended to ONE unique chain ([QUEUE_NAME]), so uploads
 * run one at a time in the order they were started — a second reel begun
 * while the first uploads waits its turn rather than halving its bandwidth.
 * A chain cancels every dependent the moment one link FAILS, which is why
 * this worker never returns failure: a publish that stops is recorded in
 * the store and the tracker, the run returns success, and the next reel in
 * the queue goes ahead. The pending tile offers Retry, which appends a
 * fresh run for the same record.
 *
 * ## WHY IT CHAINS ITSELF
 *
 * Only on the fallback path: a server that still refuses a confirmed video
 * is polled, a run is stopped at ten minutes and a transcode can take
 * thirty, so a run whose budget ends mid-poll returns success and APPENDS
 * another run to the queue. The record's `processingSinceMillis` keeps the
 * 30-minute window honest across those runs.
 */
@HiltWorker
// The worker's collaborators, injected; a wrapper would add indirection, not clarity.
@Suppress("LongParameterList")
class ReelPublishWorker @AssistedInject constructor(
    @Assisted context: Context,
    @Assisted params: WorkerParameters,
    private val pipeline: ReelPublishPipeline,
    private val store: ReelPublishStore,
    private val files: ReelPublishFiles,
    private val tracker: ReelPublishTracker,
    private val discards: ReelPublishDiscards,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val key = inputData.getString(KEY_CREATION_KEY) ?: return Result.success()
        // No record means the user discarded this publish while it queued.
        // Nothing to do — and never a failure, which would cancel the rest
        // of the queue behind it.
        val pending = store.load(key) ?: return Result.success()
        val retry = inputData.getBoolean(KEY_RETRY, false)
        if (pending.failure != null && !retry) return Result.success()

        return when (val outcome = pipeline.run(pending, isDiscarded = { discards.isDiscarded(key) })) {
            is ReelPublishPipeline.Outcome.Published -> {
                // The record as the pipeline left it, not as this run loaded
                // it: the stashed copy's path was written by a checkpoint
                // and would be missed (and leak) if read from `pending`.
                val final = store.load(key) ?: pending
                store.remove(key)
                files.delete(listOf(final.videoPath, final.coverPath))
                tracker.update(key, ReelPublishState.Published(outcome.postId, publishAt = final.publishAt))
                Result.success(workDataOf(KEY_POST_ID to outcome.postId))
            }
            ReelPublishPipeline.Outcome.Continue -> {
                enqueue(applicationContext, key)
                Result.success()
            }
            ReelPublishPipeline.Outcome.Discarded -> Result.success()
            is ReelPublishPipeline.Outcome.Failed -> {
                tracker.update(key, ReelPublishState.Failed(outcome.message, outcome.retryable, outcome.needsChannel))
                Result.success(workDataOf(KEY_FAILURE_REASON to outcome.message))
            }
        }
    }

    companion object {
        const val KEY_CREATION_KEY = "creationKey"
        const val KEY_RETRY = "retry"
        const val KEY_POST_ID = "postId"
        const val KEY_FAILURE_REASON = "reason"

        /** The one chain every publish joins the end of. */
        const val QUEUE_NAME = "reel-publish-queue"

        /**
         * Append a run for [creationKey] to the queue. APPEND_OR_REPLACE:
         * after the current chain when one is still running, at once when
         * the last one finished (the queue never fails, so "replace" is
         * only ever the finished-chain case).
         */
        fun enqueue(context: Context, creationKey: String, retry: Boolean = false) {
            val request = OneTimeWorkRequestBuilder<ReelPublishWorker>()
                .setInputData(workDataOf(KEY_CREATION_KEY to creationKey, KEY_RETRY to retry))
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork(QUEUE_NAME, ExistingWorkPolicy.APPEND_OR_REPLACE, request)
        }
    }
}

/** What the reel form hands over on Post. */
interface ReelPublishLauncher {
    suspend fun enqueue(pending: PendingReelPublish)
}

/**
 * The keys the user discarded while their publish was queued or running —
 * the running pipeline checks here and stops.
 */
@Singleton
class ReelPublishDiscards @Inject constructor() {
    private val keys: MutableSet<String> = Collections.synchronizedSet(mutableSetOf())

    fun discard(creationKey: String) {
        keys += creationKey
    }

    fun isDiscarded(creationKey: String): Boolean = creationKey in keys
}

/**
 * Owns the pending queue: enqueues records, restores them after a restart,
 * and answers the pending tiles' Retry and Discard.
 *
 * Created the first time something injects it — a grid's ViewModel, the
 * Reels tab's, or the reel form — which is early enough: the worker itself
 * only needs the tracker, so a WorkManager restart reports progress whether
 * or not this object exists yet.
 */
@Singleton
class ReelPublishController @Inject constructor(
    @ApplicationContext private val context: Context,
    private val store: ReelPublishStore,
    private val files: ReelPublishFiles,
    private val tracker: ReelPublishTracker,
    private val discards: ReelPublishDiscards,
    @ApplicationScope private val scope: CoroutineScope,
) : ReelPublishLauncher, ReelPublishActions {

    init {
        scope.launch { restore() }
    }

    override suspend fun enqueue(pending: PendingReelPublish) {
        store.save(pending)
        // The preview first, then the state: the grid draws the pending
        // tile the moment the state turns active, and wants the cover then.
        tracker.setPreview(pending.preview())
        tracker.update(pending.creationKey, ReelPublishState.Preparing)
        ReelPublishWorker.enqueue(context, pending.creationKey)
    }

    override fun retry(creationKey: String) {
        scope.launch {
            // No video record means this is a PHOTO publish (2026-09-06). The
            // queue is shared, so its failed tile offers the same Retry, but
            // the work behind it is the studio's own worker — whose creation
            // key IS the project id, and whose pipeline is checkpointed page by
            // page, so re-enqueuing resumes rather than re-uploading.
            val pending = store.load(creationKey) ?: return@launch retryPhoto(creationKey)
            // A fresh readiness window: the retry is a new decision by the
            // user, not the tail of the one that timed out.
            store.save(pending.copy(failure = null, processingSinceMillis = null))
            tracker.setPreview(pending.preview())
            tracker.update(creationKey, ReelPublishState.Preparing)
            ReelPublishWorker.enqueue(context, creationKey, retry = true)
        }
    }

    override fun discard(creationKey: String) {
        discards.discard(creationKey)
        scope.launch {
            val pending = store.load(creationKey)
            if (pending == null) {
                // A photo publish: stop its worker and let the tile go. The
                // project document is deliberately left alone — the studio
                // reopens the most recent editable project, so discarding the
                // upload does not throw away the edit behind it.
                WorkManager.getInstance(context).cancelUniqueWork(PublishWorker.uniqueName(creationKey))
                tracker.reset(creationKey)
                return@launch
            }
            store.remove(creationKey)
            files.delete(listOf(pending.videoPath, pending.coverPath))
            tracker.reset(creationKey)
        }
    }

    private fun retryPhoto(creationKey: String) {
        tracker.update(creationKey, ReelPublishState.Preparing)
        PublishWorker.enqueue(context, creationKey)
    }

    override fun dismiss(creationKey: String) = tracker.dismiss(creationKey)

    /**
     * After a process restart: a failed record shows its failure; a record
     * still in flight either has WorkManager work running (which reports
     * itself) or lost it, in which case it is appended again, in order.
     */
    private suspend fun restore() {
        val records = store.loadAll()
        if (records.isEmpty()) return
        val infos = WorkManager.getInstance(context).getWorkInfosForUniqueWorkFlow(ReelPublishWorker.QUEUE_NAME).first()
        val alive = infos.any { !it.state.isFinished }
        records.forEach { pending ->
            if (tracker.previewOf(pending.creationKey) == null) tracker.setPreview(pending.preview())
            val failure = pending.failure
            if (failure != null) {
                tracker.restoreIfIdle(
                    pending.creationKey,
                    ReelPublishState.Failed(failure.message, failure.retryable, failure.needsChannel),
                )
            } else {
                if (!alive) ReelPublishWorker.enqueue(context, pending.creationKey)
                val resumed =
                    if (pending.confirmedVideoId != null) ReelPublishState.Posting else ReelPublishState.Preparing
                tracker.restoreIfIdle(pending.creationKey, resumed)
            }
        }
    }

    private fun PendingReelPublish.preview() = ReelPublishPreview(
        creationKey = creationKey,
        coverPath = coverPath,
        caption = caption,
        kind = kind,
        title = title,
        publishAt = publishAt,
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
