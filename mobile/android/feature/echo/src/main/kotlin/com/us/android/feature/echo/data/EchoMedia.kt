package com.us.android.feature.echo.data

import android.net.Uri
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.media.upload.ChatAttachmentUploader
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
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
}

@Singleton
class DefaultEchoMediaUploader @Inject constructor(
    private val uploader: ChatAttachmentUploader,
) : EchoMediaUploader {
    override fun isGif(uri: String): Boolean = uploader.mimeOf(Uri.parse(uri)) == GIF_MIME

    override suspend fun upload(uri: String): AppResult<String> = uploader.uploadImage(Uri.parse(uri))

    private companion object {
        const val GIF_MIME = "image/gif"
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
    suspend fun load(notes: List<Note>) {
        val ids = notes.flatMap { note -> note.media + note.quoted?.media.orEmpty() }.map { it.mediaId }
        val fresh = lock.withLock { ids.filter { asked.add(it) } }
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
