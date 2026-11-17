package com.us.android.core.media.upload

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/** The upload refusals of 17 Nov 2026, as the publish tile words them. */
class UploadRefusalTest {

    @Test
    fun `each code reads as its own sentence, with the wait when the server gave one`() {
        val cases = mapOf(
            UploadRefusal(CODE_UPLOAD_QUOTA_EXCEEDED, retryAfterSeconds = 3_600L) to
                "You've reached today's video upload limit. You can try again in about an hour.",
            UploadRefusal(CODE_LONG_VIDEO_IN_PROGRESS, retryAfterSeconds = 600L) to
                "You already have long videos processing. Upload this one when one of them is finished. " +
                "You can try again in about 10 minutes.",
            UploadRefusal(CODE_UPLOAD_LIMITER_UNAVAILABLE, retryAfterSeconds = 30L) to
                "Uploads are paused for a moment. Try again in a minute.",
            UploadRefusal(CODE_UPLOAD_EXPIRED) to "The upload took too long and expired. Start it again.",
            UploadRefusal(CODE_UPLOAD_INCOMPLETE) to "Part of the upload is missing. Tap Retry to send the rest.",
            UploadRefusal(CODE_RATE_LIMITED, retryAfterSeconds = 7_200L) to
                "You've started too many uploads for now. You can try again in about 2 hours.",
            UploadRefusal(CODE_VIDEO_TOO_LONG, details = mapOf(DETAIL_MAX_DURATION_SECONDS to "14400")) to
                "This video is longer than 4 hours, the longest we can process. Trim it or split it into parts.",
            UploadRefusal(CODE_VIDEO_TOO_LONG) to "This video is too long to upload. Trim it or split it into parts.",
        )
        cases.forEach { (refusal, sentence) -> assertThat(refusal.message).isEqualTo(sentence) }
    }

    @Test
    fun `the server's own sentence wins, and only an over-long video is final`() {
        val tooLong = UploadRefusal(CODE_VIDEO_TOO_LONG, serverMessage = "Longer than 4 hours.")
        assertThat(tooLong.message).isEqualTo("Longer than 4 hours.")
        assertThat(tooLong.final).isTrue()
        listOf(CODE_UPLOAD_QUOTA_EXCEEDED, CODE_LONG_VIDEO_IN_PROGRESS, CODE_UPLOAD_EXPIRED, CODE_RATE_LIMITED)
            .forEach { assertThat(UploadRefusal(it).final).isFalse() }
    }

    @Test
    fun `refusals are read from the errors the transport makes, and nothing else is one`() {
        val quota = AppError.Unknown(
            code = CODE_UPLOAD_QUOTA_EXCEEDED,
            statusCode = 429,
            message = "Daily limit.",
            details = mapOf("max" to "10", DETAIL_RETRY_AFTER_SECONDS to "3600"),
        )
        assertThat(quota.uploadRefusal())
            .isEqualTo(UploadRefusal(CODE_UPLOAD_QUOTA_EXCEEDED, "Daily limit.", mapOf("max" to "10"), 3_600L))
        assertThat(AppError.Server(503, CODE_UPLOAD_LIMITER_UNAVAILABLE).uploadRefusal()?.code)
            .isEqualTo(CODE_UPLOAD_LIMITER_UNAVAILABLE)
        assertThat(AppError.Server(503, null).uploadRefusal()).isNull()
        assertThat(AppError.Unknown(code = "BAD_REQUEST", statusCode = 400).uploadRefusal()).isNull()
        assertThat(AppError.RateLimited(retryAfterSeconds = 5L).uploadRefusal()).isNull()
    }

    @Test
    fun `a 429 that is a wait becomes its code, a short one stays a blip`() {
        val mapped = AppError.RateLimited(retryAfterSeconds = 600L, requestId = "r1")
        val wait = RawUploadRefusal(429, CODE_LONG_VIDEO_IN_PROGRESS, "busy", emptyMap(), 600L).reshape(mapped)
        assertThat(wait).isEqualTo(
            AppError.Unknown(
                code = CODE_LONG_VIDEO_IN_PROGRESS,
                statusCode = 429,
                requestId = "r1",
                message = "busy",
                details = mapOf(DETAIL_RETRY_AFTER_SECONDS to "600"),
            ),
        )
        val blip = AppError.RateLimited(retryAfterSeconds = 5L)
        val short = RawUploadRefusal(429, CODE_RATE_LIMITED, "slow", emptyMap(), 5L)
        assertThat(short.reshape(blip)).isSameInstanceAs(blip)
    }

    @Test
    fun `waits and lengths in words`() {
        assertThat(formatWait(30L)).isEqualTo("a minute")
        assertThat(formatWait(600L)).isEqualTo("about 10 minutes")
        assertThat(formatWait(3_600L)).isEqualTo("about an hour")
        assertThat(formatWait(10_000L)).isEqualTo("about 3 hours")
        assertThat(formatDuration(14_400L)).isEqualTo("4 hours")
        assertThat(formatDuration(3_600L)).isEqualTo("1 hour")
        assertThat(formatDuration(5_400L)).isEqualTo("1 h 30 min")
        assertThat(formatDuration(600L)).isEqualTo("10 minutes")
    }
}
