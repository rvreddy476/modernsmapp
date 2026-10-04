package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.ui.istDateOf
import com.us.android.feature.doorsteppro.ui.instantOrNull
import java.time.Duration
import java.time.Instant
import java.time.LocalDate

/** A booking's status as the professional sees it (BookingStatus). */
enum class JobStatus(val wire: String, val label: String) {
    PENDING_PAYMENT("pending_payment", "Awaiting payment"),
    CONFIRMED("confirmed", "Confirmed"),
    ASSIGNED("assigned", "Accepted"),
    EN_ROUTE("en_route", "On the way"),
    ARRIVED("arrived", "Arrived"),
    IN_PROGRESS("in_progress", "In progress"),
    AWAITING_EXTRAS_PAYMENT("awaiting_extras_payment", "Extras being paid"),
    COMPLETED("completed", "Completed"),
    CANCELLED("cancelled", "Cancelled"),
    EXPIRED("expired", "Expired"),
    CUSTOMER_NO_SHOW("customer_no_show", "Customer no-show"),
    PRO_NO_SHOW("pro_no_show", "Missed"),
    UNKNOWN("", "Unknown"),
    ;

    /** Holding the professional now: from acceptance until the visit ends. */
    val isActive: Boolean get() = this in ACTIVE

    companion object {
        private val ACTIVE = setOf(ASSIGNED, EN_ROUTE, ARRIVED, IN_PROGRESS, AWAITING_EXTRAS_PAYMENT)

        fun of(wire: String?): JobStatus = entries.firstOrNull { it.wire == wire && it != UNKNOWN } ?: UNKNOWN
    }
}

/** The visit's next step, as one primary action on the job screen. */
enum class VisitStep {
    /** assigned: start travelling. */
    GO_EN_ROUTE,

    /** en_route: tell the server you arrived (geo-checked). */
    MARK_ARRIVED,

    /** arrived: before photos (and the sealed kit for salon), then the customer's start code. */
    START_WITH_OTP,

    /** in_progress, work not yet declared done: propose extras, then "work done". */
    WORK,

    /** finished (in_progress after finish, or awaiting_extras_payment): after photos, then the end code. */
    COMPLETE_WITH_OTP,

    /** completed / customer no-show: rate the customer. */
    RATE,

    /** Nothing left to do (cancelled, expired, missed, unknown). */
    NONE,
}

/** Everything the job screen may offer, decided in one place. */
data class VisitActions(
    val step: VisitStep,
    val canNavigate: Boolean,
    val canChat: Boolean,
    val canProposeExtras: Boolean,
    /** Give the job back before the start code (counts against acceptance). */
    val canCancel: Boolean,
    /** "Unsafe, leaving" — no penalty, incident raised. */
    val canUnsafeExit: Boolean,
    val canSos: Boolean,
    /** Customer no-show: arrived and waited [VisitFlow.NO_SHOW_WAIT]. */
    val noShowAvailable: Boolean,
    /** Seconds until no-show becomes available, while arrived and still waiting; null otherwise. */
    val noShowInSeconds: Long?,
)

/**
 * The visit flow (x-doorstep-booking-states), professional side:
 *
 *     assigned → en_route → arrived → (before photos + start OTP) in_progress
 *       → [finish → awaiting_extras_payment] → (after photos + end OTP) completed
 *
 * plus the off-ramps the professional owns: customer no-show after waiting
 * 15 minutes from arrival (or the slot start, whichever is later), give the
 * job back before the start code, "unsafe, leaving" while travelling or in
 * the home, SOS throughout. Pure; the server decides every transition.
 *
 * [finished] is this device's memory of a successful `finish` — the contract's
 * ProJob carries no flag for it, and `finish` below the extras threshold
 * leaves the status at in_progress (see the report's contract gaps).
 */
object VisitFlow {

    val NO_SHOW_WAIT: Duration = Duration.ofMinutes(15)

    fun of(job: ProJobDto, now: Instant, arrivedAt: Instant? = null, finished: Boolean = false): VisitActions {
        val status = JobStatus.of(job.status)
        val step = when (status) {
            JobStatus.ASSIGNED -> VisitStep.GO_EN_ROUTE
            JobStatus.EN_ROUTE -> VisitStep.MARK_ARRIVED
            JobStatus.ARRIVED -> VisitStep.START_WITH_OTP
            JobStatus.IN_PROGRESS -> if (finished) VisitStep.COMPLETE_WITH_OTP else VisitStep.WORK
            JobStatus.AWAITING_EXTRAS_PAYMENT -> VisitStep.COMPLETE_WITH_OTP
            JobStatus.COMPLETED, JobStatus.CUSTOMER_NO_SHOW -> VisitStep.RATE
            else -> VisitStep.NONE
        }
        val noShowAt = if (status == JobStatus.ARRIVED) noShowAt(job, arrivedAt) else null
        val noShowIn = noShowAt?.let { Duration.between(now, it).seconds }
        return VisitActions(
            step = step,
            canNavigate = job.address != null && (status == JobStatus.ASSIGNED || status == JobStatus.EN_ROUTE),
            canChat = job.chatOpen,
            canProposeExtras = status == JobStatus.IN_PROGRESS && !finished,
            canCancel = status == JobStatus.ASSIGNED || status == JobStatus.EN_ROUTE || status == JobStatus.ARRIVED,
            canUnsafeExit = status == JobStatus.EN_ROUTE || status == JobStatus.ARRIVED || status == JobStatus.IN_PROGRESS,
            canSos = status.isActive,
            noShowAvailable = noShowIn != null && noShowIn <= 0,
            noShowInSeconds = noShowIn?.takeIf { it > 0 },
        )
    }

    /** When waiting is over: 15 minutes after the later of arrival and the slot start. Null when neither is known. */
    fun noShowAt(job: ProJobDto, arrivedAt: Instant?): Instant? {
        val slotStart = instantOrNull(job.slotStart)
        val from = listOfNotNull(arrivedAt, slotStart).maxOrNull() ?: return null
        return from.plus(NO_SHOW_WAIT)
    }
}

/** Jobs on one India-time calendar day, earliest first. Pure. */
object JobsForDate {
    fun on(jobs: List<ProJobDto>, date: LocalDate): List<ProJobDto> =
        jobs.filter { istDateOf(it.slotStart) == date }
            .distinctBy { it.bookingId }
            .sortedBy { instantOrNull(it.slotStart) ?: Instant.MAX }

    /** The days in [jobs] from [today] on, for the date strip's dots. */
    fun daysWithJobs(jobs: List<ProJobDto>): Set<LocalDate> = jobs.mapNotNull { istDateOf(it.slotStart) }.toSet()
}
