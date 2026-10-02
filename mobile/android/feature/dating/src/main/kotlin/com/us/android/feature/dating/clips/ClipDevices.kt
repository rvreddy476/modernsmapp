package com.us.android.feature.dating.clips

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.media.MediaMetadataRetriever
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import androidx.core.content.ContextCompat
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_AUDIO
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_GENERAL
import com.us.android.core.media.upload.UploadSource
import com.us.android.feature.dating.photos.UploadOutcome
import com.us.android.feature.dating.photos.awaitProcessed
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton

/*
 * The device side of prompt clips (mechanic M15): the microphone, the length
 * of a picked video, and the upload. Each is a port, so the editor's state
 * machine tests on the JVM; none of the Android halves can.
 */

/** What a clip is uploaded from. */
sealed interface ClipSource {
    val kind: ClipKind

    /** A voice answer recorded here: a temp .m4a the editor deletes afterwards. */
    data class Recorded(val file: File) : ClipSource {
        override val kind: ClipKind get() = ClipKind.AUDIO
    }

    /** A video chosen in the system picker: its content:// URI. */
    data class Picked(val uri: String) : ClipSource {
        override val kind: ClipKind get() = ClipKind.VIDEO
    }
}

/** A clip → a media id `PUT /prompts/:promptId/clip` can attach. */
interface ClipUploader {
    suspend fun upload(source: ClipSource, onProgress: (Float) -> Unit): UploadOutcome
}

/** The microphone, one voice answer at a time. */
interface VoiceRecorder {
    /**
     * Starts recording into a fresh temp file and returns it; null when it
     * could not start — no RECORD_AUDIO grant, or the microphone is busy.
     * Nothing is ever recorded without the grant.
     */
    fun start(): File?

    /** Stops. True when the file holds a usable recording; false leaves it for the caller to delete. */
    fun stop(): Boolean

    /** Stops, and deletes whatever was being written. Harmless when idle. */
    fun cancel()
}

/** The length of a picked video, read on the device before any upload. */
fun interface VideoDurationReader {
    /** Milliseconds, or null when it cannot be read. */
    suspend fun durationMs(uri: String): Long?
}

/**
 * Where voice answers live between the microphone and the upload: one
 * directory, one file at a time, emptied before every recording.
 */
class ClipFileStore(private val directory: File) {

    /** Empties the directory and names the next recording. Nothing is written yet. */
    fun next(stamp: Long): File {
        clear()
        directory.mkdirs()
        return File(directory, "voice-$stamp.m4a")
    }

    /** Deletes every recording left behind. Harmless when the directory never existed. */
    fun clear() {
        directory.listFiles()?.forEach { it.delete() }
    }
}

/**
 * A voice answer through MediaRecorder: AAC in an MPEG-4 container (.m4a,
 * `audio/mp4`, on media-service's audio list), mono, speech quality. The
 * recorder also stops itself at 30 s, behind the editor's own countdown.
 */
@Singleton
class AndroidVoiceRecorder @Inject constructor(
    @ApplicationContext private val context: Context,
) : VoiceRecorder {

    private val files = ClipFileStore(File(context.cacheDir, CLIP_DIRECTORY))
    private var recorder: MediaRecorder? = null
    private var file: File? = null

    /** The recorder reached its own 30 s limit and stopped by itself. */
    @Volatile
    private var reachedLimit = false

    @Synchronized
    override fun start(): File? {
        if (recorder != null) return null
        // The editor asks first; this is the second lock on the same door.
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) return null
        val target = files.next(System.currentTimeMillis())
        val created = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else legacyRecorder()
        return try {
            created.apply {
                setAudioSource(MediaRecorder.AudioSource.MIC)
                setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
                setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
                setAudioChannels(1)
                setAudioSamplingRate(SAMPLE_RATE)
                setAudioEncodingBitRate(BIT_RATE)
                setMaxDuration(ClipRules.MAX_CLIP_MS.toInt())
                setOnInfoListener { _, what, _ ->
                    if (what == MediaRecorder.MEDIA_RECORDER_INFO_MAX_DURATION_REACHED) reachedLimit = true
                }
                setOutputFile(target.absolutePath)
                prepare()
                start()
            }
            reachedLimit = false
            recorder = created
            file = target
            target
        } catch (e: java.io.IOException) {
            abandon(created, target)
        } catch (e: RuntimeException) {
            // The microphone is held by a call or another app, or the recorder refused its setup.
            abandon(created, target)
        }
    }

    @Synchronized
    override fun stop(): Boolean {
        val active = recorder ?: return false
        val target = file
        recorder = null
        file = null
        // stop() throws when nothing was captured; such a file is useless. A
        // recorder that hit its own limit has already finished the file.
        val stopped = runCatching { active.stop() }.isSuccess || reachedLimit
        active.release()
        return stopped && target != null && target.isFile && target.length() > 0L
    }

    @Synchronized
    override fun cancel() {
        val active = recorder
        recorder = null
        if (active != null) {
            runCatching { active.stop() }
            active.release()
        }
        file?.delete()
        file = null
        files.clear()
    }

    private fun abandon(created: MediaRecorder, target: File): File? {
        created.release()
        target.delete()
        return null
    }

    @Suppress("DEPRECATION")
    private fun legacyRecorder(): MediaRecorder = MediaRecorder()

    private companion object {
        const val CLIP_DIRECTORY = "dating-clips"
        const val SAMPLE_RATE = 44_100
        const val BIT_RATE = 64_000
    }
}

/** A picked video's length from its metadata. */
class AndroidVideoDurationReader @Inject constructor(
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : VideoDurationReader {

    override suspend fun durationMs(uri: String): Long? = withContext(io) {
        val retriever = MediaMetadataRetriever()
        try {
            retriever.setDataSource(context, Uri.parse(uri))
            retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()?.takeIf { it > 0L }
        } catch (e: RuntimeException) {
            // A revoked grant, an unreadable file, or a format the retriever does not know.
            null
        } finally {
            runCatching { retriever.release() }
        }
    }
}

/**
 * A clip through `:core:media`'s [MediaUploader], exactly as a dating photo
 * goes: reserve (`file_type` audio or video, NO upload lease — dating-service
 * references the media id itself) → presigned PUT → confirm → a short wait for
 * processing. dating-service then checks ownership and length itself.
 */
class MediaClipUploader @Inject constructor(
    private val uploader: MediaUploader,
    private val resolver: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : ClipUploader {

    override suspend fun upload(source: ClipSource, onProgress: (Float) -> Unit): UploadOutcome = withContext(io) {
        val prepared = when (source) {
            is ClipSource.Recorded -> {
                val size = source.file.length()
                if (!source.file.isFile || size <= 0L) return@withContext UploadOutcome.Failed(ClipCopy.RECORD_FAILED)
                Prepared(MIME_M4A, size, UploadSource { source.file.inputStream() }, FILE_TYPE_AUDIO)
            }
            is ClipSource.Picked -> {
                val picked = resolver.resolve(source.uri) ?: return@withContext UploadOutcome.Failed(ClipCopy.VIDEO_UNREADABLE)
                val mime = picked.mimeType.substringBefore(';').trim().lowercase()
                if (mime !in VIDEO_MIME) return@withContext UploadOutcome.Failed(VIDEO_FORMAT)
                if (picked.sizeBytes > MAX_VIDEO_BYTES) return@withContext UploadOutcome.Failed(VIDEO_TOO_BIG)
                Prepared(mime, picked.sizeBytes, picked.source, FILE_TYPE_VIDEO)
            }
        }
        val reserved = when (
            val result = uploader.reserve(
                mimeType = prepared.mime,
                sizeBytes = prepared.size,
                mediaSubtype = SUBTYPE_GENERAL,
                uploadPurpose = NO_LEASE,
                fileType = prepared.fileType,
            )
        ) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        }
        val put = uploader.upload(reserved.uploadUrl, prepared.mime, prepared.size, prepared.source) { sent, total ->
            if (total > 0) onProgress(sent.toFloat() / total)
        }
        when (put) {
            PresignedPutResult.Success -> Unit
            PresignedPutResult.UrlExpired -> return@withContext UploadOutcome.Failed("The upload link expired. Try again.")
            is PresignedPutResult.Failed -> return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        }
        if (uploader.confirm(reserved.mediaId) !is AppResult.Success) return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        if (awaitProcessed(uploader, reserved.mediaId, POLLS) != null) return@withContext UploadOutcome.Failed(REFUSED)
        UploadOutcome.Ready(reserved.mediaId)
    }

    private class Prepared(val mime: String, val size: Long, val source: UploadSource, val fileType: String)

    private companion object {
        const val NO_LEASE = ""
        const val MIME_M4A = "audio/mp4"
        const val POLLS = 20

        /** media-service's video list (`allowedVideoMIME`). */
        val VIDEO_MIME = setOf("video/mp4", "video/quicktime", "video/webm", "video/x-matroska", "video/x-msvideo")

        /** Far above any 30-second phone video; it only stops a mistaken pick before bytes move. */
        const val MAX_VIDEO_BYTES = 200L * 1024L * 1024L
        const val GENERIC_FAILURE = "The clip didn't upload. Check your connection and try again."
        const val REFUSED = "That clip can't be used. Try another."
        const val VIDEO_FORMAT = "Choose an MP4, MOV or WebM video."
        const val VIDEO_TOO_BIG = "That video is too large. Choose a shorter one."
    }
}
