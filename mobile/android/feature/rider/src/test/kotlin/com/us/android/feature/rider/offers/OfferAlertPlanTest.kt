package com.us.android.feature.rider.offers

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.time.Duration

/** Which offers get a heads-up while the rider is on duty, and which are taken down (food audit H1). */
class OfferAlertPlanTest {

    private val offer = goldenOffer()
    private val second = offer.copy(id = "offer-2", expiresAt = "2026-09-13 06:30:35+00")

    @Test
    fun `a new offer is alerted once, for the time it has left on the server's clock`() {
        val changes = OfferAlertPlan.diff(emptySet(), listOf(offer), GOLDEN_EXPIRY.minusSeconds(18))

        assertThat(changes.post).containsExactly(OfferAlert(offer.id, Duration.ofSeconds(18)))
        assertThat(changes.cancel).isEmpty()
    }

    @Test
    fun `an offer already alerted is not alerted again on the next refresh`() {
        val changes = OfferAlertPlan.diff(setOf(offer.id), listOf(offer, second), GOLDEN_EXPIRY.minusSeconds(5))

        assertThat(changes.post.map { it.offerId }).containsExactly("offer-2")
        assertThat(changes.cancel).isEmpty()
    }

    @Test
    fun `an offer that left the list is taken down`() {
        val changes = OfferAlertPlan.diff(setOf(offer.id, "offer-2"), listOf(second), GOLDEN_EXPIRY)

        assertThat(changes.post).isEmpty()
        assertThat(changes.cancel).containsExactly(offer.id)
    }

    @Test
    fun `an expired or unreadable offer never rings, and an alerted one that expired is taken down`() {
        val unreadable = offer.copy(id = "offer-3", expiresAt = "soon")

        val changes = OfferAlertPlan.diff(setOf(offer.id), listOf(offer, unreadable), GOLDEN_EXPIRY)

        assertThat(changes.post).isEmpty()
        assertThat(changes.cancel).containsExactly(offer.id)
    }
}
