package com.us.android.feature.post.studio

import android.content.Context
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.creator.model.PostDetails
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton

/**
 * A photo post handed to [PublishWorker] and not yet published (2026-10-19).
 *
 * The details are kept HERE because the publisher writes them to disk only
 * when it freezes the create request, after every photo is uploaded. A
 * failure during the upload therefore left them only in the work's input,
 * which nothing can read back: after a restart the failed tile was gone, and
 * its Retry re-enqueued the post with empty details — no title, the default
 * audience. [failure] is set once the worker gives up, so the tile and its
 * Retry come back after a restart.
 */
@Serializable
data class PendingPhotoPublish(
    /** The studio project; also the publish queue's creation key. */
    val projectId: String,
    val details: PostDetails = PostDetails(),
    val failure: PendingPhotoFailure? = null,
)

@Serializable
data class PendingPhotoFailure(val message: String, val retryable: Boolean)

/**
 * `filesDir/photo_publish/queue.json`, written whole on every change and
 * serialized, the same shape as the reel queue's file.
 */
@Singleton
class PhotoPublishRecords @Inject constructor(
    @ApplicationContext private val context: Context,
    private val json: Json,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {
    private val lock = Mutex()
    private val serializer = ListSerializer(PendingPhotoPublish.serializer())

    private val file: File
        get() = File(File(context.filesDir, DIR), FILE)

    suspend fun loadAll(): List<PendingPhotoPublish> = withContext(io) { lock.withLock { read() } }

    suspend fun load(projectId: String): PendingPhotoPublish? = loadAll().firstOrNull { it.projectId == projectId }

    suspend fun save(record: PendingPhotoPublish) = withContext(io) {
        lock.withLock {
            val current = read()
            val index = current.indexOfFirst { it.projectId == record.projectId }
            write(if (index < 0) current + record else current.toMutableList().also { it[index] = record })
        }
    }

    /** Marks [projectId] failed, keeping its details; a project with no record gets one. */
    suspend fun fail(projectId: String, failure: PendingPhotoFailure, details: PostDetails) = withContext(io) {
        lock.withLock {
            val current = read()
            val index = current.indexOfFirst { it.projectId == projectId }
            val next = if (index < 0) {
                current + PendingPhotoPublish(projectId, details, failure)
            } else {
                current.toMutableList().also { it[index] = it[index].copy(failure = failure) }
            }
            write(next)
        }
    }

    suspend fun remove(projectId: String) = withContext(io) {
        lock.withLock {
            val next = read().filterNot { it.projectId == projectId }
            if (next.isEmpty()) file.delete() else write(next)
            Unit
        }
    }

    private fun read(): List<PendingPhotoPublish> {
        val target = file
        if (!target.exists()) return emptyList()
        return runCatching { json.decodeFromString(serializer, target.readText()) }.getOrDefault(emptyList())
    }

    private fun write(records: List<PendingPhotoPublish>) {
        val target = file
        target.parentFile?.mkdirs()
        // Beside, then rename: a crash mid-write must not read as "nothing pending".
        val temp = File(target.parentFile, "$FILE.tmp")
        val text = json.encodeToString(serializer, records)
        temp.writeText(text)
        if (!temp.renameTo(target)) {
            target.writeText(text)
            temp.delete()
        }
    }

    private companion object {
        const val DIR = "photo_publish"
        const val FILE = "queue.json"
    }
}
