package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.DeliveryOfferPayload
import com.us.android.core.food.network.DeliveryOffersDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

/**
 * The rider's job detail (food-service 610a2acd) beyond the goldens: the
 * realtime offer frame in both its shapes, and the optional keys whose absence
 * must still decode STRICTLY.
 */
class RiderJobDetailContractTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private fun golden(name: String): JsonObject =
        strict.parseToJsonElement(File("src/test/resources/contracts/$name").readText()).jsonObject

    /** The offer view from the offers-list golden — the exact shape the frames carry. */
    private fun goldenOfferView(): JsonObject {
        val data = golden("delivery_offers_me_get_200.json").getValue("data").jsonObject
        return (data.getValue("offers") as kotlinx.serialization.json.JsonArray).single().jsonObject
    }

    @Test
    fun `a single-order realtime frame is the offer view with the rider's user id beside it`() {
        val frame = JsonObject(goldenOfferView() + ("delivery_partner_user_id" to JsonPrimitive("u-1")))

        val offer = checkNotNull(DeliveryOfferPayload.offer(frame))

        assertThat(offer.id).isEqualTo("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0025")
        assertThat(offer.restaurant?.name).isEqualTo("Test Kitchen")
        assertThat(offer.payoutPaise).isEqualTo(Paise(2_320))
    }

    @Test
    fun `a batch realtime frame is read from its offer key, not the top level`() {
        val batch = strict.parseToJsonElement(
            """{"id":"b-1","restaurant_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002","status":"PENDING",""" +
                """"created_at":"2026-09-13 06:30:00+00","members":[{"order_id":"o-1","sequence":1},{"order_id":"o-2","sequence":2}]}""",
        )
        val frame = JsonObject(
            mapOf(
                "offer" to goldenOfferView(),
                "batch" to batch,
                "is_batch" to JsonPrimitive(true),
                "delivery_partner_user_id" to JsonPrimitive("u-1"),
            ),
        )

        val offer = checkNotNull(DeliveryOfferPayload.offer(frame)) { "the batch frame's offer did not decode" }

        assertThat(offer.id).isEqualTo("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0025")
        assertThat(offer.restaurant?.name).isEqualTo("Test Kitchen")
        assertThat(offer.dropArea?.locality).isEqualTo("Bengaluru")
        assertThat(offer.tripDistanceMeters).isEqualTo(5_065L)
        assertThat(offer.payoutPaise).isEqualTo(Paise(2_320))
    }

    @Test
    fun `a frame that is not an offer reads as null rather than throwing`() {
        assertThat(DeliveryOfferPayload.offer(JsonPrimitive("nope"))).isNull()
        assertThat(DeliveryOfferPayload.offer(JsonObject(mapOf("order_id" to JsonPrimitive("o-1"))))).isNull()
    }

    @Test
    fun `a bare offer without any job detail still decodes strictly`() {
        val bare = JsonObject(goldenOfferView() - DETAIL_KEYS)

        val offer = strict.decodeFromJsonElement(DeliveryOfferDto.serializer(), bare)

        assertThat(offer.restaurant).isNull()
        assertThat(offer.dropArea).isNull()
        assertThat(offer.distanceToRestaurantMeters).isNull()
        assertThat(offer.tripDistanceMeters).isNull()
        assertThat(offer.payoutPaise).isNull()
        assertThat(strict.decodeFromString(ApiEnvelope.serializer(DeliveryOffersDto.serializer()), """{"data":{"offers":[]}}""").data?.offers)
            .isEmpty()
    }

    @Test
    fun `an assignment without drop, summary, navigation, coordinates or phone decodes strictly`() {
        val accepted = golden("delivery_assignment_current_get_200_accepted.json").getValue("data").jsonObject
        val restaurant = accepted.getValue("restaurant").jsonObject - setOf("latitude", "longitude", "phone")
        val navigation = accepted.getValue("navigation").jsonObject - "drop_url"
        val stripped: Map<String, JsonElement> =
            accepted - setOf("drop", "drop_summary", "eta_at", "eta_source") +
                ("restaurant" to JsonObject(restaurant)) + ("navigation" to JsonObject(navigation))

        val assignment = strict.decodeFromJsonElement(DeliveryAssignmentDto.serializer(), JsonObject(stripped))

        assertThat(assignment.drop).isNull()
        assertThat(assignment.dropSummary).isNull()
        assertThat(assignment.navigation?.dropUrl).isNull()
        assertThat(assignment.restaurant?.latitude).isNull()
        assertThat(assignment.restaurant?.phone).isNull()

        val noNavigation = strict.decodeFromJsonElement(DeliveryAssignmentDto.serializer(), JsonObject(stripped - "navigation"))
        assertThat(noNavigation.navigation).isNull()

        val dropWithoutPin = accepted.getValue("drop").jsonObject - setOf("latitude", "longitude")
        val pinless = strict.decodeFromJsonElement(DeliveryAssignmentDto.serializer(), JsonObject(accepted + ("drop" to JsonObject(dropWithoutPin))))
        assertThat(pinless.drop?.latitude).isNull()
        assertThat(pinless.drop?.addressLine1).isEqualTo("42 Lake View Road")
    }

    private companion object {
        val DETAIL_KEYS = setOf("restaurant", "drop_area", "distance_to_restaurant_meters", "trip_distance_meters", "payout_paise", "currency")
    }
}
