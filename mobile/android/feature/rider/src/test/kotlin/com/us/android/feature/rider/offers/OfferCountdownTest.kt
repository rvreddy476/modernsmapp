package com.us.android.feature.rider.offers

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Instant

class OfferCountdownTest {

    private val expiry = Instant.parse("2026-09-13T06:35:00Z")
    private val countdown = OfferCountdown(expiry)

    @Test
    fun `an open offer counts down in whole seconds, rounded up`() {
        assertThat(countdown.at(expiry.minusSeconds(45))).isEqualTo(OfferWindow.Open(45, urgent = false))
        assertThat(countdown.at(expiry.minusMillis(44_001))).isEqualTo(OfferWindow.Open(45, urgent = false))
        assertThat(countdown.at(expiry.minusSeconds(10))).isEqualTo(OfferWindow.Open(10, urgent = true))
        assertThat(countdown.at(expiry.minusMillis(1))).isEqualTo(OfferWindow.Open(1, urgent = true))
    }

    @Test
    fun `at its expires_at the offer expires and can no longer be accepted`() {
        assertThat(countdown.canRespond(expiry.minusMillis(1))).isTrue()
        assertThat(countdown.at(expiry)).isEqualTo(OfferWindow.Expired)
        assertThat(countdown.canRespond(expiry)).isFalse()
        assertThat(countdown.canRespond(expiry.plusSeconds(30))).isFalse()
    }

    @Test
    fun `both server time formats are read, anything else is unknown and left to the server`() {
        assertThat(OfferCountdown.of("2026-09-13T06:35:00Z").expiresAt).isEqualTo(expiry)
        assertThat(OfferCountdown.of("2026-09-13 06:35:00+00").expiresAt).isEqualTo(expiry)
        assertThat(OfferCountdown.of("2026-09-13 12:05:00.5+05:30").expiresAt).isEqualTo(expiry.plusMillis(500))

        val unknown = OfferCountdown.of("soon")
        assertThat(unknown.at(expiry)).isEqualTo(OfferWindow.Unknown)
        assertThat(unknown.canRespond(expiry)).isTrue()
    }
}
