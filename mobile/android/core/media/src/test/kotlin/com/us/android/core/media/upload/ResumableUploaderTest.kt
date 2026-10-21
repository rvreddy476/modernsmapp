package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import okhttp3.RequestBody
import okio.Buffer
import okio.buffer
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.IOException
import java.io.InputStream

/**
 * The part-by-part upload (2026-10-21: long videos have no size limit).
 *
 * The fake transport WRITES every part body into a buffer, so these tests
 * check the real bytes: that part N is `[(N-1)·chunk, N·chunk)` of the file
 * with the last part the remainder, that a retried part is re-read from its
 * offset, and that progress never runs backwards.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ResumableUploaderTest {

    // ── Fakes ───────────────────────────────────────────────────────────

    private class FakeTransport(
        private val chunk: Long = 10L,
        private val totalPartsOverride: Int? = null,
    ) : ResumableUploadTransport {
        val inits = mutableListOf<ResumableInitRequest>()
        val partAttempts = mutableListOf<Int>()
        val partBytes = linkedMapOf<Int, ByteArray>()
        val completes = mutableListOf<String>()
        val statusCalls = mutableListOf<String>()

        /** Per part number: failures to answer, in order, before succeeding. */
        val partFailures = mutableMapOf<Int, ArrayDeque<AppError>>()

        /** Fail mid-body: write this many bytes of the part, then answer a transport failure. */
        val partialThenFail = mutableMapOf<Int, Long>()
        var initResult: AppResult<ResumableInitDto>? = null
        var completeFailures = ArrayDeque<AppError>()
        var status: AppResult<ResumableStatusDto>? = null

        override suspend fun init(request: ResumableInitRequest): AppResult<ResumableInitDto> {
            inits += request
            initResult?.let { return it }
            val parts = totalPartsOverride ?: ((request.totalBytes + chunk - 1) / chunk).toInt()
            return AppResult.Success(
                ResumableInitDto(
                    uploadId = "up-${inits.size}",
                    mediaId = "media-${inits.size}",
                    chunkSize = chunk,
                    totalParts = parts,
                    expiresAt = "2099-01-01T00:00:00Z",
                ),
            )
        }

        override suspend fun uploadPart(
            uploadId: String,
            partNumber: Int,
            body: RequestBody,
        ): AppResult<ResumableChunkDto> {
            partAttempts += partNumber
            partialThenFail.remove(partNumber)?.let { cut ->
                // Drive the body partly, as a dropped connection would.
                val sink = Buffer()
                runCatching { CuttingSink(sink, cut).buffer().use { body.writeTo(it) } }
                return AppResult.Failure(AppError.NoNetwork())
            }
            partFailures[partNumber]?.removeFirstOrNull()?.let { return AppResult.Failure(it) }
            val buffer = Buffer()
            try {
                body.writeTo(buffer)
            } catch (e: IOException) {
                // What apiCall does with a body that threw.
                return AppResult.Failure(AppError.NoNetwork())
            }
            assertThat(buffer.size).isEqualTo(body.contentLength())
            partBytes[partNumber] = buffer.readByteArray()
            return AppResult.Success(ResumableChunkDto(uploadId = uploadId, partNumber = partNumber))
        }

        override suspend fun complete(uploadId: String): AppResult<MediaAssetDto> {
            completes += uploadId
            completeFailures.removeFirstOrNull()?.let { return AppResult.Failure(it) }
            return AppResult.Success(MediaAssetDto(id = "media-${uploadId.removePrefix("up-")}"))
        }

        override suspend fun status(uploadId: String): AppResult<ResumableStatusDto> {
            statusCalls += uploadId
            return status ?: AppResult.Success(ResumableStatusDto(uploadId = uploadId, status = "uploading"))
        }
    }

    /** A sink that takes [limit] bytes and then fails, like a socket that dropped. */
    private class CuttingSink(private val delegate: Buffer, private val limit: Long) : okio.Sink by delegate {
        private var taken = 0L
        override fun write(source: Buffer, byteCount: Long) {
            val allowed = minOf(byteCount, limit - taken)
            if (allowed > 0) delegate.write(source, allowed)
            taken += allowed
            if (taken >= limit) throw IOException("connection reset")
        }
    }

    /** A file of distinct bytes, recording every offset it was opened at. */
    private class RecordingSource(val bytes: ByteArray) : UploadSource {
        val openedAt = mutableListOf<Long>()
        override fun open(): InputStream = ByteArrayInputStream(bytes)
        override fun openAt(offset: Long): InputStream {
            openedAt += offset
            return super.openAt(offset)
        }
    }

    private fun file(size: Int) = ByteArray(size) { (it % 251).toByte() }

    private fun uploader(transport: ResumableUploadTransport, now: Long = 0L) =
        ResumableUploader(
            transport = transport,
            maxAttempts = ResumableUploader.MAX_ATTEMPTS,
            backoffMillis = { ResumableUploader.defaultBackoffMillis(it) },
            now = { now },
        )

    private class Recorder {
        val sessions = mutableListOf<ResumableSession>()
        val progress = mutableListOf<Long>()
    }

    private suspend fun ResumableUploader.run(
        bytes: ByteArray,
        source: UploadSource = RecordingSource(bytes),
        resume: ResumableSession? = null,
        recorder: Recorder = Recorder(),
        onProgress: (Long, Long) -> Unit = { sent, _ -> recorder.progress += sent },
    ): ResumableOutcome = upload(
        mimeType = "video/mp4",
        totalBytes = bytes.size.toLong(),
        source = source,
        resume = resume,
        onSession = { recorder.sessions += it },
        onProgress = onProgress,
    )

    // ── Slicing ─────────────────────────────────────────────────────────

    @Test
    fun `parts are sliced by the server's chunk size and the last part is the remainder`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        val bytes = file(25)
        val source = RecordingSource(bytes)

        val outcome = uploader(transport).run(bytes, source)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.partBytes.keys).containsExactly(1, 2, 3).inOrder()
        assertThat(transport.partBytes[1]).isEqualTo(bytes.copyOfRange(0, 10))
        assertThat(transport.partBytes[2]).isEqualTo(bytes.copyOfRange(10, 20))
        assertThat(transport.partBytes[3]).isEqualTo(bytes.copyOfRange(20, 25))
        // Each part opened AT its offset — never by reading the parts before it into memory.
        assertThat(source.openedAt).containsExactly(0L, 10L, 20L).inOrder()
        assertThat(transport.inits.single()).isEqualTo(ResumableInitRequest(FILE_TYPE_VIDEO, "video/mp4", 25L))
    }

    @Test
    fun `an exact multiple of the chunk has no short last part`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        val bytes = file(30)

        uploader(transport).run(bytes)

        assertThat(transport.partBytes.mapValues { it.value.size }).containsExactly(1, 10, 2, 10, 3, 10).inOrder()
    }

    @Test
    fun `the session is handed over before the first part and complete is called once`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        val recorder = Recorder()

        uploader(transport).run(file(25), recorder = recorder)

        assertThat(recorder.sessions).containsExactly(
            ResumableSession("up-1", "media-1", 10L, 3, 25L, "2099-01-01T00:00:00Z"),
        )
        assertThat(transport.completes).containsExactly("up-1")
    }

    // ── Retry ───────────────────────────────────────────────────────────

    @Test
    fun `a part that fails on the transport is retried with backoff and then succeeds`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.partFailures[2] = ArrayDeque(listOf(AppError.NoNetwork(), AppError.Server(503, null)))
        val bytes = file(25)

        val outcome = uploader(transport).run(bytes)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.partAttempts).containsExactly(1, 2, 2, 2, 3).inOrder()
        assertThat(transport.partBytes[2]).isEqualTo(bytes.copyOfRange(10, 20))
        // 1 s then 2 s of backoff, in virtual time.
        assertThat(testScheduler.currentTime).isEqualTo(3_000L)
        assertThat(transport.completes).hasSize(1)
    }

    @Test
    fun `a part that keeps failing stops after the attempts run out and keeps the session`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.partFailures[2] = ArrayDeque(List(ResumableUploader.MAX_ATTEMPTS) { AppError.Timeout() })

        val outcome = uploader(transport).run(file(25))

        assertThat(outcome).isEqualTo(
            ResumableOutcome.Failed(ResumableUploader.PART_FAILED, retryable = true, discardSession = false),
        )
        assertThat(transport.partAttempts.count { it == 2 }).isEqualTo(ResumableUploader.MAX_ATTEMPTS)
        assertThat(transport.partAttempts).doesNotContain(3)
        assertThat(transport.completes).isEmpty()
    }

    @Test
    fun `a part the server refuses is not retried and the session is discarded`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.partFailures[1] = ArrayDeque(listOf(AppError.Unknown(code = "BAD_REQUEST", statusCode = 400)))

        val outcome = uploader(transport).run(file(25))

        assertThat(outcome).isEqualTo(
            ResumableOutcome.Failed(ResumableUploader.PART_FAILED, retryable = true, discardSession = true),
        )
        assertThat(transport.partAttempts).containsExactly(1)
        assertThat(testScheduler.currentTime).isEqualTo(0L)
    }

    @Test
    fun `a complete that fails on the transport is retried`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.completeFailures = ArrayDeque(listOf(AppError.Server(502, null)))

        val outcome = uploader(transport).run(file(25))

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.completes).containsExactly("up-1", "up-1")
    }

    @Test
    fun `an init the server refuses is final and an init the network loses is retryable`() = runTest {
        val refused = FakeTransport().apply {
            initResult = AppResult.Failure(AppError.Unknown(code = "BAD_REQUEST", statusCode = 400))
        }
        assertThat(uploader(refused).run(file(25))).isEqualTo(
            ResumableOutcome.Failed(ResumableUploader.START_REFUSED, retryable = false, discardSession = true),
        )
        assertThat(refused.inits).hasSize(1)

        val offline = FakeTransport().apply { initResult = AppResult.Failure(AppError.NoNetwork()) }
        assertThat(uploader(offline).run(file(25))).isEqualTo(
            ResumableOutcome.Failed(ResumableUploader.START_FAILED, retryable = true, discardSession = true),
        )
        assertThat(offline.inits).hasSize(ResumableUploader.MAX_ATTEMPTS)
    }

    @Test
    fun `a plan that does not fit the file is refused before any byte moves`() = runTest {
        val transport = FakeTransport(chunk = 10L, totalPartsOverride = 2)

        val outcome = uploader(transport).run(file(25))

        assertThat(outcome).isEqualTo(
            ResumableOutcome.Failed(ResumableUploader.PLAN_MISMATCH, retryable = false, discardSession = true),
        )
        assertThat(transport.partAttempts).isEmpty()
    }

    // ── Progress ────────────────────────────────────────────────────────

    @Test
    fun `progress starts at zero, ends at the total and never runs backwards across a retried part`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.partialThenFail[2] = 6L
        val recorder = Recorder()

        val outcome = uploader(transport).run(file(25), recorder = recorder)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(recorder.progress.first()).isEqualTo(0L)
        assertThat(recorder.progress.last()).isEqualTo(25L)
        assertThat(recorder.progress).isInStrictOrder()
        // Part 2 dropped mid-body and was re-sent whole; its second pass reported
        // nothing the bar had already shown.
        assertThat(transport.partAttempts).containsExactly(1, 2, 2, 3).inOrder()
        assertThat(transport.partBytes[2]).hasLength(10)
    }

    @Test
    fun `an abort thrown from the progress callback is rethrown as is and never retried`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        val discard = IOException("Publish discarded")

        val thrown = runCatching {
            uploader(transport).run(file(25)) { sent, _ -> if (sent > 12L) throw discard }
        }.exceptionOrNull()

        assertThat(thrown).isSameInstanceAs(discard)
        assertThat(transport.partAttempts).containsExactly(1, 2).inOrder()
        assertThat(transport.completes).isEmpty()
    }

    // ── Resume ──────────────────────────────────────────────────────────

    private val stored = ResumableSession("up-9", "media-9", 10L, 3, 25L, "2099-01-01T00:00:00Z")

    @Test
    fun `a resumed session sends only the parts the server does not have`() = runTest {
        val transport = FakeTransport(chunk = 10L).apply {
            status = AppResult.Success(
                ResumableStatusDto(uploadId = "up-9", status = "uploading", uploadedParts = listOf(1, 2)),
            )
        }
        val bytes = file(25)
        val source = RecordingSource(bytes)
        val recorder = Recorder()

        val outcome = uploader(transport).run(bytes, source, resume = stored, recorder = recorder)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-9"))
        assertThat(transport.inits).isEmpty()
        assertThat(recorder.sessions).isEmpty()
        assertThat(transport.partAttempts).containsExactly(3)
        assertThat(source.openedAt).containsExactly(20L)
        assertThat(transport.partBytes[3]).isEqualTo(bytes.copyOfRange(20, 25))
        assertThat(recorder.progress.first()).isEqualTo(20L)
        assertThat(transport.completes).containsExactly("up-9")
    }

    @Test
    fun `a session the server already completed only needs the complete`() = runTest {
        val transport = FakeTransport(chunk = 10L).apply {
            status = AppResult.Success(ResumableStatusDto(uploadId = "up-9", status = RESUMABLE_STATUS_COMPLETED))
        }

        val outcome = uploader(transport).run(file(25), resume = stored)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-9"))
        assertThat(transport.partAttempts).isEmpty()
        assertThat(transport.completes).containsExactly("up-9")
    }

    @Test
    fun `a session the server no longer knows is replaced by one fresh init`() = runTest {
        val transport = FakeTransport(chunk = 10L).apply { status = AppResult.Failure(AppError.NotFound()) }
        val recorder = Recorder()

        val outcome = uploader(transport).run(file(25), resume = stored, recorder = recorder)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.inits).hasSize(1)
        assertThat(recorder.sessions.single().uploadId).isEqualTo("up-1")
        assertThat(transport.partAttempts).containsExactly(1, 2, 3).inOrder()
    }

    @Test
    fun `an expired or near-expiry session is not resumed at all`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        val expired = stored.copy(expiresAt = "2026-10-21T10:00:00+05:30")
        val now = java.time.OffsetDateTime.parse("2026-10-21T09:55:00+05:30").toInstant().toEpochMilli()

        val outcome = uploader(transport, now = now).run(file(25), resume = expired)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.statusCalls).isEmpty()
        assertThat(transport.inits).hasSize(1)
    }

    @Test
    fun `a session for a different size is not resumed`() = runTest {
        val transport = FakeTransport(chunk = 10L)

        uploader(transport).run(file(31), resume = stored)

        assertThat(transport.statusCalls).isEmpty()
        assertThat(transport.inits.single().totalBytes).isEqualTo(31L)
    }

    @Test
    fun `a resumed part refused by the server falls back to a fresh session once`() = runTest {
        val transport = FakeTransport(chunk = 10L)
        transport.partFailures[1] = ArrayDeque(listOf(AppError.Unknown(code = "BAD_REQUEST", statusCode = 400)))

        val outcome = uploader(transport).run(file(25), resume = stored)

        assertThat(outcome).isEqualTo(ResumableOutcome.Completed("media-1"))
        assertThat(transport.inits).hasSize(1)
        assertThat(transport.completes).containsExactly("up-1")
    }

    // ── Pieces ──────────────────────────────────────────────────────────

    @Test
    fun `backoff doubles from one second and caps at thirty`() {
        assertThat((1..8).map { ResumableUploader.defaultBackoffMillis(it) })
            .containsExactly(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L, 30_000L).inOrder()
    }

    @Test
    fun `the server's 413 asks for the resumable path and nothing else does`() {
        val asked = AppError.Unknown(code = CODE_USE_RESUMABLE_UPLOAD, statusCode = 413)
        assertThat(asked.asksForResumableUpload()).isTrue()
        assertThat(AppError.Unknown(code = null, statusCode = 413).asksForResumableUpload()).isTrue()
        // The code alone is enough, whatever status carried it.
        val codeOnly = AppError.Unknown(code = CODE_USE_RESUMABLE_UPLOAD, statusCode = null)
        assertThat(codeOnly.asksForResumableUpload()).isTrue()
        assertThat(AppError.Unknown(code = "BAD_REQUEST", statusCode = 400).asksForResumableUpload()).isFalse()
        assertThat(AppError.NoNetwork().asksForResumableUpload()).isFalse()
        assertThat(AppError.Server(503, CODE_USE_RESUMABLE_UPLOAD).asksForResumableUpload()).isFalse()
    }

    @Test
    fun `the default openAt skips to the offset and refuses one past the end`() {
        val bytes = file(25)
        val source = UploadSource { ByteArrayInputStream(bytes) }

        assertThat(source.openAt(20L).readBytes()).isEqualTo(bytes.copyOfRange(20, 25))
        assertThat(source.openAt(25L).readBytes()).isEmpty()
        assertThrows(IOException::class.java) { source.openAt(26L) }
    }

    @Test
    fun `a source shorter than its part fails the part instead of sending a short body`() {
        val body = PartRequestBody(UploadSource { ByteArrayInputStream(file(15)) }, offset = 10L, length = 10L) {}
        assertThrows(IOException::class.java) { body.writeTo(Buffer()) }
    }

    @Test
    fun `the threshold is one hundred megabytes`() {
        assertThat(RESUMABLE_THRESHOLD_BYTES).isEqualTo(100L * 1024L * 1024L)
    }
}
