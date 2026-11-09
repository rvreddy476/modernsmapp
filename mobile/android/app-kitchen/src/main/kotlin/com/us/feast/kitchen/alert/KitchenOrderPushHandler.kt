package com.us.feast.kitchen.alert

import android.os.Handler
import android.os.Looper
import com.us.android.core.notifications.OperationalPushHandler
import com.us.android.feature.kitchen.queue.NewOrderAlert
import com.us.android.feature.kitchen.queue.NewOrderPush
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Rings the looping new-order alarm from a `food_order_new` push (food audit
 * M4): the push is what reaches a kitchen whose process Android has cached or
 * frozen, where the in-app queue cannot run.
 *
 * notification-service sends `food_order_new` to Android DATA-ONLY (food
 * server lane, 9 Nov 2026): `type`, `order_id`, `deep_link`,
 * `accept_deadline_at`, `title`, `body`, high priority, and an FCM TTL of the
 * time left to the deadline, with no notification block. So the system never
 * draws a card of its own: this handler is the only thing that shows the
 * order, in every app state, and the generic presenter must not draw it too.
 *
 * Fail-safe: it rings on the push alone, before anything is fetched, and the
 * queue (once it runs) either takes the alarm over or silences it. The alarm
 * stops at the order's accept deadline at the latest, and opening it opens the
 * pushed order. A push whose deadline has passed is dropped: nothing is left
 * to accept.
 */
@Singleton
class KitchenOrderPushHandler internal constructor(
    private val alert: NewOrderAlert,
    private val now: () -> Instant,
    private val onMain: (() -> Unit) -> Unit,
) : OperationalPushHandler {

    @Inject
    constructor(alert: NewOrderAlert) : this(alert, Instant::now, { block -> mainHandler.post(block) })

    override fun onPush(data: Map<String, String>): Boolean {
        if (data[TYPE_KEY] != NewOrderPush.TYPE) return false
        val ringFor = NewOrderPush.ringFor(data, now()) ?: return true
        val deepLink = NewOrderPush.deepLink(data)
        onMain { alert.ringFromPush(ringFor, deepLink) }
        return true
    }

    private companion object {
        const val TYPE_KEY = "type"
        val mainHandler = Handler(Looper.getMainLooper())
    }
}
