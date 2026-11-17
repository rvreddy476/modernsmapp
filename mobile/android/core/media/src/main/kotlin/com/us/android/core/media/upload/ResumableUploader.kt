package com.us.android.core.media.upload

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
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

/**
 * Whether a refused simple `init` is the server asking for the resumable path:
 * its code, or a 413 with no code at all (a server older than the code). A
 * 413 with ANOTHER code refuses the file, not the route: [CODE_VIDEO_TOO_LARGE]
 * (2026-11-07) is refused by the resumable route too, and falling back there
 * only earned a second 413.
 */
fun AppError.asksForResumableUpload(): Boolean =
    this is AppError.Unknown &&
        (code == CODE_USE_RESUMABLE_UPLOAD || (code == null && statusCode == HTTP_PAYLOAD_TOO_LARGE))

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
    /**
     * RFC 3339, the LATEST the server sent (init, then each part and status
     * answer: it slides). Empty when it said nothing. Kept for the record;
     * whether the session lives is always the server's answer, never this.
     */
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
     *
     * [transient] (2026-11-07): the NETWORK ran out of retries, not the server
     * refusing anything; the caller should try again by itself once the phone
     * is back online, rather than wait for a tap. [progressed]: at least one
     * part was confirmed during this call, so the upload is further on than
     * when it started and a caller counting failed tries may start again.
     */
    data class Failed(
        val message: String,
        val retryable: Boolean,
        val discardSession: Boolean,
        val transient: Boolean = false,
        val progressed: Boolean = false,
        /** The server said the session expired (410 `UPLOAD_EXPIRED`, or status `expired`). */
        val expired: Boolean = false,
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
        uploadCall(errorMapper) { api.init(request) }

    // Every route keeps the upload refusals' codes, details and Retry-After.
    override suspend fun uploadPart(
        uploadId: String,
        partNumber: Int,
        body: RequestBody,
    ): AppResult<ResumableChunkDto> = uploadCall(errorMapper) { api.uploadPart(uploadId, partNumber, body) }

    override suspend fun complete(uploadId: String): AppResult<MediaAssetDto> =
        uploadCall(errorMapper) { api.complete(uploadId) }

    override suspend fun status(uploadId: String): AppResult<ResumableStatusDto> =
        uploadCall(errorMapper) { api.status(uploadId) }
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
 * sends only the rest. That outcome is [ResumableOutcome.Failed.transient]:
 * these retries ride out a blip of about half a minute, and a longer loss of
 * signal (a tunnel, a lift) is the caller's to wait out — the publish
 * worker waits for the network and runs again by itself. A resumed session that turns out to be dead
 * (expired, unknown, refused) is replaced by ONE fresh `init` in the same
 * call before giving up.
 *
 * ## EXPIRY IS THE SERVER'S (lv-16, 2026-11-18)
 *
 * A session expires on inactivity: every accepted part pushes `expires_at`
 * on, up to a 7-day ceiling, and every part and status answer carries the
 * new value. The uploader keeps the latest one on the session it persists,
 * and NEVER discards a session on the phone's clock: a stored session is
 * always asked about (`status`) and only the server's "expired" (status
 * `expired` or 410 `UPLOAD_EXPIRED`) starts again from byte zero. The old
 * rule — give up locally 24 h after `init` — threw away live sessions that
 * had been moving for days. A session that expires mid-run is replaced by
 * one fresh `init` in the same call, and [upload]'s `onRestarted` tells the
 * caller so the creator can be told. A `complete` refused 409
 * `UPLOAD_INCOMPLETE` reads the status and sends the missing parts once.
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
     * session this call opens — persist it before the first part goes up —
     * and again whenever the server moves the session's `expires_at` on.
     *
     * [durationMs] is the video's length as the phone read it, 0 when it
     * could not (N-3): declared at `init` so an over-long video is refused
     * with 413 `VIDEO_TOO_LONG` before a single part is sent.
     * [onRestarted] is called when the server expired the session and a new
     * one is starting from the beginning.
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
        durationMs: Long = 0L,
        onRestarted: () -> Unit = {},
    ): ResumableOutcome {
        require(totalBytes > 0L) { "nothing to upload" }
        val progress = ProgressGate(totalBytes, onProgress)
        val request = ResumableInitRequest(
            fileType = fileType,
            mimeType = mimeType,
            totalBytes = totalBytes,
            durationMs = durationMs.coerceAtLeast(0L),
        )
        val outcome = uploadOnce(request, source, resume, onSession, progress, onRestarted)
        // Whether this call moved the upload on, whichever session it ran.
        return if (outcome is ResumableOutcome.Failed) outcome.copy(progressed = progress.partsLanded > 0) else outcome
    }

    @Suppress("LongParameterList")
    private suspend fun uploadOnce(
        request: ResumableInitRequest,
        source: UploadSource,
        resume: ResumableSession?,
        onSession: suspend (ResumableSession) -> Unit,
        progress: ProgressGate,
        onRestarted: () -> Unit,
    ): ResumableOutcome {
        // Never judged on the phone's clock: the status call says whether it lives.
        val resumable = resume?.takeIf { it.totalBytes == request.totalBytes }
        var restarted = false
        if (resumable != null) {
            val resumed = runSession(resumable, source, progress, resumed = true, onSession)
            if (resumed !is ResumableOutcome.Failed || !resumed.discardSession) return resumed
            if (resumed.expired) {
                progress.restart()
                onRestarted()
                restarted = true
            }
        }
        val fresh = openAndRun(request, source, progress, onSession)
        if (restarted || fresh !is ResumableOutcome.Failed || !fresh.expired) return fresh
        // The fresh session expired under this very run (a stall of hours): once more, from zero.
        progress.restart()
        onRestarted()
        return openAndRun(request, source, progress, onSession)
    }

    private suspend fun openAndRun(
        request: ResumableInitRequest,
        source: UploadSource,
        progress: ProgressGate,
        onSession: suspend (ResumableSession) -> Unit,
    ): ResumableOutcome {
        val opened = when (val result = open(request, progress)) {
            is Step.Done -> result.value
            is Step.Stopped -> return result.outcome
        }
        onSession(opened)
        return runSession(opened, source, progress, resumed = false, onSession)
    }

    // ── One session ─────────────────────────────────────────────────────

    private suspend fun open(request: ResumableInitRequest, progress: ProgressGate): Step<ResumableSession> {
        val totalBytes = request.totalBytes
        val init = when (val result = withRetries(progress) { transport.init(request) }) {
            is Attempt.Ok -> result.value
            // The server refused THIS file, or this moment (a quota, a wait).
            is Attempt.Refused -> return Step.Stopped(initRefused(result.error))
            is Attempt.Exhausted -> return Step.Stopped(
                failed(result.error.uploadRefusal()?.message ?: START_FAILED, discard = true, transient = true),
            )
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

    /** `init` said no: the refusal's own words when it is one we know, and whether waiting helps. */
    private fun initRefused(error: AppError): ResumableOutcome.Failed {
        val refusal = error.uploadRefusal()
        return ResumableOutcome.Failed(
            // Over the platform's caps (2026-11-07, 2026-11-17): say so, and the cap.
            message = refusal?.message ?: error.videoTooLarge()?.message ?: START_REFUSED,
            // A quota or a wait is worth a later Retry; the same file over a cap is not.
            retryable = refusal?.let { !it.final } ?: !error.isFinalRefusal(),
            discardSession = true,
        )
    }

    // One loop over the parts, then one bounded re-send round; one exit per way a session stops.
    @Suppress("CyclomaticComplexMethod", "ReturnCount")
    private suspend fun runSession(
        session: ResumableSession,
        source: UploadSource,
        progress: ProgressGate,
        resumed: Boolean,
        onSession: suspend (ResumableSession) -> Unit,
    ): ResumableOutcome {
        if (!session.planFits()) return ResumableOutcome.Failed(PLAN_MISMATCH, false, discardSession = true)
        val keeper = SessionKeeper(session, onSession, now)
        var landed = if (resumed) {
            when (val known = landedParts(keeper, progress)) {
                is Step.Done -> known.value
                is Step.Stopped -> return known.outcome
            }
        } else {
            emptySet()
        }
        var round = 0
        while (true) {
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
                    is Attempt.Ok -> keeper.saw(sent.value.expiresAt)
                    is Attempt.Refused -> return partRefused(sent.error)
                    is Attempt.Exhausted -> return failed(
                        sent.error.uploadRefusal()?.message ?: PART_FAILED,
                        discard = false,
                        transient = true,
                    )
                }
                progress.partsLanded++
                confirmed += length
                progress.report(confirmed)
            }
            val finished = finish(keeper.session, progress)
            // 409 UPLOAD_INCOMPLETE: ask which parts are missing and send those, once.
            if (finished !== Incomplete) return finished
            if (round++ > 0) return failed(COMPLETE_FAILED, discard = false)
            landed = when (val known = landedParts(keeper, progress)) {
                is Step.Done -> known.value
                is Step.Stopped -> return known.outcome
            }
        }
    }

    /**
     * A part the server refused. Expired: say so and start again. Over the
     * byte cap: final. Anything else is the session's, not the network's:
     * forget it, and the next attempt opens a fresh one.
     */
    private fun partRefused(error: AppError): ResumableOutcome.Failed {
        if (error.isExpiredSession()) return expiredOutcome()
        error.videoTooLarge()?.let {
            return ResumableOutcome.Failed(it.message, retryable = false, discardSession = true)
        }
        return failed(PART_FAILED, discard = true)
    }

    /**
     * Which parts the server already holds, and its latest expiry. A session
     * it no longer knows is discarded; one it says has expired is discarded
     * AS expired (the creator is told); a COMPLETED one needs nothing but the
     * (idempotent) complete.
     */
    private suspend fun landedParts(keeper: SessionKeeper, progress: ProgressGate): Step<Set<Int>> {
        val session = keeper.session
        return when (val result = withRetries(progress) { transport.status(session.uploadId) }) {
            is Attempt.Ok -> {
                keeper.saw(result.value.expiresAt, always = true)
                when (result.value.status) {
                    RESUMABLE_STATUS_EXPIRED -> Step.Stopped(expiredOutcome())
                    RESUMABLE_STATUS_COMPLETED -> Step.Done((1..session.totalParts).toSet())
                    else -> Step.Done(result.value.uploadedParts.filter { it in 1..session.totalParts }.toSet())
                }
            }
            is Attempt.Refused -> Step.Stopped(
                if (result.error.isExpiredSession()) expiredOutcome() else failed(RESUME_FAILED, discard = true),
            )
            is Attempt.Exhausted -> Step.Stopped(
                failed(result.error.uploadRefusal()?.message ?: RESUME_FAILED, discard = false, transient = true),
            )
        }
    }

    /**
     * `complete` — retried on the transport like a part. [Incomplete] when
     * the server says parts are missing (the caller re-sends them). A quota
     * or "long video in progress" refusal keeps the session: the server kept
     * the upload, and a later Retry completes it. Over the length cap is
     * final. Any other refusal keeps the session too: the next status says
     * what is missing.
     */
    private suspend fun finish(session: ResumableSession, progress: ProgressGate): ResumableOutcome =
        when (val result = withRetries(progress) { transport.complete(session.uploadId) }) {
            is Attempt.Ok -> ResumableOutcome.Completed(result.value.id.ifBlank { session.mediaId })
            is Attempt.Refused -> {
                val refusal = result.error.uploadRefusal()
                when {
                    refusal?.code == CODE_UPLOAD_INCOMPLETE -> Incomplete
                    result.error.isExpiredSession() -> expiredOutcome()
                    refusal?.final == true -> ResumableOutcome.Failed(refusal.message, false, discardSession = true)
                    refusal != null -> failed(refusal.message, discard = false)
                    else -> failed(COMPLETE_FAILED, discard = false)
                }
            }
            is Attempt.Exhausted -> failed(
                result.error.uploadRefusal()?.message ?: COMPLETE_FAILED,
                discard = false,
                transient = true,
            )
        }

    private fun expiredOutcome() =
        ResumableOutcome.Failed(EXPIRED, retryable = true, discardSession = true, expired = true)

    /**
     * The session as the server last described it. Every part and status
     * answer may move `expires_at` on; the new value is persisted through
     * `onSession` — at once after a status read, and at most once a minute
     * while parts stream (a write per 5 MiB part would be thousands).
     */
    private class SessionKeeper(
        var session: ResumableSession,
        private val onSession: suspend (ResumableSession) -> Unit,
        private val now: () -> Long,
    ) {
        private var persistedAt = now()

        suspend fun saw(expiresAt: String, always: Boolean = false) {
            if (expiresAt.isBlank() || expiresAt == session.expiresAt) return
            session = session.copy(expiresAt = expiresAt)
            val at = now()
            if (always || at - persistedAt >= PERSIST_EVERY_MILLIS) {
                persistedAt = at
                onSession(session)
            }
        }
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
     * Only a refused `init` (above) can be final. [transient]: the retries ran
     * out on the network, and the caller tries again by itself.
     */
    private fun failed(message: String, discard: Boolean, transient: Boolean = false) =
        ResumableOutcome.Failed(message = message, retryable = true, discardSession = discard, transient = transient)

    companion object {
        /** Tries per call — the first plus five retries. */
        const val MAX_ATTEMPTS = 6

        private const val BASE_BACKOFF_MILLIS = 1_000L
        private const val MAX_BACKOFF_MILLIS = 30_000L
        private const val MAX_BACKOFF_SHIFT = 5

        /** 1 s, 2 s, 4 s, 8 s, 16 s, then 30 s. */
        fun defaultBackoffMillis(attempt: Int): Long =
            minOf(BASE_BACKOFF_MILLIS shl (attempt - 1).coerceIn(0, MAX_BACKOFF_SHIFT), MAX_BACKOFF_MILLIS)

        /** How often a moving `expires_at` is written back while parts stream. */
        private const val PERSIST_EVERY_MILLIS = 60_000L

        /** [finish]'s private answer to 409 `UPLOAD_INCOMPLETE`; compared by identity, never returned. */
        private val Incomplete =
            ResumableOutcome.Failed("incomplete", retryable = true, discardSession = false)

        /** What the creator is told when the server expired the session and it starts again (lv-16). */
        const val RESTARTED = "Upload expired. Starting again."

        const val START_FAILED = "Couldn't start the upload. Check your connection."
        const val START_REFUSED = "The server wouldn't take this video."
        const val PART_FAILED = "The upload didn't finish. It will pick up where it stopped."
        const val RESUME_FAILED = "Couldn't resume the upload. Check your connection."
        const val COMPLETE_FAILED = "The server didn't finish the upload. Try again."
        const val EXPIRED = "The upload took too long and expired. Try again."
        const val PLAN_MISMATCH = "The server's upload plan doesn't fit this file."
    }
}

/** The server's word that a resumable session is gone for good: 410 `UPLOAD_EXPIRED`. */
private fun AppError.isExpiredSession(): Boolean =
    this is AppError.Unknown && (code == CODE_UPLOAD_EXPIRED || (code == null && statusCode == HTTP_GONE))

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
 * One call's progress: monotonic bytes, the parts it landed, and the place
 * the caller's abort is caught.
 *
 * Called from OkHttp's writer thread. A throw from the caller's callback is
 * remembered, then surfaced to OkHttp as an `IOException` (anything else
 * thrown inside a request body crashes the dispatcher thread), and the
 * uploader rethrows the original once the call returns.
 */
private class ProgressGate(private val total: Long, private val sink: (Long, Long) -> Unit) {
    @Volatile var callerFailure: Throwable? = null
        private set

    /** Parts the server confirmed during this call: the upload moved on. */
    var partsLanded = 0
    private var reported = -1L

    /** The server expired the session and the bytes go up again from zero: the bar may start over, once. */
    fun restart() {
        reported = -1L
    }

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
