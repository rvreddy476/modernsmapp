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
    // The editor does not promise a content:// URI. Banuba exports to its own
    // working directory and can hand back a plain file path, and
    // ContentResolver.openInputStream throws on a "file" scheme with no
    // authority / on a bare path — which runCatching turned into a null, which
    // the caller reported as "The edited photo could not be saved". The edit
    // existed; only this copy failed to reach it.
    val open = {
        runCatching { context.contentResolver.openInputStream(source) }.getOrNull()
            ?: sourceFile(source)?.takeIf { it.canRead() }?.inputStream()
    }
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

/**
 * The export as a plain file, when the Uri names one.
 *
 * Covers `file:///…`, and a Uri with no scheme at all — an editor that hands
 * back `/data/user/0/…/export.jpg` parses as a Uri whose path is the whole
 * string and whose scheme is null.
 */
private fun sourceFile(source: Uri): File? = when (source.scheme) {
    null, "file" -> source.path?.let(::File)
    else -> null
}
