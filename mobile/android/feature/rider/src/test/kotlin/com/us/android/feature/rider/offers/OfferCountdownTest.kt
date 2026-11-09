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

    @Test
    fun `the server clock offset is the Date header against the device clock at receipt`() {
        val received = Instant.parse("2026-09-13T06:34:30Z")

        val fast = ServerClockOffset.from(serverTime = Instant.parse("2026-09-13T06:34:10Z"), receivedAt = received)
        assertThat(fast.serverNow(received)).isEqualTo(Instant.parse("2026-09-13T06:34:10Z"))
        val window = countdown.at(fast.serverNow(received.plusSeconds(5)))
        assertThat(window).isEqualTo(OfferWindow.Open(45, urgent = false))

        assertThat(ServerClockOffset.from(serverTime = null, receivedAt = received)).isEqualTo(ServerClockOffset.NONE)
        assertThat(ServerClockOffset.NONE.serverNow(received)).isEqualTo(received)
    }

    // ── seconds_to_expiry (food server lane, 9 Nov 2026) ───────────────

    private val received = Instant.parse("2026-09-13T06:30:00Z")

    @Test
    fun `an offer's seconds_to_expiry wins over its expires_at`() {
        val offer = goldenOffer().copy(secondsToExpiry = 12)

        val countdowns = OfferExpiry.fromCountdowns(listOf(offer), received)

        assertThat(OfferExpiry.of(offer, countdowns)).isEqualTo(received.plusSeconds(12))
    }

    @Test
    fun `an offer without seconds_to_expiry falls back to its expires_at`() {
        val older = goldenOffer().copy(secondsToExpiry = null)

        val countdowns = OfferExpiry.fromCountdowns(listOf(older), received)

        assertThat(countdowns).isEmpty()
        assertThat(OfferExpiry.of(older, countdowns)).isEqualTo(GOLDEN_EXPIRY)
    }

    @Test
    fun `a negative seconds_to_expiry is not read, and the offer falls back to its expires_at`() {
        val odd = goldenOffer().copy(secondsToExpiry = -5)

        assertThat(OfferExpiry.of(odd, OfferExpiry.fromCountdowns(listOf(odd), received))).isEqualTo(GOLDEN_EXPIRY)
    }

    @Test
    fun `the countdown from seconds_to_expiry ignores the server clock offset`() {
        val offer = goldenOffer()
        val deviceAtReceipt = Instant.parse("2026-09-13T07:45:00Z")
        // The phone runs 75 minutes fast; the Date header said 06:30:00.
        val clock = ServerClockOffset.from(Instant.parse("2026-09-13T06:30:00Z"), deviceAtReceipt)
        val snapshot = OfferSnapshot(
            offers = listOf(offer),
            clock = clock,
            countdowns = OfferExpiry.fromCountdowns(listOf(offer), clock.serverNow(deviceAtReceipt)),
        )

        assertThat(snapshot.windowOf(offer, deviceAtReceipt.plusSeconds(5)))
            .isEqualTo(OfferWindow.Open(15, urgent = false))
        assertThat(snapshot.windowOf(offer, deviceAtReceipt.plusSeconds(20))).isEqualTo(OfferWindow.Expired)
    }
}
