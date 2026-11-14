package com.us.android.feature.echo.data

import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Which seen notes go in the next `POST /v1/notes/views` (contract section
 * 2: up to 50 ids). Pure, so the rules are table tests:
 *
 *  - a batch never holds the same id twice, and never more than [max];
 *  - an id already reported this session is not reported again (the server
 *    counts a viewer once per note per 24 hours, so a second report is noise);
 *  - a batch that failed goes back to the front of the queue, once.
 */
class ViewBatcher(private val max: Int = EchoViews.BATCH_MAX) {
    private val reported = HashSet<String>()
    private val pending = LinkedHashSet<String>()
    private val retried = HashSet<String>()

    val hasPending: Boolean get() = pending.isNotEmpty()

    fun seen(ids: Collection<String>) {
        for (id in ids) if (id.isNotBlank() && id !in reported) pending.add(id)
    }

    /** The next batch, removed from the queue and marked reported. Empty when nothing waits. */
    fun next(): List<String> {
        val batch = pending.take(max)
        pending.removeAll(batch.toSet())
        reported.addAll(batch)
        return batch
    }

    /** The server did not take [batch]: each id is queued once more, then dropped. */
    fun failed(batch: List<String>) {
        val again = batch.filter { retried.add(it) }
        reported.removeAll(again.toSet())
        val rest = pending.toList()
        pending.clear()
        pending.addAll(again)
        pending.addAll(rest)
    }
}

/**
 * Collects the notes the viewer actually saw on any Echo list and sends them
 * every [FLUSH_MILLIS] in batches. One per app session, so a note seen on
 * Home and again on its page is reported once.
 */
@Singleton
class EchoViews @Inject constructor(
    private val repository: EchoRepository,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val lock = Mutex()
    private val batcher = ViewBatcher()
    private var flusher: Job? = null

    /** Notes on screen now; only published notes count. */
    fun seen(notes: List<Note>) {
        val ids = notes.filter { it.status == NoteStatus.PUBLISHED }.map { it.id }
        if (ids.isEmpty()) return
        scope.launch {
            lock.withLock { batcher.seen(ids) }
            startFlusher()
        }
    }

    private fun startFlusher() {
        if (flusher?.isActive == true) return
        flusher = scope.launch {
            while (true) {
                delay(FLUSH_MILLIS)
                val batch = lock.withLock { batcher.next() }
                if (batch.isEmpty()) break
                if (repository.recordViews(batch) is AppResult.Failure) lock.withLock { batcher.failed(batch) }
            }
        }
    }

    companion object {
        const val BATCH_MAX = 50
        const val FLUSH_MILLIS = 3_000L
    }
}
