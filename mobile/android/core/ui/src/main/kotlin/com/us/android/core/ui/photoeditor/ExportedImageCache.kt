package com.us.android.core.ui.photoeditor

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import java.io.File
import java.io.InputStream

/** Where edited photos land: a cache subdirectory the app's FileProvider also serves (`photo_edits`). */
const val PHOTO_EDITS_DIR = "photo_edits"

/**
 * Copies an editor's export into `cacheDir/photo_edits/<millis>.jpg` and
 * returns the file's absolute path, or null when the source cannot be read.
 *
 * A source that already is a JPEG is streamed byte for byte — no second
 * lossy encode. Anything else (a PNG export, say) is decoded and written as
 * JPEG at [JPEG_QUALITY], because every consumer of this path declares the
 * result as `image/jpeg`.
 */
fun copyExportToCache(context: Context, source: Uri): String? {
    val dir = File(context.cacheDir, PHOTO_EDITS_DIR).apply { mkdirs() }
    val target = File(dir, "edit_${System.currentTimeMillis()}.jpg")
    val open = { context.contentResolver.openInputStream(source) }
    return runCatching {
        val jpeg = open()?.use(::isJpeg) ?: return null
        if (jpeg) {
            open()?.use { input -> target.outputStream().use { input.copyTo(it) } } ?: return null
        } else {
            val bitmap = open()?.use { BitmapFactory.decodeStream(it) } ?: return null
            target.outputStream().use { bitmap.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, it) }
            bitmap.recycle()
        }
        target.takeIf { it.length() > 0 }?.absolutePath
    }.getOrElse {
        target.delete()
        null
    }
}

/** JPEG starts with the SOI marker FF D8. */
private fun isJpeg(input: InputStream): Boolean {
    val head = ByteArray(2)
    val read = input.read(head)
    return read == 2 && head[0] == SOI_FIRST && head[1] == SOI_SECOND
}

private const val SOI_FIRST = 0xFF.toByte()
private const val SOI_SECOND = 0xD8.toByte()
private const val JPEG_QUALITY = 95
