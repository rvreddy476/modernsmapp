package com.us.android.core.notifications

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class DoorstepOfferNotificationTest {
    private val now = Instant.parse("2026-10-10T10:00:00Z")
    private val data = mapOf("type" to "doorstep.pro.offer.new", "entity_id" to "offer", "expires_at" to now.plusSeconds(30).toString())

    @Test fun `delivery counts down remaining time rather than restarting deadline`() {
        assertEquals(DoorstepOfferNotification.Open("offer", 20000), DoorstepOfferNotification.evaluate(data, now.plusSeconds(10)))
        assertEquals(DoorstepOfferNotification.Expired("offer"), DoorstepOfferNotification.evaluate(data, now.plusSeconds(30)))
    }
    @Test fun `closed offer silently clears the same notification`() {
        assertEquals(DoorstepOfferNotification.Expired("offer"), DoorstepOfferNotification.evaluate(data + ("type" to "doorstep.pro.offer.expired"), now))
    }
    @Test fun `invalid deadlines and missing IDs never ring`() {
        assertEquals(DoorstepOfferNotification.Invalid, DoorstepOfferNotification.evaluate(data - "expires_at", now))
        assertEquals(DoorstepOfferNotification.Invalid, DoorstepOfferNotification.evaluate(data - "entity_id", now))
        assertEquals(DoorstepOfferNotification.Invalid, DoorstepOfferNotification.evaluate(data + ("expires_at" to "bad"), now))
    }
    @Test fun `other apps pushes are unchanged`() {
        assertEquals(DoorstepOfferNotification.NotOffer, DoorstepOfferNotification.evaluate(mapOf("type" to "chat.message"), now))
    }

    @Test fun `all partner offers expire on their original deadline`() {
        for (type in listOf("captain.offer", "food_delivery_offer", "commerce.delivery.offer")) {
            val offer = data + mapOf("type" to type, "offer_id" to "offer")
            assertEquals(DoorstepOfferNotification.Open("offer", 10000), DoorstepOfferNotification.evaluate(offer, now.plusSeconds(20)))
            assertEquals(DoorstepOfferNotification.Expired("offer"), DoorstepOfferNotification.evaluate(offer, now.plusSeconds(31)))
            assertEquals(DoorstepOfferNotification.Invalid, DoorstepOfferNotification.evaluate(offer - "expires_at", now))
        }
    }
}
