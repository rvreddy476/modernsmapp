package com.us.android.feature.kitchen.queue

import java.time.Duration

/**
 * The looping new-order alert. Implemented by the application (it owns the
 * notification channel and the audio focus); the queue only says when.
 *
 * [ring] is called on every queue update while it should sound and must be
 * idempotent; [silence] likewise.
 */
interface NewOrderAlert {
    fun ring(waitingOrders: Int)

    fun silence()

    /**
     * A `food_order_new` push arrived: ring now, for at most [ringFor], before
     * the queue has the order (see [PushRingHold]). [deepLink] is the push's
     * `deep_link` (`/kitchen/orders/<id>?restaurant_id=<id>`): opening the alarm
     * opens that order, as the system-drawn card did before the push went
     * data-only. Null opens the Kitchen home.
     */
    fun ringFromPush(ringFor: Duration, deepLink: String? = null)
}

object NewOrderAlertPolicy {
    /** Ring while any order can still be accepted. Expired and answered orders never ring. */
    fun shouldRing(windows: Collection<AcceptWindow>): Boolean = windows.any { it.canRespond }
}
