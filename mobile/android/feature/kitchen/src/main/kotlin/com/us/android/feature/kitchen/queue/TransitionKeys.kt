package com.us.android.feature.kitchen.queue

import com.us.android.core.food.repository.KitchenOrderAction
import java.time.Instant
import java.util.UUID

/**
 * The Idempotency-Key for each (order, action) the kitchen sends.
 *
 * A key is minted on the first tap and kept only while that request's outcome
 * is unknown (the connection dropped, a 5xx): the retry then resends the SAME
 * key and the server answers with what it already did. A definite answer frees
 * the key, so a later tap is a new request.
 */
internal class TransitionKeys(private val mint: () -> String = { UUID.randomUUID().toString() }) {

    private val kept = HashMap<Pair<String, KitchenOrderAction>, String>()

    fun keyFor(orderId: String, action: KitchenOrderAction): String = kept.getOrPut(orderId to action, mint)

    fun settle(orderId: String, action: KitchenOrderAction, outcomeUnknown: Boolean) {
        if (!outcomeUnknown) kept.remove(orderId to action)
    }

    /** An earlier request for this order and action may have gone through. */
    fun isOutstanding(orderId: String, action: KitchenOrderAction): Boolean = (orderId to action) in kept
}

/**
 * "Too late to accept" is said on the device only for a fresh tap. A retry of
 * an accept whose outcome is unknown goes to the server even after the
 * countdown ends: the first request may have landed in time, and only the
 * server knows (food audit L8).
 */
internal fun refuseAcceptLocally(countdown: AcceptCountdown?, now: Instant, retryOfUnknownOutcome: Boolean): Boolean =
    !retryOfUnknownOutcome && countdown != null && !countdown.canRespond(now)
