package com.us.android.feature.post.createhub

import android.content.Context
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.UploadSource
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File
import java.io.FileInputStream
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

// ════════════════════════════════════════════════════════════════════════
// The pending publish — everything the worker needs, and nothing the
// process needs to be alive for
// ════════════════════════════════════════════════════════════════════════

/**
 * One reel waiting to be, or being, published in the background.
 *
 * Persisted as JSON in `filesDir` the moment the user taps Post, and updated
 * at every checkpoint the worker reaches: the cached copy of the video, the
 * confirmed upload id, the ready cover id. A process death anywhere in
 * between resumes from the last checkpoint — a video the server already has
 * is never uploaded twice.
 *
 * The form's fields are copied in whole so the create request can be built
 * from this record alone; the ViewModel that held them is long gone by the
 * time the transcode finishes.
 */
@Serializable
data class PendingReelPublish(
    val creationKey: String,
    /** The picked content URI. May not survive process death — see [videoPath]. */
    val videoUri: String,
    /**
     * The app-private copy of the video, once made. This is what gets
     * uploaded. The studio's export (2026-09-05) writes here directly, so a
     * reel that went through it is never copied a second time.
     */
    val videoPath: String? = null,
    val videoMimeType: String? = null,
    /** The chosen cover frame as a JPEG file, or null when no frame could be extracted. */
    val coverPath: String? = null,
    /** Reel (`flick`) or long video (`long_video`) — which surface it lands on. */
    val kind: VideoKind = VideoKind.REEL,
    /** The long video's title; required for [VideoKind.LONG], ignored for a reel. */
    val title: String = "",
    val caption: String = "",
    val visibility: String = VISIBILITY_PUBLIC,
    val category: String = "",
    val allowComments: Boolean = true,
    val hideShare: Boolean = false,
    val allowDownload: Boolean = true,
    val allowRemix: Boolean = true,
    val taggedUserIds: List<String> = emptyList(),
    val locationName: String = "",
    /** The hashtag chips, without `#`, at most thirty (2026-09-05). */
    val hashtags: List<String> = emptyList(),
    /** The mentioned people's usernames, without `@` (2026-09-05). */
    val mentions: List<String> = emptyList(),
    /** RFC 3339 instant the post goes live, or null to post now (2026-09-05). */
    val publishAt: String? = null,
    /**
     * Bytes landed and `confirm` succeeded. This is the id the post is
     * created with — instant reels need nothing more than confirmed.
     */
    val confirmedVideoId: String? = null,
    /**
     * FALLBACK ONLY: when the pre-instant server's `MEDIA_NOT_READY` first
     * sent the pipeline polling — the 30-minute window runs from here.
     */
    val processingSinceMillis: Long? = null,
    /** The cover, EXACTLY ready+passed. Survives a create retry. */
    val readyCoverId: String? = null,
    /** Why the last run stopped, when it did. Cleared by a retry. */
    val failure: PendingReelFailure? = null,
)

@Serializable
data class PendingReelFailure(
    val message: String,
    val retryable: Boolean,
    /** The server wants a channel first (`CHANNEL_REQUIRED`); the tile offers "Create channel". */
    val needsChannel: Boolean = false,
)

// ════════════════════════════════════════════════════════════════════════
// Ports
// ════════════════════════════════════════════════════════════════════════

/**
 * The durable queue (2026-09-05: "the user can start another reel while
 * one uploads"). Records keep the order they were saved in; a save of a
 * known key updates it in place.
 */
interface ReelPublishStore {
    /** Every pending record, oldest first. Empty when nothing is pending. */
    suspend fun loadAll(): List<PendingReelPublish>

    suspend fun load(creationKey: String): PendingReelPublish? = loadAll().firstOrNull { it.creationKey == creationKey }

    suspend fun save(pending: PendingReelPublish)

    suspend fun remove(creationKey: String)
}

/** An app-private copy of the picked video. */
data class StashedVideo(val path: String, val mimeType: String)

/**
 * The files a pending publish owns: the video copy and the cover JPEG.
 *
 * A port so the pipeline can be tested on the JVM with an in-memory fake;
 * the Android implementation lives in `cacheDir/reel_publish`.
 */
interface ReelPublishFiles {
    /** Copy the picked video into app storage. Null when it cannot be read. */
    suspend fun stashVideo(uri: String, creationKey: String): StashedVideo?

    /**
     * Where the studio's export lands for [creationKey] — the same place a
     * stash would, so the worker uploads it without a copy.
     */
    fun exportTarget(creationKey: String): String

    /** Write the chosen cover's JPEG bytes. Returns the path, or null on failure. */
    suspend fun writeCover(bytes: ByteArray, creationKey: String): String?

    /** Open a stashed video for upload. Null when the copy is gone. */
    fun openVideo(path: String, mimeType: String): PickedMedia?

    suspend fun readBytes(path: String): ByteArray?

    suspend fun delete(paths: List<String?>)
}

// ════════════════════════════════════════════════════════════════════════
// Android implementations
// ════════════════════════════════════════════════════════════════════════

/**
 * `filesDir/reel_publish/queue.json`, written whole on every checkpoint.
 * Reads and writes are serialized: two workers checkpointing at once must
 * not lose each other's record.
 */
@Singleton
class FileReelPublishStore @Inject constructor(
    @ApplicationContext private val context: Context,
    private val json: Json,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ReelPublishStore {

    private val lock = Mutex()
    private val serializer = ListSerializer(PendingReelPublish.serializer())

    private val file: File
        get() = File(File(context.filesDir, DIR), FILE)

    override suspend fun loadAll(): List<PendingReelPublish> = withContext(io) { lock.withLock { read() } }

    override suspend fun save(pending: PendingReelPublish) = withContext(io) {
        lock.withLock {
            val current = read()
            val index = current.indexOfFirst { it.creationKey == pending.creationKey }
            val next = if (index < 0) current + pending else current.toMutableList().also { it[index] = pending }
            write(next)
        }
    }

    override suspend fun remove(creationKey: String) = withContext(io) {
        lock.withLock {
            val next = read().filterNot { it.creationKey == creationKey }
            if (next.isEmpty()) file.delete() else write(next)
            Unit
        }
    }

    private fun read(): List<PendingReelPublish> {
        val target = file
        if (!target.exists()) return emptyList()
        // A file this build no longer decodes is not a queue it can finish;
        // treating it as "nothing pending" is the honest fallback.
        return runCatching { json.decodeFromString(serializer, target.readText()) }.getOrDefault(emptyList())
    }

    private fun write(records: List<PendingReelPublish>) {
        val target = file
        target.parentFile?.mkdirs()
        // Write beside, then rename: a crash mid-write must not leave a
        // half-file that the next launch reads as "nothing pending".
        val temp = File(target.parentFile, "$FILE.tmp")
        val text = json.encodeToString(serializer, records)
        temp.writeText(text)
        if (!temp.renameTo(target)) {
            target.writeText(text)
            temp.delete()
        }
    }

    private companion object {
        const val DIR = "reel_publish"
        const val FILE = "queue.json"
    }
}

/**
 * `cacheDir/reel_publish/<key>.video` and `<key>.jpg`.
 *
 * The copy exists because a content URI from the system picker is a grant
 * to THIS process; after a process death the worker's restart cannot open
 * it. The in-app gallery's MediaStore URIs would survive, but copying every
 * pick is the one rule that is always safe. The copy is deleted when the
 * publish completes or is discarded.
 */
@Singleton
class AndroidReelPublishFiles @Inject constructor(
    @ApplicationContext private val context: Context,
    private val sources: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ReelPublishFiles {

    private val dir: File
        get() = File(context.cacheDir, DIR).apply { mkdirs() }

    override suspend fun stashVideo(uri: String, creationKey: String): StashedVideo? = withContext(io) {
        val picked = sources.resolve(uri) ?: return@withContext null
        val target = File(dir, "$creationKey.video")
        val copied = runCatching {
            picked.source.open().use { input -> target.outputStream().use { output -> input.copyTo(output) } }
        }.isSuccess
        if (!copied || target.length() <= 0L) {
            target.delete()
            return@withContext null
        }
        StashedVideo(path = target.absolutePath, mimeType = picked.mimeType)
    }

    override fun exportTarget(creationKey: String): String = File(dir, "$creationKey.video").absolutePath

    override suspend fun writeCover(bytes: ByteArray, creationKey: String): String? = withContext(io) {
        val target = File(dir, "$creationKey.jpg")
        runCatching { target.writeBytes(bytes) }.map { target.absolutePath }.getOrNull()
    }

    override fun openVideo(path: String, mimeType: String): PickedMedia? {
        val file = File(path)
        val size = file.length()
        if (!file.exists() || size <= 0L) return null
        return PickedMedia(
            uri = file.toURI().toString(),
            mimeType = mimeType,
            sizeBytes = size,
            source = UploadSource {
                if (!file.exists()) throw IOException("Stashed video is gone")
                FileInputStream(file)
            },
        )
    }

    override suspend fun readBytes(path: String): ByteArray? = withContext(io) {
        runCatching { File(path).readBytes() }.getOrNull()?.takeIf { it.isNotEmpty() }
    }

    override suspend fun delete(paths: List<String?>) = withContext(io) {
        paths.filterNotNull().forEach { runCatching { File(it).delete() } }
    }

    private companion object {
        const val DIR = "reel_publish"
    }
}
