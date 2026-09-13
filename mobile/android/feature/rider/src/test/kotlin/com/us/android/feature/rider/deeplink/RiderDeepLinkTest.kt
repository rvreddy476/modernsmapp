package com.us.android.feature.rider.deeplink

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test

class RiderDeepLinkTest {

    @Test
    fun `the offer push path and its full-URL form open the offer`() {
        assertThat(RiderDeepLinks.parse("/rider/offers/0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0031"))
            .isEqualTo(RiderDeepLink.Offer("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0031"))
        assertThat(RiderDeepLinks.parse("https://rider.feast.example/rider/offers/abc-1/"))
            .isEqualTo(RiderDeepLink.Offer("abc-1"))
    }

    @Test
    fun `anything else in the path is refused`() {
        for (bad in listOf("/rider/offers/", "/rider/offers/../admin", "/rider/offers/a/b", "/kitchen/orders/1", "", null)) {
            assertWithMessage(bad.toString()).that(RiderDeepLinks.parse(bad)).isNull()
        }
    }

    @Test
    fun `a food_delivery_offer push opens its offer`() {
        val data = mapOf(
            "type" to "food_delivery_offer",
            "order_id" to "o-1",
            "offer_id" to "offer-9",
            "expires_at" to "2026-09-13T06:35:00Z",
            "deep_link" to "/rider/offers/offer-9",
            "title" to "New delivery job",
            "body" to "Pickup 1.2 km away",
        )
        assertThat(RiderDeepLinks.fromPush(data)).isEqualTo(RiderDeepLink.Offer("offer-9"))
        assertThat(RiderDeepLinks.fromPush(data - "offer_id")).isEqualTo(RiderDeepLink.Offer("offer-9"))
        assertThat(RiderDeepLinks.fromPush(data + ("type" to "food_order_new"))).isNull()
    }

    @Test
    fun `a DigiLocker return is routed to verification`() {
        val link = RiderDeepLinks.parse("feastrider://digilocker/return?code=c1&state=s1")
        assertThat(link).isInstanceOf(RiderDeepLink.DigiLocker::class.java)
        assertThat((link as RiderDeepLink.DigiLocker).link.state).isEqualTo("s1")
    }
}
