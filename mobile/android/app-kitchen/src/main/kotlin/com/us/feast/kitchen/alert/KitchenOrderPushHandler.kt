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
 * Fail-safe: it rings on the push alone, before anything is fetched, and the
 * queue (once it runs) either takes the alarm over or silences it. The alarm
 * stops at the order's accept deadline at the latest. A push whose deadline has
 * passed is dropped: nothing is left to accept.
 *
 * This only reaches here for a DATA-ONLY push. While notification-service
 * sends `food_order_new` with a notification block, a backgrounded kitchen
 * gets the system's one-shot card on `kitchen_new_order` instead.
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
        onMain { alert.ringFromPush(ringFor) }
        return true
    }

    private companion object {
        const val TYPE_KEY = "type"
        val mainHandler = Handler(Looper.getMainLooper())
    }
}
