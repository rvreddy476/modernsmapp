package com.us.android.feature.rider.offers

import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.network.DeliveryOfferPayload
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.feature.rider.money.RiderMoney
import java.util.Locale

/**
 * What an offer card shows, from the offer alone. Pure, and the same for every
 * way an offer arrives: the offers list (Home, the push deep link) and a
 * realtime frame, single or batch.
 *
 * It shows the restaurant's name and city, the drop LOCALITY, the trip and the
 * distance to the restaurant, and the pay. Never an address: the offer carries
 * no drop-off address, and the restaurant's street waits for the job.
 */
data class OfferSummary(
    val restaurantName: String?,
    val restaurantArea: String?,
    val dropLocality: String?,
    /** Restaurant to drop, e.g. `5.1 km`. */
    val tripDistance: String?,
    /** The rider to the restaurant, e.g. `1.6 km`. */
    val toRestaurant: String?,
    /** `₹23.20`, from `payout_paise`. */
    val pay: String?,
) {
    val title: String get() = restaurantName ?: "Delivery job"

    /** Every value the card can put on screen. */
    val lines: List<String> get() = listOfNotNull(restaurantName, restaurantArea, dropLocality, tripDistance, toRestaurant, pay)

    companion object {
        fun of(offer: DeliveryOfferDto): OfferSummary = OfferSummary(
            restaurantName = offer.restaurant?.name.clean(),
            restaurantArea = offer.restaurant?.city.clean(),
            dropLocality = offer.dropArea?.locality.clean(),
            tripDistance = offer.tripDistanceMeters?.let(DistanceText::fromMeters),
            toRestaurant = offer.distanceToRestaurantMeters?.let(DistanceText::fromMeters)
                ?: offer.distanceKm?.let(DistanceText::fromKilometres),
            pay = RiderMoney.offerPay(offer)?.let(RiderMoney::text),
        )

        private fun String?.clean(): String? = this?.trim()?.takeIf { it.isNotEmpty() }
    }
}

/** Kilometres with one decimal, locale-independent. */
object DistanceText {
    /** Integer arithmetic, half up: 5065 m is `5.1 km`, 1553 m is `1.6 km`. */
    fun fromMeters(meters: Long): String {
        val tenths = (meters.coerceAtLeast(0) + HALF_TENTH) / METERS_PER_TENTH
        return "${tenths / TENTHS}.${tenths % TENTHS} km"
    }

    fun fromKilometres(kilometres: Double): String = String.format(Locale.US, "%.1f km", kilometres)

    private const val HALF_TENTH = 50L
    private const val METERS_PER_TENTH = 100L
    private const val TENTHS = 10L
}

/** Folds a realtime `food.delivery.offered` frame into the offers on screen. */
object LiveOffers {
    /** [current] with the frame's offer added or replaced; unchanged for any other frame. */
    fun upsert(current: List<DeliveryOfferDto>, message: RealtimeEvent.Message): List<DeliveryOfferDto> {
        if (message.eventType != DeliveryOfferPayload.EVENT_TYPE) return current
        val offer = DeliveryOfferPayload.offer(message.data) ?: return current
        val index = current.indexOfFirst { it.id == offer.id }
        return if (index < 0) current + offer else current.toMutableList().also { it[index] = offer }
    }
}
