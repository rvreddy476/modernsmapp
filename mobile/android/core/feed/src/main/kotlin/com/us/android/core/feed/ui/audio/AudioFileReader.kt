package com.us.android.core.feed.ui.audio

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import com.us.android.core.feed.data.AudioUploadFile
import dagger.hilt.android.qualifiers.ApplicationContext
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody
import okio.BufferedSink
import okio.source
import java.io.IOException
import javax.inject.Inject

/**
 * Turns the audio file the creator picked into something to upload: its
 * name, its type and its size, and a body that streams the bytes from the
 * picker's URI as they are sent — a 50 MB dub is never held in memory.
 */
class AudioFileReader @Inject constructor(
    @ApplicationContext private val context: Context,
) {

    /** What was picked, or null when the URI cannot be read (it was revoked, or is not a file). */
    fun read(uri: Uri): PickedAudio? {
        val resolver = context.contentResolver
        var name: String? = null
        var size: Long? = null
        runCatching {
            resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)
                ?.use { cursor ->
                    if (cursor.moveToFirst()) {
                        name = cursor.getString(0)
                        size = if (cursor.isNull(1)) null else cursor.getLong(1)
                    }
                }
        }
        val mime = resolver.getType(uri) ?: FALLBACK_MIME
        val fileName = name?.takeIf { it.isNotBlank() } ?: uri.lastPathSegment ?: FALLBACK_NAME
        val length = size?.takeIf { it > 0 } ?: -1L
        val body = object : RequestBody() {
            override fun contentType() = mime.toMediaTypeOrNull()

            override fun contentLength(): Long = length

            override fun writeTo(sink: BufferedSink) {
                val input = resolver.openInputStream(uri) ?: throw IOException("the picked file can't be read")
                input.source().use { sink.writeAll(it) }
            }
        }
        return PickedAudio(file = AudioUploadFile(fileName, mime, body), sizeBytes = size)
    }

    private companion object {
        const val FALLBACK_MIME = "application/octet-stream"
        const val FALLBACK_NAME = "audio"
    }
}

/** A picked file: what is uploaded, and its size when the picker said (for the 50 MB check before sending). */
class PickedAudio(val file: AudioUploadFile, val sizeBytes: Long?)
