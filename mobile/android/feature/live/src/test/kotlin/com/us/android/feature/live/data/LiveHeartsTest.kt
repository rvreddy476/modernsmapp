package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/** Free hearts: batches of at most 20, refusals that stop sending, totals that never go down. */
class LiveHeartsTest {

    @Test
    fun `a batch is at most twenty and never negative`() {
        assertThat(heartsBatch(0)).isEqualTo(0)
        assertThat(heartsBatch(7)).isEqualTo(7)
        assertThat(heartsBatch(25)).isEqualTo(HEART_BATCH_MAX)
        assertThat(heartsBatch(-3)).isEqualTo(0)
    }

    @Test
    fun `hearts are open only on air`() {
        assertThat(heartsOpen(LiveStatus.Live)).isTrue()
        assertThat(heartsOpen(LiveStatus.Reconnecting)).isTrue()
        LiveStatus.entries.filterNot { it == LiveStatus.Live || it == LiveStatus.Reconnecting }.forEach {
            assertThat(heartsOpen(it)).isFalse()
        }
    }

    @Test
    fun `a ban, an ended stream and a lost session stop hearts, anything else drops the batch`() {
        assertThat(heartBlockFor(AppError.Forbidden(code = "BANNED_FROM_STREAM"))).isEqualTo(HeartBlock.Banned)
        assertThat(heartBlockFor(AppError.Forbidden(code = "LIVE_BANNED"))).isEqualTo(HeartBlock.Banned)
        assertThat(heartBlockFor(AppError.Unknown(code = "STREAM_NOT_LIVE", statusCode = 409))).isEqualTo(HeartBlock.NotLive)
        assertThat(heartBlockFor(AppError.AuthFailed())).isEqualTo(HeartBlock.SignedOut)
        assertThat(heartBlockFor(AppError.RateLimited(retryAfterSeconds = 10))).isNull()
        assertThat(heartBlockFor(AppError.NoNetwork())).isNull()
    }

    @Test
    fun `a frame's total wins when it is larger, and a frame without one adds its batch`() {
        assertThat(heartTotalAfterFrame(100, LiveFrame.Hearts("s1", 3, 515))).isEqualTo(515L)
        assertThat(heartTotalAfterFrame(600, LiveFrame.Hearts("s1", 3, 515))).isEqualTo(600L)
        assertThat(heartTotalAfterFrame(100, LiveFrame.Hearts("s1", 3, 0))).isEqualTo(103L)
    }
}
