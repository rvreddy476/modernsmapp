package com.us.android.feature.rider.offers

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.DeliveryOffersDto
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.feature.rider.RiderFixtures
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import org.junit.Test

class OfferSummaryTest {

    private val offer: DeliveryOfferDto =
        checkNotNull(RiderFixtures.success("delivery_offers_me_get_200.json", DeliveryOffersDto.serializer()).value.offers).single()

    @Test
    fun `the offer card shows the restaurant, drop locality, trip, distance and pay from paise`() {
        val summary = OfferSummary.of(offer)

        assertThat(summary.title).isEqualTo("Test Kitchen")
        assertThat(summary.restaurantArea).isEqualTo("Bengaluru")
        assertThat(summary.dropLocality).isEqualTo("Bengaluru")
        assertThat(summary.tripDistance).isEqualTo("5.1 km")
        assertThat(summary.toRestaurant).isEqualTo("1.6 km")
        assertThat(summary.pay).isEqualTo("₹23.20")
    }

    @Test
    fun `pay is read from payout_paise`() {
        assertThat(OfferSummary.of(offer.copy(payoutPaise = Paise(9_999))).pay).isEqualTo("₹99.99")
        assertThat(OfferSummary.of(offer.copy(payoutPaise = null)).pay).isNull()
    }

    @Test
    fun `the offer card never shows an exact address or a pin`() {
        val shown = OfferSummary.of(offer).lines.joinToString("\n")

        for (banned in listOf("1 Test Lane", "12.98", "77.64", "12.9716", "77.5946")) {
            assertThat(shown).doesNotContain(banned)
        }
    }

    @Test
    fun `a bare offer still makes a card, falling back to the dispatch distance`() {
        val bare = DeliveryOfferDto(id = "o-1", distanceKm = 1.25)
        val summary = OfferSummary.of(bare)

        assertThat(summary.title).isEqualTo("Delivery job")
        assertThat(summary.toRestaurant).isEqualTo("1.3 km")
        assertThat(summary.pay).isNull()
        assertThat(summary.dropLocality).isNull()
        assertThat(summary.tripDistance).isNull()
    }

    @Test
    fun `kilometres round half up from whole metres`() {
        assertThat(DistanceText.fromMeters(0)).isEqualTo("0.0 km")
        assertThat(DistanceText.fromMeters(49)).isEqualTo("0.0 km")
        assertThat(DistanceText.fromMeters(50)).isEqualTo("0.1 km")
        assertThat(DistanceText.fromMeters(12_345)).isEqualTo("12.3 km")
    }

    @Test
    fun `a single and a batch realtime offer make the same card as the offers list`() {
        val view = goldenOfferView()
        val single = JsonObject(view + ("delivery_partner_user_id" to JsonPrimitive("u-1")))
        val batch = JsonObject(
            mapOf(
                "offer" to view,
                "batch" to JsonObject(mapOf("id" to JsonPrimitive("b-1"), "status" to JsonPrimitive("PENDING"))),
                "is_batch" to JsonPrimitive(true),
                "delivery_partner_user_id" to JsonPrimitive("u-1"),
            ),
        )
        val expected = OfferSummary.of(offer)

        for (frame in listOf(single, batch)) {
            val offers = LiveOffers.upsert(emptyList(), message(frame))
            assertThat(offers.map { it.id }).containsExactly(offer.id)
            assertThat(OfferSummary.of(offers.single())).isEqualTo(expected)
        }
    }

    @Test
    fun `a live offer replaces its stale copy, and other frames change nothing`() {
        val stale = offer.copy(payoutPaise = null, restaurant = null)
        val updated = LiveOffers.upsert(listOf(stale), message(goldenOfferView()))
        assertThat(updated).containsExactly(offer)

        val other = RealtimeEvent.Message(id = "1", topic = "t", eventType = "food.delivery.assigned", data = goldenOfferView(), emittedAt = null)
        assertThat(LiveOffers.upsert(listOf(stale), other)).containsExactly(stale)
    }

    private fun message(data: JsonObject) =
        RealtimeEvent.Message(id = "1", topic = "food.delivery_partner.u-1.assignments", eventType = "food.delivery.offered", data = data, emittedAt = null)

    private fun goldenOfferView(): JsonObject {
        val root = Json.parseToJsonElement(RiderFixtures.raw("delivery_offers_me_get_200.json")).jsonObject
        return (root.getValue("data").jsonObject.getValue("offers") as JsonArray).single().jsonObject
    }
}
