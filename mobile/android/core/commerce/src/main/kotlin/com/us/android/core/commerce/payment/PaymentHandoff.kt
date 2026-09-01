package com.us.android.core.commerce.payment

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import java.util.Collections
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Carries the PSP sheet's outcome back into the checkout flow.
 *
 * ## Why a bus and not a callback
 *
 * The provider SDK delivers its result to the **Activity**, not to whoever
 * opened the sheet, and the checkout ViewModel is scoped to a navigation
 * destination that `:app` cannot reach into. So `:app` publishes here and the
 * checkout screen collects. This also survives the case that matters most: a
 * process death while the buyer is inside the provider's sheet. The screen
 * re-subscribes on recreation, and if nothing arrives, the order is still
 * there and still shows its real state — because the server, not this bus, is
 * the record of what happened.
 *
 * ## What an event means
 *
 * [PaymentHandoffEvent.SheetClosed] means the SDK flow ENDED. It does not say
 * whether money moved, and the checkout flow deliberately treats every ending
 * the same way: it polls the server. A1/R-3 — an order becomes paid only on a
 * signature-verified provider webhook, so a client-side "success" and a
 * client-side "failure" are both merely hints about where to look.
 *
 * [PaymentHandoffEvent.Unavailable] is different in kind: the sheet was never
 * presented, so no payment can have been taken and the app can say so plainly
 * rather than making the buyer wait through a poll that will never resolve.
 *
 * ## C3-LB-4 — why events carry an attempt, and are consumed once
 *
 * Review 3 (C-LB-1) found this bus replaying an event to whoever subscribed
 * next, with no way for a subscriber to tell whether the event was *theirs*.
 * `replay = 1` exists for a real reason — the sheet closes and the Activity
 * resumes a frame later, and a screen that subscribes in that gap would
 * otherwise miss its own outcome — but combined with a ViewModel that checked
 * only its own state TYPE, it meant a delayed or replayed event for order A
 * could be applied to order B: B's screen would start polling A, and render
 * A's terminal state as B's result.
 *
 * Two changes make the replay safe rather than removing it:
 *
 *  1. every event names the exact [PaymentAttempt] it belongs to — an order
 *     id AND an attempt id, because a buyer who retries the same order gets a
 *     second attempt and the first one's late callback must not settle it;
 *  2. [consume] marks an attempt finished, and the buffered event is then
 *     filtered out for good, so re-subscribing after a rotation cannot
 *     re-deliver an outcome that has already been acted on.
 *
 * The alternative — `replay = 0` plus server polling on every screen entry —
 * loses the resume race and makes the common path slower for a case the
 * attempt token already solves.
 */
@Singleton
class PaymentHandoff @Inject constructor() {

    private val _events = MutableSharedFlow<PaymentHandoffEvent>(
        // Replay one so a screen that subscribes a frame after the event was
        // published — which is exactly what happens when the sheet closes and
        // the Activity resumes — does not miss it. Safe because every event
        // is attempt-scoped and consumed exactly once; see the class note.
        replay = 1,
        extraBufferCapacity = 4,
    )

    /**
     * Attempts whose outcome has already been acted on.
     *
     * A set rather than a single value: two checkouts can legitimately be in
     * flight across a navigation stack, and "the last one" is not the same as
     * "this one". Synchronised because [publish] and [consume] are called
     * from the main thread while collection happens on a ViewModel scope.
     */
    private val consumed = Collections.synchronizedSet(mutableSetOf<String>())

    /**
     * Outcomes, filtered to those not yet consumed.
     *
     * Collectors must still check that an event is for THEIR attempt —
     * filtering here would need a per-collector view, and the check belongs
     * next to the state it is being applied to.
     */
    val events: SharedFlow<PaymentHandoffEvent> = _events.asSharedFlow()

    fun publish(event: PaymentHandoffEvent) {
        _events.tryEmit(event)
    }

    /**
     * Reports whether [attempt] has already been acted on.
     *
     * Collectors call this before applying a replayed event. It is the guard
     * that stops a rotation from re-delivering an outcome the buyer has
     * already moved past.
     */
    fun isConsumed(attempt: PaymentAttempt): Boolean = consumed.contains(attempt.key)

    /** Marks [attempt]'s outcome as acted on. Idempotent. */
    fun consume(attempt: PaymentAttempt) {
        consumed.add(attempt.key)
    }
}

/**
 * One payment-sheet opening, for one order.
 *
 * C3-LB-4. The order id alone is not enough: a buyer whose first attempt
 * failed can retry the SAME order, and the first attempt's late callback must
 * not be applied to the second. [id] is minted per opening.
 */
data class PaymentAttempt(
    val orderId: String,
    val id: String,
) {
    internal val key: String get() = "$orderId#$id"
}

sealed interface PaymentHandoffEvent {
    val attempt: PaymentAttempt
    val orderId: String get() = attempt.orderId

    /**
     * The provider sheet ended, however it ended. The caller polls the server;
     * it must not conclude success or failure from this.
     */
    data class SheetClosed(override val attempt: PaymentAttempt) : PaymentHandoffEvent

    /**
     * The sheet could not be opened. Nothing was presented to the buyer, so
     * no payment can have been taken.
     */
    data class Unavailable(
        override val attempt: PaymentAttempt,
        val reason: String,
    ) : PaymentHandoffEvent
}
