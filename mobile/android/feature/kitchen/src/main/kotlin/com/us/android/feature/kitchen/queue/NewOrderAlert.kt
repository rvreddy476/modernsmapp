package com.us.android.feature.kitchen.queue

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
}

object NewOrderAlertPolicy {
    /** Ring while any order can still be accepted. Expired and answered orders never ring. */
    fun shouldRing(windows: Collection<AcceptWindow>): Boolean = windows.any { it.canRespond }
}
