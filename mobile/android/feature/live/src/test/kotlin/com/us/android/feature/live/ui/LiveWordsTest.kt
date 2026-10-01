package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveStatus
import org.junit.Test

/**
 * Protects the live screens' words: the pinned pilot refusal, a sentence for
 * every state and every ended reason on both sides, and the refusal copy
 * for chat and reports.
 */
class LiveWordsTest {

    @Test
    fun `LIVE_NOT_ENABLED says the pilot copy and offers no retry, from either error path`() {
        val fromHttp = goLiveRefusal(AppError.Forbidden(code = "LIVE_NOT_ENABLED"))
        val fromEnvelope = goLiveRefusal(AppError.Unknown(code = "LIVE_NOT_ENABLED", statusCode = null))

        listOf(fromHttp, fromEnvelope).forEach { refusal ->
            assertThat(refusal.message).isEqualTo("Going live is in a closed pilot right now.")
            assertThat(refusal.canRetry).isFalse()
        }
    }

    @Test
    fun `other refusals do not claim the pilot`() {
        val others = listOf(
            AppError.Forbidden(code = "FORBIDDEN"),
            AppError.NoNetwork(),
            AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"),
            AppError.RateLimited(retryAfterSeconds = null),
        )
        others.forEach { assertThat(goLiveRefusal(it).message).isNotEqualTo(LIVE_PILOT_COPY) }
        assertThat(goLiveRefusal(AppError.NoNetwork()).canRetry).isTrue()
        assertThat(goLiveRefusal(AppError.Forbidden(code = "FORBIDDEN")).canRetry).isFalse()
    }

    @Test
    fun `the host sees a banner for every state but live`() {
        LiveStatus.entries.forEach { status ->
            val copy = hostStatusCopy(status, EndedReason.Unknown)
            if (status == LiveStatus.Live) assertThat(copy).isNull() else assertThat(copy).isNotNull()
        }
    }

    @Test
    fun `a viewer sees a banner for every state but live`() {
        LiveStatus.entries.forEach { status ->
            val copy = viewerStatusCopy(status, EndedReason.Unknown)
            if (status == LiveStatus.Live) assertThat(copy).isNull() else assertThat(copy).isNotNull()
        }
    }

    @Test
    fun `every ended reason has its own sentence on each side`() {
        val known = EndedReason.entries - EndedReason.Unknown
        val host = known.map { hostStatusCopy(LiveStatus.Ended, it)!!.detail }
        val viewer = known.map { viewerStatusCopy(LiveStatus.Ended, it)!!.detail }
        assertThat(host.toSet()).hasSize(known.size)
        assertThat(viewer.toSet()).hasSize(known.size)
    }

    @Test
    fun `an admin stop is named as a moderator's on both sides`() {
        assertThat(hostStatusCopy(LiveStatus.Ended, EndedReason.AdminStopped)!!.detail).contains("moderator")
        assertThat(viewerStatusCopy(LiveStatus.Ended, EndedReason.AdminStopped)!!.detail).contains("moderator")
    }

    @Test
    fun `reconnecting and starting are never worded as live`() {
        listOf(LiveStatus.Starting, LiveStatus.Reconnecting).forEach { status ->
            assertThat(viewerStatusCopy(status, EndedReason.Unknown)!!.title).isNotEqualTo("Live")
            assertThat(statusPillLabel(status)).isNotEqualTo("LIVE")
        }
        assertThat(statusPillLabel(LiveStatus.Unknown)).isNull()
    }

    @Test
    fun `the viewer count label is the server's number, compact, never negative`() {
        assertThat(viewerCountLabel(0)).isEqualTo("0 watching")
        assertThat(viewerCountLabel(12)).isEqualTo("12 watching")
        assertThat(viewerCountLabel(1_240)).isEqualTo("1.2K watching")
        assertThat(viewerCountLabel(-3)).isEqualTo("0 watching")
    }

    @Test
    fun `chat refusals name the mute, the blocked word, the pace and the ban`() {
        assertThat(chatSendRefusal(AppError.Forbidden(code = "CHAT_MUTED"))).contains("muted")
        assertThat(chatSendRefusal(AppError.Unknown(code = "CHAT_BLOCKED_WORD", statusCode = 400))).contains("blocked")
        assertThat(chatSendRefusal(AppError.RateLimited(retryAfterSeconds = 5))).contains("too fast")
        assertThat(chatSendRefusal(AppError.Forbidden(code = "LIVE_BANNED")))
            .isEqualTo("You can't chat in this stream.")
        assertThat(chatSendRefusal(AppError.Forbidden(code = "CHAT_BANNED"))).contains("banned")
    }

    @Test
    fun `ALREADY_REPORTED is already reported and anything else can be retried`() {
        assertThat(reportStateFor(AppError.Unknown(code = "ALREADY_REPORTED", statusCode = 409)))
            .isEqualTo(UsPostReportState.AlreadyReported)
        assertThat(reportStateFor(AppError.Unknown(code = "CONFLICT", statusCode = 409)))
            .isEqualTo(UsPostReportState.Failed)
        assertThat(reportStateFor(AppError.Unknown(code = null, statusCode = 400))).isEqualTo(UsPostReportState.Failed)
        assertThat(reportStateFor(AppError.RateLimited(retryAfterSeconds = null))).isEqualTo(UsPostReportState.Failed)
    }

    @Test
    fun `a user is labelled by the first six characters of their id`() {
        assertThat(shortUserLabel("5f0c2a9e-1111-2222")).isEqualTo("user 5f0c2a")
    }
}
