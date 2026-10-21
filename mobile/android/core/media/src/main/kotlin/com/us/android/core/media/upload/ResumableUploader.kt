package com.us.android.core.media.upload

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.delay
import kotlinx.serialization.Serializable
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okio.BufferedSink
import okio.source
import java.io.IOException
import java.time.OffsetDateTime
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Videos at or above this size go up part by part, never as one PUT
 * (founder, 2026-10-21: long videos have no size limit). Below it the
 * single presigned PUT stays: one request, nothing to resume, and the
 * server takes it straight to the object store. A smaller file is switched
 * over too when the simple `init` answers 413 [CODE_USE_RESUMABLE_UPLOAD].
 */
const val RESUMABLE_THRESHOLD_BYTES: Long = 100L * 1024L * 1024L

/** Whether a refused simple `init` is the server asking for the resumable path. */
fun AppError.asksForResumableUpload(): Boolean =
    this is AppError.Unknown && (code == CODE_USE_RESUMABLE_UPLOAD || statusCode == HTTP_PAYLOAD_TOO_LARGE)

private const val HTTP_PAYLOAD_TOO_LARGE = 413

/**
 * An open resumable upload, as the caller persists it.
 *
 * Saved the moment `init` answers, so a worker that is stopped — the
 * ten-minute background limit, a reboot, a process death — resumes the SAME
 * upload: the server lists the parts it already has and only the rest are
 * sent. Losing this would restart a multi-gigabyte upload from byte zero.
 */
@Serializable
data class ResumableSession(
    val uploadId: String,
    val mediaId: String,
    val chunkSize: Long,
    val totalParts: Int,
    val totalBytes: Long,
    /** RFC 3339 as the server sent it; empty when it said nothing. */
    val expiresAt: String = "",
)

sealed interface ResumableOutcome {
    /** Every part landed and `complete` confirmed the asset (processing is queued). */
    data class Completed(val mediaId: String) : ResumableOutcome

    /**
     * The upload stopped. [discardSession] means the stored session can never
     * finish (refused, expired, or a plan that does not fit the file) and the
     * caller must forget it, so the next attempt opens a fresh one; otherwise
     * the next attempt resumes it.
     */
    data class Failed(
        val message: String,
        val retryable: Boolean,
        val discardSession: Boolean,
    ) : ResumableOutcome
}

/** media-service's four resumable calls, as a port, so the uploader is tested against a fake. */
interface ResumableUploadTransport {
    suspend fun init(request: ResumableInitRequest): AppResult<ResumableInitDto>

    suspend fun uploadPart(uploadId: String, partNumber: Int, body: RequestBody): AppResult<ResumableChunkDto>

    suspend fun complete(uploadId: String): AppResult<MediaAssetDto>

    suspend fun status(uploadId: String): AppResult<ResumableStatusDto>
}

@Singleton
class RetrofitResumableTransport @Inject constructor(
    private val api: ResumableUploadApi,
    private val errorMapper: ErrorMapper,
) : ResumableUploadTransport {
    override suspend fun init(request: ResumableInitRequest): AppResult<ResumableInitDto> =
        apiCall(errorMapper) { api.init(request) }

    override suspend fun uploadPart(
        uploadId: String,
        partNumber: Int,
        body: RequestBody,
    ): AppResult<ResumableChunkDto> = apiCall(errorMapper) { api.uploadPart(uploadId, partNumber, body) }

    override suspend fun complete(uploadId: String): AppResult<MediaAssetDto> =
        apiCall(errorMapper) { api.complete(uploadId) }

    override suspend fun status(uploadId: String): AppResult<ResumableStatusDto> =
        apiCall(errorMapper) { api.status(uploadId) }
}

/**
 * Uploads a big file part by part through media-service's resumable routes.
 *
 * ## STREAMED, NEVER LOADED
 *
 * Each part is a request body that opens the source AT the part's offset
 * ([UploadSource.openAt]) and copies exactly the part's length through an
 * okio segment buffer. Nothing larger than one segment is ever in memory,
 * whatever the server's `chunk_size` (5 MiB today, up to 64 MiB) or the
 * file's size. The part plan is the SERVER's: part N starts at
 * `(N - 1) × chunk_size`, and the last part is the remainder.
 *
 * ## RETRIED PER PART, RESUMED PER SESSION
 *
 * A part that fails on the transport (no network, timeout, 5xx, 429) is
 * re-sent with exponential backoff, up to [maxAttempts] tries; a part that
 * is refused (4xx) is not retried, because sending the same bytes again
 * gets the same answer. When the retries run out the outcome keeps the
 * session, so the next run asks the server which parts it already has and
 * sends only the rest. A resumed session that turns out to be dead
 * (expired, unknown, refused) is replaced by ONE fresh `init` in the same
 * call before giving up.
 *
 * ## PROGRESS
 *
 * Bytes, monotonic: a retried part does not move the bar backwards. The
 * caller's progress callback may throw an `IOException` to abort (the reel
 * pipeline does so on discard); that exception is rethrown as it was
 * thrown, never retried and never turned into a failure.
 */
@Singleton
class ResumableUploader internal constructor(
    private val transport: ResumableUploadTransport,
    private val maxAttempts: Int,
    private val backoffMillis: (attempt: Int) -> Long,
    private val now: () -> Long,
) {

    @Inject
    constructor(transport: ResumableUploadTransport) :
        this(transport, MAX_ATTEMPTS, { attempt -> defaultBackoffMillis(attempt) }, System::currentTimeMillis)

    /**
     * Upload [totalBytes] of [source] as one asset. [resume] is a session a
     * previous run persisted, or null. [onSession] is called with every
     * session this call opens — persist it before the first part goes up.
     */
    @Suppress("LongParameterList")
    suspend fun upload(
        mimeType: String,
        totalBytes: Long,
        source: UploadSource,
        resume: ResumableSession?,
        onSession: suspend (ResumableSession) -> Unit,
        onProgress: (uploaded: Long, total: Long) -> Unit,
        fileType: String = FILE_TYPE_VIDEO,
    ): ResumableOutcome {
        require(totalBytes > 0L) { "nothing to upload" }
        val progress = ProgressGate(totalBytes, onProgress)
        val resumable = resume?.takeIf { it.totalBytes == totalBytes && !it.isExpired(now()) }
        if (resumable != null) {
            val resumed = runSession(resumable, source, progress, resumed = true)
            if (resumed !is ResumableOutcome.Failed || !resumed.discardSession) return resumed
        }
        val opened = when (val result = open(mimeType, totalBytes, fileType, progress)) {
            is Step.Done -> result.value
            is Step.Stopped -> return result.outcome
        }
        onSession(opened)
        return runSession(opened, source, progress, resumed = false)
    }

    // ── One session ─────────────────────────────────────────────────────

    private suspend fun open(
        mimeType: String,
        totalBytes: Long,
        fileType: String,
        progress: ProgressGate,
    ): Step<ResumableSession> {
        val request = ResumableInitRequest(fileType = fileType, mimeType = mimeType, totalBytes = totalBytes)
        val init = when (val result = withRetries(progress) { transport.init(request) }) {
            is Attempt.Ok -> result.value
            // The server refused THIS file; asking again gets the same answer.
            is Attempt.Refused -> return Step.Stopped(
                ResumableOutcome.Failed(
                    message = START_REFUSED,
                    retryable = !result.error.isFinalRefusal(),
                    discardSession = true,
                ),
            )
            is Attempt.Exhausted -> return Step.Stopped(failed(START_FAILED, discard = true))
        }
        val session = ResumableSession(
            uploadId = init.uploadId,
            mediaId = init.mediaId,
            chunkSize = init.chunkSize,
            totalParts = init.totalParts,
            totalBytes = totalBytes,
            expiresAt = init.expiresAt,
        )
        if (!session.planFits()) {
            return Step.Stopped(ResumableOutcome.Failed(PLAN_MISMATCH, retryable = false, discardSession = true))
        }
        return Step.Done(session)
    }

    private suspend fun runSession(
        session: ResumableSession,
        source: UploadSource,
        progress: ProgressGate,
        resumed: Boolean,
    ): ResumableOutcome {
        if (!session.planFits()) return ResumableOutcome.Failed(PLAN_MISMATCH, false, discardSession = true)
        val landed = if (resumed) {
            when (val known = landedParts(session, progress)) {
                is Step.Done -> known.value
                is Step.Stopped -> return known.outcome
            }
        } else {
            emptySet()
        }
        var confirmed = landed.sumOf { session.partLength(it) }
        progress.report(confirmed)
        for (part in 1..session.totalParts) {
            if (part in landed) continue
            val offset = session.partOffset(part)
            val length = session.partLength(part)
            val sent = withRetries(progress) {
                val body = PartRequestBody(source, offset, length) { inPart -> progress.report(confirmed + inPart) }
                transport.uploadPart(session.uploadId, part, body)
            }
            when (sent) {
                is Attempt.Ok -> Unit
                // A refusal is the session's, not the network's: forget it.
                is Attempt.Refused -> return failed(PART_FAILED, discard = true)
                is Attempt.Exhausted -> return failed(PART_FAILED, discard = false)
            }
            confirmed += length
            progress.report(confirmed)
        }
        return finish(session, progress)
    }

    /**
     * Which parts the server already holds. A session it no longer knows
     * — or one it says has expired — is discarded; a COMPLETED one needs
     * nothing but the (idempotent) complete.
     */
    private suspend fun landedParts(session: ResumableSession, progress: ProgressGate): Step<Set<Int>> =
        when (val result = withRetries(progress) { transport.status(session.uploadId) }) {
            is Attempt.Ok -> when (result.value.status) {
                RESUMABLE_STATUS_EXPIRED ->
                    Step.Stopped(ResumableOutcome.Failed(EXPIRED, retryable = true, discardSession = true))
                RESUMABLE_STATUS_COMPLETED -> Step.Done((1..session.totalParts).toSet())
                else -> Step.Done(result.value.uploadedParts.filter { it in 1..session.totalParts }.toSet())
            }
            is Attempt.Refused -> Step.Stopped(failed(RESUME_FAILED, discard = true))
            is Attempt.Exhausted -> Step.Stopped(failed(RESUME_FAILED, discard = false))
        }

    /**
     * `complete` — retried on the transport like a part. A refusal keeps the
     * session: the server says what is missing on the next status, and the
     * next run sends just that.
     */
    private suspend fun finish(session: ResumableSession, progress: ProgressGate): ResumableOutcome =
        when (val result = withRetries(progress) { transport.complete(session.uploadId) }) {
            is Attempt.Ok -> ResumableOutcome.Completed(result.value.id.ifBlank { session.mediaId })
            is Attempt.Refused -> failed(COMPLETE_FAILED, discard = false)
            is Attempt.Exhausted -> failed(COMPLETE_FAILED, discard = false)
        }

    // ── Retry ───────────────────────────────────────────────────────────

    private sealed interface Attempt<out T> {
        data class Ok<T>(val value: T) : Attempt<T>

        /** Not worth repeating: the server refused it. */
        data class Refused(val error: AppError) : Attempt<Nothing>

        /** Transport failures until the attempts ran out. */
        data class Exhausted(val error: AppError) : Attempt<Nothing>
    }

    private sealed interface Step<out T> {
        data class Done<T>(val value: T) : Step<T>
        data class Stopped(val outcome: ResumableOutcome) : Step<Nothing>
    }

    private suspend fun <T> withRetries(progress: ProgressGate, call: suspend () -> AppResult<T>): Attempt<T> {
        var attempt = 1
        while (true) {
            val result = call()
            // The caller's own abort, raised inside the body: rethrown as is.
            progress.callerFailure?.let { throw it }
            when (result) {
                is AppResult.Success -> return Attempt.Ok(result.data)
                is AppResult.Failure -> {
                    if (!result.error.isTransient()) return Attempt.Refused(result.error)
                    if (attempt >= maxAttempts) return Attempt.Exhausted(result.error)
                }
            }
            delay(backoffMillis(attempt))
            attempt++
        }
    }

    /**
     * Once a session exists every stop is worth a later try: the network
     * returns, and a refused session is replaced by a fresh one next time.
     * Only a refused `init` (above) can be final.
     */
    private fun failed(message: String, discard: Boolean) =
        ResumableOutcome.Failed(message = message, retryable = true, discardSession = discard)

    companion object {
        /** Tries per call — the first plus five retries. */
        const val MAX_ATTEMPTS = 6

        private const val BASE_BACKOFF_MILLIS = 1_000L
        private const val MAX_BACKOFF_MILLIS = 30_000L
        private const val MAX_BACKOFF_SHIFT = 5

        /** 1 s, 2 s, 4 s, 8 s, 16 s, then 30 s. */
        fun defaultBackoffMillis(attempt: Int): Long =
            minOf(BASE_BACKOFF_MILLIS shl (attempt - 1).coerceIn(0, MAX_BACKOFF_SHIFT), MAX_BACKOFF_MILLIS)

        /** A session closer than this to its expiry is not resumed: the rest may not land in time. */
        private const val EXPIRY_MARGIN_MILLIS = 10L * 60L * 1_000L

        const val START_FAILED = "Couldn't start the upload. Check your connection."
        const val START_REFUSED = "The server wouldn't take this video."
        const val PART_FAILED = "The upload didn't finish. It will pick up where it stopped."
        const val RESUME_FAILED = "Couldn't resume the upload. Check your connection."
        const val COMPLETE_FAILED = "The server didn't finish the upload. Try again."
        const val EXPIRED = "The upload took too long and expired. Try again."
        const val PLAN_MISMATCH = "The server's upload plan doesn't fit this file."

        internal fun ResumableSession.isExpired(nowMillis: Long): Boolean {
            if (expiresAt.isBlank()) return false
            val expiry = runCatching { OffsetDateTime.parse(expiresAt).toInstant().toEpochMilli() }.getOrNull()
                ?: return false
            return nowMillis >= expiry - EXPIRY_MARGIN_MILLIS
        }
    }
}

/** Transport-shaped failures, which a repeat may fix. Everything else is the server's answer. */
private fun AppError.isTransient(): Boolean = when (this) {
    is AppError.NoNetwork, is AppError.Timeout, is AppError.Server, is AppError.RateLimited,
    is AppError.Malformed,
    -> true
    else -> false
}

/** A refusal no retry can change: the request itself was judged, not the moment. */
private fun AppError.isFinalRefusal(): Boolean = when (this) {
    is AppError.InvalidRequest, is AppError.Forbidden -> true
    is AppError.Unknown -> statusCode != null && statusCode in HTTP_CLIENT_ERRORS
    else -> false
}

private val HTTP_CLIENT_ERRORS = 400..499

/** The part plan: N parts of [ResumableSession.chunkSize], the last the remainder. */
internal fun ResumableSession.planFits(): Boolean =
    chunkSize > 0L && totalParts >= 1 && totalBytes > 0L &&
        totalParts.toLong() == (totalBytes + chunkSize - 1) / chunkSize

internal fun ResumableSession.partOffset(part: Int): Long = (part - 1).toLong() * chunkSize

internal fun ResumableSession.partLength(part: Int): Long = minOf(chunkSize, totalBytes - partOffset(part))

/**
 * Monotonic progress, and the place the caller's abort is caught.
 *
 * Called from OkHttp's writer thread. A throw from the caller's callback is
 * remembered, then surfaced to OkHttp as an `IOException` (anything else
 * thrown inside a request body crashes the dispatcher thread), and the
 * uploader rethrows the original once the call returns.
 */
private class ProgressGate(private val total: Long, private val sink: (Long, Long) -> Unit) {
    @Volatile var callerFailure: Throwable? = null
        private set
    private var reported = -1L

    @Suppress("TooGenericExceptionCaught")
    fun report(bytes: Long) {
        callerFailure?.let { throw it.asIo() }
        val value = bytes.coerceIn(0L, total)
        if (value <= reported) return
        reported = value
        try {
            sink(value, total)
        } catch (e: Throwable) {
            callerFailure = e
            throw e.asIo()
        }
    }

    private fun Throwable.asIo(): IOException = this as? IOException ?: IOException(message, this)
}

/**
 * One part: [length] bytes of [source] from [offset], streamed.
 *
 * Re-openable: OkHttp may write a body twice (the authenticator's retry
 * after a token refresh), and each write opens the source afresh at the
 * offset. A source that ends early fails the part rather than sending a
 * short body the server would refuse anyway.
 */
internal class PartRequestBody(
    private val source: UploadSource,
    private val offset: Long,
    private val length: Long,
    private val onBytes: (sentInPart: Long) -> Unit,
) : RequestBody() {

    override fun contentType() = OCTET_STREAM

    override fun contentLength(): Long = length

    override fun writeTo(sink: BufferedSink) {
        source.openAt(offset).source().use { input ->
            var sent = 0L
            onBytes(0L)
            while (sent < length) {
                val read = input.read(sink.buffer, minOf(SEGMENT_BYTES, length - sent))
                if (read == -1L) {
                    throw IOException("Source ended at byte ${offset + sent}; the part needs ${offset + length}")
                }
                sent += read
                sink.emit()
                onBytes(sent)
            }
            sink.flush()
        }
    }

    private companion object {
        val OCTET_STREAM = "application/octet-stream".toMediaType()
        const val SEGMENT_BYTES = 64L * 1024L
    }
}

@Module
@InstallIn(SingletonComponent::class)
abstract class ResumableUploadModule {
    @Binds
    @Singleton
    abstract fun bindTransport(implementation: RetrofitResumableTransport): ResumableUploadTransport
}
