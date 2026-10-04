package com.us.android.feature.doorstep.domain

import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.BookingPhotoDto
import com.us.android.feature.doorstep.data.ExtraDto
import com.us.android.feature.doorstep.data.ExtrasBillDto
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.sum
import com.us.android.feature.doorstep.ui.instantOrNull
import java.time.Duration
import java.time.Instant

/** The booking states of `x-doorstep-booking-states`, with what the customer reads. */
enum class BookingStatus(val wire: String, val label: String, val terminal: Boolean) {
    PENDING_PAYMENT("pending_payment", "Awaiting payment", terminal = false),
    CONFIRMED("confirmed", "Confirmed", terminal = false),
    ASSIGNED("assigned", "Professional assigned", terminal = false),
    EN_ROUTE("en_route", "On the way", terminal = false),
    ARRIVED("arrived", "Arrived", terminal = false),
    IN_PROGRESS("in_progress", "In progress", terminal = false),
    AWAITING_EXTRAS_PAYMENT("awaiting_extras_payment", "Extras to pay", terminal = false),
    COMPLETED("completed", "Completed", terminal = true),
    CANCELLED("cancelled", "Cancelled", terminal = true),
    EXPIRED("expired", "Expired", terminal = true),
    CUSTOMER_NO_SHOW("customer_no_show", "Missed visit", terminal = true),
    PRO_NO_SHOW("pro_no_show", "Professional didn't arrive", terminal = true),

    /** B1: the picked professional is gone; the customer picks another (or cancels) before the deadline. Live: it moves on its own. */
    PRO_UNAVAILABLE("pro_unavailable", "Pick another professional", terminal = false),

    /** A status this build does not know yet: shown neutrally, kept live so the screen keeps asking. */
    UNKNOWN("", "Updating", terminal = false),
    ;

    companion object {
        fun of(wire: String?): BookingStatus = entries.firstOrNull { it.wire.isNotEmpty() && it.wire == wire } ?: UNKNOWN
    }
}

/** One step of the tracking timeline; [at] is when the booking reached it (RFC 3339), from `status_history`. */
data class TimelineStep(val label: String, val state: StepState, val at: String? = null)

const val PHOTO_BEFORE = "before"
const val PHOTO_AFTER = "after"

enum class StepState { DONE, CURRENT, UPCOMING }

object BookingRules {

    /**
     * The start OTP is shown ONLY while the professional may ask for it —
     * from acceptance until the job starts (assigned, en route, arrived). The
     * server sends it in that window too; this is the second line, so a code
     * the server ever leaks outside it (a cached read, a cancelled booking)
     * never reaches the screen, and nobody can read it off a finished job.
     */
    private val OTP_VISIBLE = setOf(BookingStatus.ASSIGNED, BookingStatus.EN_ROUTE, BookingStatus.ARRIVED)

    fun visibleStartOtp(booking: BookingDto): String? =
        booking.startOtp?.trim()?.takeIf { it.isNotEmpty() && BookingStatus.of(booking.status) in OTP_VISIBLE }

    /**
     * The finish OTP is shown ONLY while the job is in progress — the server
     * sets it then, once the after photos are in. Any other status (extras to
     * pay, completed, cancelled, a status this build does not know) hides it,
     * whatever the server sent.
     */
    fun visibleEndOtp(booking: BookingDto): String? =
        booking.endOtp?.trim()?.takeIf { it.isNotEmpty() && BookingStatus.of(booking.status) in setOf(BookingStatus.IN_PROGRESS, BookingStatus.AWAITING_EXTRAS_PAYMENT) }

    /** The phases a customer is shown, in the order shown. */
    val CUSTOMER_PHOTO_PHASES = listOf(PHOTO_BEFORE, PHOTO_AFTER)

    /**
     * The visit photos grouped Before then After, oldest first in each; any
     * other phase is dropped (the server sends the customer none, this is the
     * second line). Phases with no photo are left out.
     */
    fun visitPhotos(booking: BookingDto): List<Pair<String, List<BookingPhotoDto>>> =
        CUSTOMER_PHOTO_PHASES.mapNotNull { phase ->
            booking.photos.filter { it.phase == phase }
                .sortedBy { instantOrNull(it.createdAt) ?: Instant.MAX }
                .takeIf { it.isNotEmpty() }
                ?.let { phase to it }
        }

    /**
     * The timeline, drawn from the server's `status_history`.
     *
     * On the happy path (and pending payment / extras to pay) the six steps
     * show done, current and upcoming, and each step the booking has reached
     * carries WHEN it got there, from its latest entry in the history. An
     * off-ramp (cancelled, expired, no-shows) or a status this build does not
     * know shows the history itself — every step it took, oldest first —
     * since there is no path ahead to draw.
     */
    fun timeline(booking: BookingDto): List<TimelineStep> {
        val history = booking.statusHistory
            .withIndex()
            .sortedWith(compareBy({ instantOrNull(it.value.createdAt) ?: Instant.MAX }, { it.index }))
            .map { it.value }
        val status = BookingStatus.of(booking.status)
        val path = timeline(status)
        if (path == null) {
            return history.map { step ->
                TimelineStep(BookingStatus.of(step.toStatus).historyLabel(step.toStatus), StepState.DONE, step.createdAt)
            }
        }
        val reachedAt = history.associate { it.toStatus to it.createdAt } // the latest entry wins
        return path.mapIndexed { i, step ->
            val at = if (step.state == StepState.UPCOMING) null else reachedAt[HAPPY_PATH[i].first.wire]
            step.copy(at = at)
        }
    }

    private fun BookingStatus.historyLabel(wire: String): String =
        if (this == BookingStatus.UNKNOWN) wire.replace('_', ' ').replaceFirstChar { it.uppercase() } else label

    private val HAPPY_PATH = listOf(
        BookingStatus.CONFIRMED to "Booked",
        BookingStatus.ASSIGNED to "Professional assigned",
        BookingStatus.EN_ROUTE to "On the way",
        BookingStatus.ARRIVED to "Arrived",
        BookingStatus.IN_PROGRESS to "Job started",
        BookingStatus.COMPLETED to "Completed",
    )

    /**
     * The timeline for the happy path. Awaiting-extras sits on "Job started";
     * a pending payment sits before "Booked". Terminal off-ramps (cancelled,
     * expired, no-shows) return null: the screen says what happened instead.
     */
    fun timeline(status: BookingStatus): List<TimelineStep>? {
        val index = when (status) {
            BookingStatus.PENDING_PAYMENT -> -1
            BookingStatus.AWAITING_EXTRAS_PAYMENT -> HAPPY_PATH.indexOfFirst { it.first == BookingStatus.IN_PROGRESS }
            BookingStatus.UNKNOWN -> return null
            else -> HAPPY_PATH.indexOfFirst { it.first == status }.takeIf { it >= 0 } ?: return null
        }
        return HAPPY_PATH.mapIndexed { i, (s, label) ->
            val state = when {
                i < index || (s == BookingStatus.COMPLETED && status == BookingStatus.COMPLETED) -> StepState.DONE
                i == index -> StepState.CURRENT
                else -> StepState.UPCOMING
            }
            TimelineStep(label, state)
        }
    }

    /** Whether the screen should stay subscribed and polling. */
    fun isLive(status: BookingStatus): Boolean = !status.terminal

    /** SOS and the share link exist while someone is coming or in the home. */
    fun safetyAvailable(status: BookingStatus): Boolean = status in setOf(
        BookingStatus.ASSIGNED,
        BookingStatus.EN_ROUTE,
        BookingStatus.ARRIVED,
        BookingStatus.IN_PROGRESS,
        BookingStatus.AWAITING_EXTRAS_PAYMENT,
    )

    /** Extras can be proposed once the job has started. */
    fun extrasVisible(status: BookingStatus): Boolean = status in setOf(
        BookingStatus.IN_PROGRESS,
        BookingStatus.AWAITING_EXTRAS_PAYMENT,
        BookingStatus.COMPLETED,
    )

    /** Rating and rework are offered after completion; the server enforces each window. */
    fun afterCareAvailable(booking: BookingDto): Boolean = BookingStatus.of(booking.status) == BookingStatus.COMPLETED

    /** The pending-payment hold left, never negative. */
    fun holdRemaining(holdExpiresAt: Instant?, now: Instant): Duration? =
        holdExpiresAt?.let { Duration.between(now, it).let { d -> if (d.isNegative) Duration.ZERO else d } }

    /** "9:05" for a hold countdown. */
    fun countdownText(remaining: Duration): String {
        val seconds = remaining.seconds.coerceAtLeast(0)
        return "${seconds / SECONDS_PER_MINUTE}:${(seconds % SECONDS_PER_MINUTE).toString().padStart(2, '0')}"
    }

    private const val SECONDS_PER_MINUTE = 60
}

/**
 * The extras on a booking, summed from the server's own per-extra
 * `total_paise` — the client never multiplies a unit price for money it
 * shows as owed.
 */
data class ExtrasSummary(
    /** Waiting for the customer to approve or decline. */
    val proposed: List<ExtraDto>,
    /** Approved or already on a bill: what the customer has agreed to pay. */
    val agreedTotal: Paise,
    /** What approving every proposed extra would add. */
    val proposedTotal: Paise,
) {
    companion object {
        private val AGREED = setOf("approved", "billed")

        fun of(extras: List<ExtraDto>): ExtrasSummary = ExtrasSummary(
            proposed = extras.filter { it.status == "proposed" },
            agreedTotal = extras.filter { it.status in AGREED }.map { Paise(it.totalPaise) }.sum(),
            proposedTotal = extras.filter { it.status == "proposed" }.map { Paise(it.totalPaise) }.sum(),
        )
    }
}

object BillRules {
    private val PAYABLE = setOf("open", "payment_pending", "outstanding")

    /** A bill the customer can (and should) pay now. */
    fun payable(bill: ExtrasBillDto?): Boolean = bill != null && bill.status in PAYABLE && bill.amountPaise > 0
}

/**
 * Unpaid extras block new bookings (DOORSTEP_OUTSTANDING_DUE). The catalogue
 * shows the block before the customer builds a basket the server will refuse.
 */
object OutstandingRules {
    fun blocksBooking(outstanding: OutstandingDto?): Boolean =
        outstanding != null && (outstanding.totalPaise > 0 || outstanding.bills.any { BillRules.payable(it) })
}
