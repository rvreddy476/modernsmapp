package com.us.android.feature.doorstep.domain

import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.PaymentIntentDto
import com.us.android.feature.doorstep.data.ProChangeDto
import com.us.android.feature.doorstep.data.ProfessionalCardDto
import com.us.android.feature.doorstep.data.ProfessionalListDto
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.payment.DoorstepReference
import com.us.android.feature.doorstep.ui.instantOrNull
import java.time.Duration
import java.time.Instant
import java.util.Locale

/*
 * B1 (4 Oct 2026): professionals price their own services and the customer
 * picks one. Pure rules, so each is a plain unit test:
 *
 *  - [Units]: what an option's quantity counts (a job, hours, months);
 *  - [ProfessionalRules]: how the list reads (distance band only, rating,
 *    the times a card offers, ASAP with nobody → the scheduled alternatives);
 *  - [ProChangeRules]: the money of a change of professional (cheaper →
 *    refunded, dearer → a difference paid ONLY through the extras-bill path
 *    and confirmed only by the payment status) and the 30-minute choice
 *    deadline.
 */

/** `ServiceOption.unit` / `QuoteLine.unit` / `PriceLine.unit`. */
object Units {
    const val PER_JOB = "per_job"
    const val PER_HOUR = "per_hour"
    const val PER_MONTH = "per_month"

    /** "/hour" after a per-hour price; "" for a job (or a unit this build does not know). */
    fun priceSuffix(unit: String): String = when (unit) {
        PER_HOUR -> "/hour"
        PER_MONTH -> "/month"
        else -> ""
    }

    /** What the quantity stepper counts. */
    fun quantityLabel(unit: String, max: Int): String = when (unit) {
        PER_HOUR -> "Hours (up to $max)"
        PER_MONTH -> "Months (up to $max)"
        else -> "Quantity (up to $max)"
    }

    /** "Cook × 3 hours", "House help × 2 months", "Sofa cleaning × 2". */
    fun lineLabel(name: String, unit: String, quantity: Int): String = when {
        unit == PER_HOUR -> "$name × $quantity ${if (quantity == 1) "hour" else "hours"}"
        unit == PER_MONTH -> "$name × $quantity ${if (quantity == 1) "month" else "months"}"
        quantity > 1 -> "$name × $quantity"
        else -> name
    }
}

/** `sort` of the professionals list; the server orders, the client only asks. */
enum class ProfessionalSort(val wire: String, val label: String) {
    PRICE("price", "Price"),
    RATING("rating", "Rating"),
    SOONEST("soonest", "Soonest"),
}

/** Scheduled (a time from the professional's calendar) or as soon as possible (on duty nearby, with an ETA). */
enum class BookingMode { SCHEDULED, ASAP }

/** One way a card can be booked: a free start, or ASAP with the ETA. */
sealed interface ProfessionalPick {
    val proId: String

    data class At(override val proId: String, val slotStart: String, val slotEnd: String) : ProfessionalPick

    data class Asap(override val proId: String, val etaMinutes: Int?) : ProfessionalPick
}

/** What the list screen shows for one server answer. */
sealed interface ProfessionalListView {
    /** Professionals to pick from, in the server's order. */
    data class Cards(val mode: BookingMode, val cards: List<ProfessionalCardDto>) : ProfessionalListView

    /**
     * ASAP found nobody (founder, 4 Oct: "include schedule as well in case no
     * worker found"): the same selection's scheduled alternatives, picked as
     * scheduled times. The customer's choices are kept — the alternatives were
     * listed for the same service, options, add-ons and address.
     */
    data class AsapNobody(val alternatives: List<ProfessionalCardDto>) : ProfessionalListView

    /** Nobody prices this selection here, or nobody is free on the chosen day. */
    data class Empty(val mode: BookingMode) : ProfessionalListView
}

object ProfessionalRules {

    /** How a list answer reads. ASAP with `no_professional` NEVER shows an empty ASAP list. */
    fun view(list: ProfessionalListDto): ProfessionalListView {
        val mode = if (list.mode == MODE_ASAP) BookingMode.ASAP else BookingMode.SCHEDULED
        return when {
            mode == BookingMode.ASAP && (list.noProfessional || list.items.isEmpty()) ->
                if (list.scheduledAlternatives.isNotEmpty()) {
                    ProfessionalListView.AsapNobody(list.scheduledAlternatives)
                } else {
                    ProfessionalListView.Empty(BookingMode.ASAP)
                }
            list.items.isEmpty() -> ProfessionalListView.Empty(mode)
            else -> ProfessionalListView.Cards(mode, list.items)
        }
    }

    /**
     * The picks a card offers: each free start when scheduled; one ASAP pick
     * (with the ETA) when the list is ASAP. A scheduled card is never offered
     * as ASAP, and an ASAP card never offers a start.
     */
    fun picks(card: ProfessionalCardDto, mode: BookingMode): List<ProfessionalPick> = when (mode) {
        BookingMode.ASAP -> listOf(ProfessionalPick.Asap(card.proId, card.etaMinutes))
        BookingMode.SCHEDULED -> card.nextSlots.map { ProfessionalPick.At(card.proId, it.start, it.end) }
    }

    /** The distance BAND, in words. An unknown band shows nothing — never a number the server did not send. */
    fun distanceText(band: String): String? = when (band) {
        "under_2_km" -> "Under 2 km away"
        "2_to_5_km" -> "2–5 km away"
        "5_to_10_km" -> "5–10 km away"
        "over_10_km" -> "Over 10 km away"
        else -> null
    }

    /** "★ 4.7 (10)", or "New" before the first rating. */
    fun ratingText(card: ProfessionalCardDto): String =
        card.ratingAvg?.takeIf { card.ratingCount > 0 }?.let { String.format(Locale.ENGLISH, "★ %.1f (%d)", it, card.ratingCount) } ?: "New"

    /** "Arrives in about 15 min" for an ASAP card. */
    fun etaText(etaMinutes: Int?): String = etaMinutes?.let { "Arrives in about $it min" } ?: "Arrives as soon as possible"

    private const val MODE_ASAP = "asap"
}

/** What a change of professional does to money: never computed by the client, only read from the server. */
sealed interface ChangeMoney {
    data object Same : ChangeMoney

    /** The new professional is cheaper: the difference comes back. */
    data class Refund(val amount: Paise) : ChangeMoney

    /** The new professional is dearer: the difference is paid before they are confirmed. */
    data class Charge(val amount: Paise) : ChangeMoney

    companion object {
        /** From a card's or a change's `difference_paise` (new minus old); null when the server sent none. */
        fun of(differencePaise: Long?): ChangeMoney? = when {
            differencePaise == null -> null
            differencePaise < 0 -> Refund(Paise(-differencePaise))
            differencePaise > 0 -> Charge(Paise(differencePaise))
            else -> Same
        }
    }
}

/** What `POST /change-professional` answered, as the screen acts on it. */
sealed interface ChangeOutcome {
    /** Confirmed with the new professional at once; [refund] is coming back (zero when equal). */
    data class Applied(val proFirstName: String, val refund: Paise) : ChangeOutcome

    /** Dearer: held while the difference is paid through the extras-bill path. */
    data class AwaitingPayment(val change: ProChangeDto, val intent: PaymentIntentDto) : ChangeOutcome

    /** Anything else (abandoned, an intent that does not match the change): nothing is paid or shown confirmed. */
    data class NotUsable(val why: String) : ChangeOutcome
}

object ProChangeRules {

    const val STATUS_PENDING_PAYMENT = "pending_payment"
    const val STATUS_APPLIED = "applied"

    private val PAYABLE_INTENT = setOf("created", "pending")

    fun outcome(change: ProChangeDto): ChangeOutcome = when (change.status) {
        STATUS_APPLIED -> ChangeOutcome.Applied(change.proFirstName, Paise(change.refundPaise.coerceAtLeast(0)))
        STATUS_PENDING_PAYMENT -> payableIntent(change)?.let { ChangeOutcome.AwaitingPayment(change, it) }
            ?: ChangeOutcome.NotUsable("This change can't be paid now. Please pick again.")
        else -> ChangeOutcome.NotUsable("That choice lapsed. Please pick again.")
    }

    /**
     * The difference's intent, only when it is safe to open a sheet for it:
     * the change is waiting for payment, the difference is positive, the
     * intent is the pro_change bill's (`doorstep_extras`), its amount is
     * EXACTLY the server's difference, and it is not already settled or
     * failed. Anything else opens nothing.
     */
    fun payableIntent(change: ProChangeDto?): PaymentIntentDto? {
        val intent = change?.paymentIntent ?: return null
        return intent.takeIf {
            change.status == STATUS_PENDING_PAYMENT &&
                change.differencePaise > 0 &&
                it.referenceType == DoorstepReference.EXTRAS &&
                it.amountPaise == change.differencePaise &&
                it.status in PAYABLE_INTENT
        }
    }

    /** Whether the customer may still pick another professional: pro_unavailable and before the deadline. */
    fun choiceOpen(booking: BookingDto, now: Instant): Boolean {
        if (BookingStatus.of(booking.status) != BookingStatus.PRO_UNAVAILABLE) return false
        val deadline = instantOrNull(booking.choiceDeadline) ?: return false
        return now.isBefore(deadline)
    }

    /** Time left to choose, never negative; null without a deadline. */
    fun choiceRemaining(booking: BookingDto, now: Instant): Duration? =
        instantOrNull(booking.choiceDeadline)?.let { Duration.between(now, it).let { d -> if (d.isNegative) Duration.ZERO else d } }

    /** Why the professional is gone, in the customer's words. */
    fun causeText(cause: String?): String = when (cause) {
        "declined" -> "Your professional couldn't take this job."
        "offer_expired" -> "Your professional didn't respond in time."
        "pro_cancel" -> "Your professional had to give this job back."
        "not_on_duty" -> "Your professional isn't available at the time."
        "pro_no_show" -> "Your professional didn't arrive."
        "ops_redispatch" -> "We had to take this job off your professional."
        "no_professional" -> "Your professional can no longer take this job."
        else -> "Your professional can't make it."
    }
}
