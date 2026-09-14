package com.us.android.core.food.network

import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject

/**
 * The offer inside a `food.delivery.offered` realtime frame (food-service
 * delivery_dispatch.go).
 *
 *  - a single-order offer is the [DeliveryOfferDto] view itself, with
 *    `delivery_partner_user_id` beside its keys;
 *  - a BATCH offer is `{"offer": <view>, "batch": {...}, "is_batch": true,
 *    "delivery_partner_user_id": ...}` — the job detail sits inside `offer`,
 *    and the top level has no `id` of its own.
 *
 * The frame's keys are additive by contract, so this reads leniently; the
 * strict pin on the view's shape is the offers-list golden.
 */
object DeliveryOfferPayload {

    private val lenient = Json { ignoreUnknownKeys = true }

    /** The offer, or null when [data] is not an offer payload. Never throws. */
    fun offer(data: JsonElement): DeliveryOfferDto? {
        val frame = data as? JsonObject ?: return null
        val view = frame[KEY_OFFER] as? JsonObject ?: frame
        return try {
            lenient.decodeFromJsonElement(DeliveryOfferDto.serializer(), view)
        } catch (e: SerializationException) {
            null
        } catch (e: IllegalArgumentException) {
            null
        }
    }

    const val EVENT_TYPE = "food.delivery.offered"
    private const val KEY_OFFER = "offer"
}
