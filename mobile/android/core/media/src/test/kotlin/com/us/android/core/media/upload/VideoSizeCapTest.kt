package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/**
 * media-service's 413 `VIDEO_TOO_LARGE` (2026-11-07): read as its own
 * refusal with the cap it names, said in GB, and never mistaken for the
 * server asking for the resumable route (which refuses it too).
 */
class VideoSizeCapTest {

    private val sixteenGib = 16L * 1024L * 1024L * 1024L

    private fun tooLarge(details: Map<String, String> = mapOf(DETAIL_MAX_BYTES to "$sixteenGib")) =
        AppError.Unknown(code = CODE_VIDEO_TOO_LARGE, statusCode = 413, details = details)

    @Test
    fun `the refusal carries the cap the server named`() {
        assertThat(tooLarge().videoTooLarge()).isEqualTo(VideoTooLarge(maxBytes = sixteenGib))
        assertThat(tooLarge().videoTooLarge()?.message).isEqualTo("This video is too big to upload (max 16 GB).")
    }

    @Test
    fun `a refusal without a readable cap still says it is too big`() {
        for (details in listOf(emptyMap(), mapOf(DETAIL_MAX_BYTES to "lots"), mapOf(DETAIL_MAX_BYTES to "0"))) {
            val refusal = tooLarge(details).videoTooLarge()
            assertThat(refusal).isEqualTo(VideoTooLarge(maxBytes = null))
            assertThat(refusal?.message).isEqualTo("This video is too big to upload.")
        }
    }

    @Test
    fun `nothing else is the refusal`() {
        assertThat(AppError.Unknown(code = CODE_USE_RESUMABLE_UPLOAD, statusCode = 413).videoTooLarge()).isNull()
        assertThat(AppError.Unknown(code = null, statusCode = 413).videoTooLarge()).isNull()
        assertThat(AppError.Server(statusCode = 503, code = CODE_VIDEO_TOO_LARGE).videoTooLarge()).isNull()
        assertThat(AppError.NoNetwork().videoTooLarge()).isNull()
    }

    @Test
    fun `too large is not the server asking for the resumable route`() {
        assertThat(tooLarge().asksForResumableUpload()).isFalse()
        // Another coded 413 refuses the file too.
        assertThat(AppError.Unknown(code = "FILE_TOO_LARGE", statusCode = 413).asksForResumableUpload()).isFalse()
    }

    @Test
    fun `the cap reads in GB to a tenth, rounded down, and in MB under a GB`() {
        assertThat(formatCap(sixteenGib)).isEqualTo("16 GB")
        assertThat(formatCap(sixteenGib + 1L)).isEqualTo("16 GB")
        assertThat(formatCap(3L * 512L * 1024L * 1024L)).isEqualTo("1.5 GB")
        assertThat(formatCap(2L * 1024L * 1024L * 1024L - 1L)).isEqualTo("1.9 GB")
        assertThat(formatCap(500L * 1024L * 1024L)).isEqualTo("500 MB")
        assertThat(formatCap(1L)).isEqualTo("1 MB")
    }

    @Test
    fun `details are the envelope's primitives, and nothing else is read`() {
        val body = """{"error":{"code":"VIDEO_TOO_LARGE","details":{"max_bytes":17179869184,"x":null,"y":[1]}}}"""
        assertThat(errorDetails(body)).containsExactly(DETAIL_MAX_BYTES, "17179869184")
        assertThat(errorDetails("""{"error":{"code":"VIDEO_TOO_LARGE"}}""")).isEmpty()
        assertThat(errorDetails("not json")).isEmpty()
        assertThat(errorDetails("""{"data":{}}""")).isEmpty()
    }
}
