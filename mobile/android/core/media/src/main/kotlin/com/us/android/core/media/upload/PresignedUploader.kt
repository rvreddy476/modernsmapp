package com.us.android.core.media.upload

import com.us.android.core.network.di.BareClient
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okio.BufferedSink
import okio.source
import java.io.IOException
import java.io.InputStream
import javax.inject.Inject
import javax.inject.Singleton

/** Where the bytes come from. Opened fresh per attempt so a retry can re-read. */
fun interface UploadSource {
    /**
     * Opens a NEW stream over the same content.
     *
     * A function rather than a stream because an upload may be retried, and a
     * consumed `InputStream` cannot be rewound. On Android the content URI from
     * the Photo Picker is re-openable, which is what makes retry possible at
     * all.
     */
    @Throws(IOException::class)
    fun open(): InputStream
}

/** Outcome of pushing bytes to the object store. */
sealed interface PresignedPutResult {
    data object Success : PresignedPutResult

    /**
     * The presigned URL is no longer valid.
     *
     * Distinct from [Failed] because the response is different: an expired URL
     * can never succeed on retry, so the caller must run a NEW `init` rather
     * than repeat the PUT.
     */
    data object UrlExpired : PresignedPutResult

    data class Failed(val code: Int, val message: String) : PresignedPutResult
}

/**
 * Pushes bytes to a presigned object-store URL.
 *
 * ## WHY THE BARE CLIENT, AND WHY THAT MATTERS
 *
 * This request must carry NO `Authorization`, no cookie, no CSRF token and no
 * app identity headers. A presigned URL authenticates through its own query
 * signature; S3 and compatible stores reject a request that ALSO carries an
 * `Authorization` header, because two credentials are ambiguous. So a stray
 * auth interceptor here does not degrade the upload — it breaks it outright,
 * and the failure looks like a signing bug rather than a client one.
 *
 * The bare client also has a deliberately long write timeout: this is the one
 * request in the app that pushes megabytes over a phone uplink.
 *
 * Sending our bearer token to a third-party object-store host would be a
 * credential leak as well as a bug, which is why the isolation is asserted by a
 * test rather than left to the DI graph being wired correctly.
 */
@Singleton
open class PresignedUploader @Inject constructor(
    @BareClient private val client: OkHttpClient,
) {

    /**
     * PUTs [source] to [url], reporting byte progress.
     *
     * [onProgress] receives monotonically increasing byte counts. It is called
     * from the upload thread; the caller is responsible for hopping to whatever
     * dispatcher its state lives on.
     */
    open suspend fun put(
        url: String,
        mimeType: String,
        sizeBytes: Long,
        source: UploadSource,
        onProgress: (uploaded: Long, total: Long) -> Unit,
    ): PresignedPutResult {
        val body = ProgressRequestBody(mimeType, sizeBytes, source, onProgress)
        val request = Request.Builder().url(url).put(body).build()

        return try {
            client.newCall(request).execute().use { response ->
                when {
                    response.isSuccessful -> PresignedPutResult.Success
                    // 403 from an object store on a presigned PUT is
                    // overwhelmingly an expired or malformed signature rather
                    // than a permission decision about the user.
                    response.code == HTTP_FORBIDDEN -> PresignedPutResult.UrlExpired
                    else -> PresignedPutResult.Failed(response.code, response.message)
                }
            }
        } catch (e: IOException) {
            PresignedPutResult.Failed(0, e.message.orEmpty())
        }
    }

    private companion object {
        const val HTTP_FORBIDDEN = 403
    }
}

/**
 * A streaming request body that reports progress as it writes.
 *
 * Streaming rather than a byte array: a 20 MiB image loaded whole is 20 MiB of
 * heap on a device that may not have it, and `contentLength` is known up front
 * anyway, so there is nothing to gain from buffering.
 */
internal class ProgressRequestBody(
    private val mimeType: String,
    private val sizeBytes: Long,
    private val source: UploadSource,
    private val onProgress: (Long, Long) -> Unit,
) : RequestBody() {

    override fun contentType() = mimeType.toMediaType()

    override fun contentLength(): Long = sizeBytes

    override fun writeTo(sink: BufferedSink) {
        // Opened here, not in the constructor: OkHttp may call writeTo more
        // than once (a redirect or an auth challenge retry), and a stream that
        // was already drained would silently upload zero bytes the second time.
        source.open().use { input -> streamTo(sink, input) }
    }

    private fun streamTo(sink: BufferedSink, input: InputStream) {
        input.source().use { okioSource ->
            var uploaded = 0L
            // Report 0 first so a UI showing progress starts at a known point
            // rather than jumping in at the first chunk.
            onProgress(0L, sizeBytes)
            while (true) {
                val read = okioSource.read(sink.buffer, SEGMENT_BYTES)
                if (read == -1L) break
                uploaded += read
                sink.flush()
                onProgress(uploaded, sizeBytes)
            }
        }
    }

    private companion object {
        /** One okio segment. Small enough for smooth progress, large enough to be cheap. */
        const val SEGMENT_BYTES = 8L * 1024L
    }
}
