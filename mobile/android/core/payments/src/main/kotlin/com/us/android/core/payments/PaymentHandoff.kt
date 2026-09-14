package com.us.android.core.payments

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Carries the payment sheet's ending from the Activity back to the screen that
 * asked for it — ONE bus for every product (moved here from `:core:commerce`,
 * 2026-09-15, so MStore and Feast share it).
 *
 * ## Why a bus and not a callback
 *
 * The provider SDK delivers its result to the **Activity**, not to whoever
 * opened the sheet, and a checkout ViewModel is scoped to a navigation
 * destination the application module cannot reach into. So the Activity side
 * publishes here and the screen collects. This also survives the case that
 * matters most: a process death while the buyer is inside the provider's
 * sheet. The screen re-subscribes on recreation, and if nothing arrives, the
 * order is still there and still shows its real state — because the server,
 * not this bus, is the record of what happened.
 *
 * ## What an event means
 *
 * [PaymentHandoffEvent.SheetClosed] means the SDK flow ENDED. It does not say
 * whether money moved; every ending is treated the same way: poll the server
 * through [PaymentCoordinator.confirm]. A1/R-3.
 *
 * [PaymentHandoffEvent.Unavailable] is different in kind: the sheet was never
 * presented, so no payment can have been taken.
 *
 * ## C3-LB-4 — why events carry an attempt, and are consumed once
 *
 * `replay = 1` exists for a real reason — the sheet closes and the Activity
 * resumes a frame later, and a screen that subscribes in that gap would
 * otherwise miss its own outcome. Two things make the replay safe:
 *
 *  1. every event names the exact [PaymentAttempt] it belongs to — the
 *     application, the reference AND an attempt id, because a buyer who
 *     retries the same order gets a second attempt;
 *  2. [consume] marks an attempt finished, so re-subscribing after a rotation
 *     cannot re-deliver an outcome that has already been acted on.
 *
 * ## One stream per application
 *
 * [events] is keyed by application id and each application has its OWN replay
 * slot. A Feast ending can never be replayed into MStore checkout, and a Feast
 * publish can never push MStore's buffered ending out of its replay slot. The
 * attempt check a collector makes (which includes the application) is the
 * second line of the same defence.
 */
@Singleton
class PaymentHandoff @Inject constructor() {

    private val streams = ConcurrentHashMap<String, MutableSharedFlow<PaymentHandoffEvent>>()

    /** Attempts whose outcome has already been acted on, across every application. */
    private val consumed = Collections.synchronizedSet(mutableSetOf<PaymentAttempt>())

    /**
     * [applicationId]'s endings, and nothing else.
     *
     * Collectors must still check that an event is for THEIR attempt — two
     * checkouts of one application can be on the back stack — and must check
     * [isConsumed] before acting on a replayed event.
     */
    fun events(applicationId: String): SharedFlow<PaymentHandoffEvent> =
        stream(PaymentApplication.requireValid(applicationId)).asSharedFlow()

    /** Publishes onto the stream of the application the event's attempt belongs to. */
    fun publish(event: PaymentHandoffEvent) {
        stream(event.applicationId).tryEmit(event)
    }

    /** Whether [attempt]'s outcome has already been acted on. */
    fun isConsumed(attempt: PaymentAttempt): Boolean = consumed.contains(attempt)

    /** Marks [attempt]'s outcome as acted on. Idempotent. */
    fun consume(attempt: PaymentAttempt) {
        consumed.add(attempt)
    }

    private fun stream(applicationId: String): MutableSharedFlow<PaymentHandoffEvent> =
        streams.getOrPut(applicationId) {
            // Replay one so a screen that subscribes a frame after the event was
            // published does not miss it. Safe: attempt-scoped, consumed once.
            MutableSharedFlow(replay = 1, extraBufferCapacity = 4)
        }
}

/** How a payment-sheet opening ended, as the screen that requested it hears it. */
sealed interface PaymentHandoffEvent {
    val attempt: PaymentAttempt
    val applicationId: String get() = attempt.applicationId
    val referenceId: String get() = attempt.referenceId

    /** The provider sheet ended, however it ended. Poll the server; conclude nothing. */
    data class SheetClosed(override val attempt: PaymentAttempt) : PaymentHandoffEvent

    /** The sheet could not be opened. Nothing was presented, so nothing was charged. */
    data class Unavailable(
        override val attempt: PaymentAttempt,
        val reason: String,
    ) : PaymentHandoffEvent
}

/**
 * A sheet result as a handoff event, for the same attempt. A closed sheet of
 * any kind is [PaymentHandoffEvent.SheetClosed]; only a sheet that never opened
 * is [PaymentHandoffEvent.Unavailable]. There is no "paid" in either type.
 */
fun PaymentSheetResult.toHandoffEvent(): PaymentHandoffEvent = when (this) {
    is PaymentSheetResult.Closed -> PaymentHandoffEvent.SheetClosed(attempt)
    is PaymentSheetResult.Unavailable -> PaymentHandoffEvent.Unavailable(attempt, reason)
}
