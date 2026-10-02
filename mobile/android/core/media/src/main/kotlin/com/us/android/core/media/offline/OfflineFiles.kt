package com.us.android.core.media.offline

import okhttp3.Call
import okhttp3.Request
import java.io.File
import java.io.IOException

/**
 * The small files beside a copy: its poster and its caption tracks.
 *
 * They are a few kilobytes each and are read by the image loader and the
 * subtitle parser as plain files, so they are fetched whole through the
 * app's authenticated client (the `serve` routes are authorized) rather
 * than through the stream cache. Written to a `.part` beside the target and
 * renamed, so a reader never finds half a file.
 */
class OfflineFiles(
    private val root: () -> File,
    private val client: () -> Call.Factory,
) {
    /** [name] inside [folder]; both are single path segments the app made, never server text. */
    fun file(folder: String, name: String): File = File(File(root(), segment(folder)), segment(name))

    /** Fetches [url] into [folder]/[name]. Null on any failure: a copy without a poster is still a copy. */
    fun fetch(url: String, folder: String, name: String): File? {
        val target = file(folder, name)
        val part = File(target.parentFile, target.name + PART_SUFFIX)
        val kept = try {
            target.parentFile?.mkdirs()
            download(url, part) && part.length() > 0L && part.renameTo(target)
        } catch (_: IOException) {
            false
        } catch (_: IllegalArgumentException) {
            // Not a URL at all.
            false
        }
        if (!kept) part.delete()
        return target.takeIf { kept }
    }

    /** True when the server answered 2xx and its body is in [part]. */
    private fun download(url: String, part: File): Boolean =
        client().newCall(Request.Builder().url(url).build()).execute().use { response ->
            if (response.isSuccessful) part.outputStream().use { out -> response.body.byteStream().copyTo(out) }
            response.isSuccessful
        }

    fun remove(folder: String) {
        File(root(), segment(folder)).deleteRecursively()
    }

    fun removeAll() {
        root().deleteRecursively()
    }

    fun usedBytes(): Long = root().walkBottomUp().filter { it.isFile }.sumOf { it.length() }

    /** One path segment: nothing that could climb out of the folder. */
    private fun segment(value: String): String =
        value.map { if (it.isLetterOrDigit() || it in SAFE_PUNCTUATION) it else '_' }
            .joinToString("")
            .trim('.')
            .ifEmpty { "_" }

    private companion object {
        const val PART_SUFFIX = ".part"
        const val SAFE_PUNCTUATION = "-_."
    }
}
