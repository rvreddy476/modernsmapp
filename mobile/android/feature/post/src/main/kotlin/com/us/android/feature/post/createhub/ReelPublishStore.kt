package com.us.android.feature.post.createhub

import android.content.Context
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.feed.data.credits.CreditInputDto
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.ResumableSession
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
import java.io.InputStream
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
    val kind: PublishKind = PublishKind.REEL,
    /** The long video's title; required for [PublishKind.LONG], ignored for a reel. */
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
     * The added sound (original sounds, 2026-09-30): another reel's audio
     * the reel is made with, by id; null when it plays only its own. Every
     * field defaults, so a record written before they existed still loads.
     */
    val audioTrackId: String? = null,
    /** Where in the sound playback starts. 0: there is no offset picker. */
    val audioStartMs: Long = 0L,
    /** The creator's level for the reel's own audio under the sound, 0..1. */
    val originalAudioVolume: Double = 1.0,
    /** The creator's level for the sound, 0..1. */
    val overlayAudioVolume: Double = 1.0,
    /**
     * A long video's credits (2026-10-23): cast & crew in order, genres,
     * year, production house — set only when the form offered the Credits
     * step. Every field defaults, so a record written before credits loads.
     */
    val credits: List<CreditInputDto> = emptyList(),
    val genres: List<String> = emptyList(),
    val releaseYear: Int? = null,
    val productionHouse: String = "",
    /**
     * Members only (paid memberships, 2026-10-23): the level the creator
     * picked, applied with PUT /membership after the create; null for
     * everyone. Defaulted, so a record written before it loads.
     */
    val membersLevelId: String? = null,
    /**
     * The Action button (2026-10-23): the label and the link, sent with the
     * create; both null for no button. Defaulted, so an older record loads.
     */
    val actionLabel: String? = null,
    val actionUrl: String? = null,
    /**
     * Bytes landed and `confirm` succeeded. This is the id the post is
     * created with — instant reels need nothing more than confirmed.
     */
    val confirmedVideoId: String? = null,
    /**
     * A big video's open resumable upload (2026-10-21), saved before its first
     * part goes up and cleared once it is confirmed. A worker stopped mid-way
     * — the ten-minute background limit, a reboot — resumes THIS session and
     * sends only the parts the server does not have yet.
     */
    val resumableSession: ResumableSession? = null,
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

/** What became of copying the picked video into app storage. */
sealed interface StashResult {
    data class Stashed(val video: StashedVideo) : StashResult

    /** The picked file could not be read (gone, or the grant lapsed). */
    data object Unreadable : StashResult

    /**
     * The copy would not fit with [STASH_HEADROOM_BYTES] left over. There is
     * no size limit on a long video (2026-10-21), so the phone's free space
     * is the only bound: [shortBytes] more must be freed to post it.
     */
    data class NoSpace(val shortBytes: Long) : StashResult
}

/** Space that must stay free after the copy: a full phone breaks more than this upload. */
const val STASH_HEADROOM_BYTES: Long = 200L * 1024L * 1024L

/**
 * How many bytes short the device is of copying [sizeBytes] with the
 * headroom kept — zero when it fits. Pure, so the boundary is a table test.
 */
fun stashShortfall(sizeBytes: Long, freeBytes: Long): Long =
    (sizeBytes + STASH_HEADROOM_BYTES - freeBytes).coerceAtLeast(0L)

/**
 * The files a pending publish owns: the video copy and the cover JPEG.
 *
 * A port so the pipeline can be tested on the JVM with an in-memory fake;
 * the Android implementation lives in `cacheDir/reel_publish`.
 */
interface ReelPublishFiles {
    /** Copy the picked video into app storage, when it can be read and there is room. */
    suspend fun stashVideo(uri: String, creationKey: String): StashResult

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
 * `cacheDir/reel_publish/<key>.video` (a studio export) and `<key>.jpg`; a
 * copy of a picked file is `noBackupFilesDir/reel_publish/<key>.video`.
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

    /**
     * Copies of picked files live OUTSIDE the cache (2026-10-21). A long video
     * may now be gigabytes and take hours to upload, and the system clears an
     * app's cache under storage pressure — biggest holders first — which
     * would delete the file mid-upload. The copy is still removed when the
     * publish completes or is discarded.
     */
    private val stashDir: File
        get() = File(context.noBackupFilesDir, DIR).apply { mkdirs() }

    override suspend fun stashVideo(uri: String, creationKey: String): StashResult = withContext(io) {
        val picked = sources.resolve(uri) ?: return@withContext StashResult.Unreadable
        val target = File(stashDir, "$creationKey.video")
        // Checked before a byte is written, against what the copy will take.
        val short = stashShortfall(picked.sizeBytes, stashDir.usableSpace + target.length())
        if (short > 0L) return@withContext StashResult.NoSpace(short)
        val copied = runCatching {
            picked.source.open().use { input ->
                target.outputStream().use { output -> input.copyTo(output, COPY_BUFFER_BYTES) }
            }
        }.isSuccess
        if (!copied || target.length() <= 0L) {
            target.delete()
            return@withContext StashResult.Unreadable
        }
        StashResult.Stashed(StashedVideo(path = target.absolutePath, mimeType = picked.mimeType))
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
            source = StashedFileSource(file),
        )
    }

    /**
     * Re-openable, and seekable: a resumed part-by-part upload of a multi-GB
     * file opens part N at its offset with a seek, not by reading the parts
     * before it.
     */
    private class StashedFileSource(private val file: File) : UploadSource {
        override fun open(): InputStream = openAt(0L)

        override fun openAt(offset: Long): InputStream {
            if (!file.exists()) throw IOException("Stashed video is gone")
            if (offset > file.length()) throw IOException("Offset $offset is past the end of the stashed video")
            return FileInputStream(file).also { stream -> stream.channel.position(offset) }
        }
    }

    override suspend fun readBytes(path: String): ByteArray? = withContext(io) {
        runCatching { File(path).readBytes() }.getOrNull()?.takeIf { it.isNotEmpty() }
    }

    override suspend fun delete(paths: List<String?>) = withContext(io) {
        paths.filterNotNull().forEach { runCatching { File(it).delete() } }
    }

    private companion object {
        const val DIR = "reel_publish"
        const val COPY_BUFFER_BYTES = 256 * 1024
    }
}
