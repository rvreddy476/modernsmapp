package com.us.android.feature.rider.job

import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.feature.rider.money.RiderMoney
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

/**
 * Where the customer's order goes, as the rider may see it. Exists only while
 * the assignment carries `drop`. Never a phone: food-service sends none.
 */
data class CustomerDrop(
    val firstName: String?,
    val addressLines: List<String>,
    val landmark: String?,
    val instructions: String?,
)

/** What the active-job screen shows beyond its step buttons. Pure. */
data class JobDetails(
    val restaurantName: String,
    val restaurantAddressLines: List<String>,
    /** The restaurant's business number, only when the server sends one. */
    val restaurantPhone: String?,
    /** From `payout_paise`, never the float. */
    val pay: Paise?,
    val etaAt: Instant?,
    /** Null whenever the assignment has no `drop`: nothing about the customer is shown. */
    val customer: CustomerDrop?,
    val navigation: JobNavigation,
) {
    val canCallRestaurant: Boolean get() = restaurantPhone != null

    /** `12:22 PM` in [zone], or null with no ETA. */
    fun etaText(zone: ZoneId): String? = etaAt?.let { ETA_FORMAT.withZone(zone).format(it) }

    companion object {
        fun of(assignment: DeliveryAssignmentDto): JobDetails {
            val active = JobActions.of(assignment).isActive
            val restaurant = assignment.restaurant
            val drop = assignment.drop?.takeIf { active }
            return JobDetails(
                restaurantName = restaurant?.name.clean() ?: assignment.restaurantName,
                restaurantAddressLines = listOfNotNull(restaurant?.addressLine1.clean(), restaurant?.addressLine2.clean(), restaurant?.city.clean()),
                restaurantPhone = dialable(restaurant?.phone),
                pay = RiderMoney.jobPay(assignment),
                etaAt = assignment.etaAt?.let(::parseInstant),
                customer = drop?.let {
                    CustomerDrop(
                        firstName = it.customerFirstName.clean(),
                        addressLines = listOfNotNull(
                            it.addressLine1.clean(),
                            it.addressLine2.clean(),
                            listOfNotNull(it.city.clean(), it.postalCode.clean()).joinToString(" ").clean(),
                        ),
                        landmark = it.landmark.clean(),
                        instructions = it.deliveryInstructions.clean(),
                    )
                },
                navigation = JobLocations.of(assignment),
            )
        }

        /** Digits and a leading `+` only; null when fewer than three digits remain. */
        internal fun dialable(phone: String?): String? {
            val trimmed = phone?.trim().orEmpty()
            val digits = trimmed.filter(Char::isDigit)
            if (digits.length < MIN_PHONE_DIGITS) return null
            return if (trimmed.startsWith("+")) "+$digits" else digits
        }

        private fun parseInstant(text: String): Instant? = try {
            Instant.parse(text)
        } catch (e: DateTimeParseException) {
            null
        }

        private fun String?.clean(): String? = this?.trim()?.takeIf { it.isNotEmpty() }

        private const val MIN_PHONE_DIGITS = 3
        private val ETA_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("h:mm a", Locale.ENGLISH)
    }
}
