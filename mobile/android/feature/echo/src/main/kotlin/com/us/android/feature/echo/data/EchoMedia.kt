package com.us.android.feature.echo.data

import android.content.Context
import android.net.Uri
import androidx.core.content.FileProvider
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.media.upload.ChatAttachmentUploader
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import java.io.InputStream
import java.io.OutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Puts a picked photo or GIF on media-service and answers its ready id: the
 * same reserve → PUT → confirm → ready pipeline every attach path uses, with
 * images only. A seam so the composer is tested without Android.
 */
interface EchoMediaUploader {
    /** True when the picked item is a GIF (`image/gif`), which must stand alone on a note. */
    fun isGif(uri: String): Boolean

    suspend fun upload(uri: String): AppResult<String>

    /**
     * Downloads a GIF picked from Giphy into a local file and answers its
     * content:// URI, which then stages like a gallery GIF. The contract
     * stores no outside URLs: the GIF is uploaded through media-service.
     */
    suspend fun fetchGif(url: String): AppResult<String> =
        AppResult.Failure(AppError.InvalidRequest(GIF_FAILED))
}

const val GIF_FAILED = "That GIF couldn't be added."

@Singleton
class DefaultEchoMediaUploader @Inject constructor(
    private val uploader: ChatAttachmentUploader,
    @ApplicationContext private val context: Context,
) : EchoMediaUploader {
    override fun isGif(uri: String): Boolean = uploader.mimeOf(Uri.parse(uri)) == GIF_MIME

    override suspend fun upload(uri: String): AppResult<String> = uploader.uploadImage(Uri.parse(uri))

    override suspend fun fetchGif(url: String): AppResult<String> = withContext(Dispatchers.IO) {
        if (!url.startsWith("https://")) return@withContext AppResult.Failure(AppError.InvalidRequest(GIF_FAILED))
        val dir = File(context.cacheDir, EchoGifFileProvider.DIR).apply { mkdirs() }
        // Earlier picks are uploaded or abandoned by now; keep the folder small.
        dir.listFiles()?.filter { System.currentTimeMillis() - it.lastModified() > STALE_MS }?.forEach { it.delete() }
        val file = File(dir, "${UUID.randomUUID()}.gif")
        val saved = runCatching {
            val connection = (URL(url).openConnection() as HttpURLConnection).apply {
                connectTimeout = TIMEOUT_MS
                readTimeout = TIMEOUT_MS
            }
            try {
                check(connection.responseCode == HttpURLConnection.HTTP_OK)
                connection.inputStream.use { input ->
                    file.outputStream().use { output -> copyCapped(input, output) }
                }
            } finally {
                connection.disconnect()
            }
        }.isSuccess
        if (!saved) {
            file.delete()
            return@withContext AppResult.Failure(AppError.InvalidRequest(GIF_FAILED))
        }
        runCatching { FileProvider.getUriForFile(context, EchoGifFileProvider.authority(context), file).toString() }
            .fold(
                onSuccess = { AppResult.Success(it) },
                onFailure = {
                    file.delete()
                    AppResult.Failure(AppError.InvalidRequest(GIF_FAILED))
                },
            )
    }

    private fun copyCapped(input: InputStream, output: OutputStream) {
        val buffer = ByteArray(BUFFER)
        var total = 0L
        while (true) {
            val read = input.read(buffer)
            if (read < 0) break
            total += read
            check(total <= MAX_GIF_BYTES) { "GIF too large" }
            output.write(buffer, 0, read)
        }
    }

    private companion object {
        const val GIF_MIME = "image/gif"
        const val MAX_GIF_BYTES = 15L * 1024 * 1024
        const val TIMEOUT_MS = 15_000
        const val BUFFER = 64 * 1024
        const val STALE_MS = 24L * 60 * 60 * 1000
    }
}

/**
 * Where each note picture is drawn from. A note carries media ids only, so
 * each is read once from media-service (`GET /v1/media/:id/url`) and kept
 * for the session; every Echo screen reads the same map.
 */
@Singleton
class EchoMediaUrls @Inject constructor(
    private val media: MediaRepository,
) {
    private val _urls = MutableStateFlow<Map<String, String>>(emptyMap())
    val urls: StateFlow<Map<String, String>> = _urls.asStateFlow()

    private val asked = mutableSetOf<String>()
    private val lock = Mutex()

    /** Reads the address of every picture on [notes] (and their quotes) not read yet. */
    suspend fun load(notes: List<Note>) =
        loadIds(notes.flatMap { note -> note.media + note.quoted?.media.orEmpty() }.map { it.mediaId })

    /** Reads the address of each media id not read yet: a profile's avatar and cover too. */
    suspend fun loadIds(ids: List<String>) {
        val fresh = lock.withLock { ids.filter { it.isNotBlank() && asked.add(it) } }
        for (id in fresh) {
            val delivery = media.delivery(id)
            val url = (delivery as? AppResult.Success)?.data?.posterUrl
            if (url != null) {
                _urls.update { it + (id to url) }
            } else {
                // A failed read may be tried again on the next load.
                lock.withLock { asked.remove(id) }
            }
        }
    }
}
