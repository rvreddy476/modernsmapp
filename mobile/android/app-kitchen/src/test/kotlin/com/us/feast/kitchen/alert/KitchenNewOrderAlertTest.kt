package com.us.feast.kitchen.alert

import android.Manifest
import android.app.Application
import android.app.Notification
import android.app.NotificationManager
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.NotificationChannelSpec
import com.us.android.feature.kitchen.navigation.kitchenPushTarget
import com.us.android.feature.kitchen.queue.NewOrderAlert
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import java.time.Duration
import java.time.Instant

/**
 * The kitchen's looping new-order alarm while the app is in the background:
 * a stale alarm from a dead process is stopped (food audit L3), and a
 * `food_order_new` push rings it at once, bounded by the accept deadline (M4).
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], application = Application::class)
class KitchenNewOrderAlertTest {

    private val context: Application = ApplicationProvider.getApplicationContext()
    private val manager get() = shadowOf(context.getSystemService(NotificationManager::class.java))

    @Before
    fun setUp() {
        shadowOf(context).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.KITCHEN)
    }

    @Test
    fun `an alarm left looping by a process that died is stopped by the next one`() {
        KitchenNewOrderAlert(context).ring(waitingOrders = 1)
        assertThat(manager.allNotifications).hasSize(1)

        // A new process: a new alert with no memory of what was posted.
        KitchenNewOrderAlert(context).silence()

        assertThat(manager.allNotifications).isEmpty()
    }

    @Test
    fun `a push rings at once, insistently, for no longer than the accept window`() {
        KitchenNewOrderAlert(context).ringFromPush(Duration.ofSeconds(90))

        val alarm = manager.allNotifications.single()
        assertThat(alarm.flags and Notification.FLAG_INSISTENT).isNotEqualTo(0)
        assertThat(alarm.timeoutAfter).isEqualTo(90_000L)
        assertThat(alarm.channelId).isEqualTo(NotificationChannelSpec.KITCHEN_NEW_ORDER.id)
    }

    @Test
    fun `a queue that has not fetched the pushed order yet cannot silence it`() {
        val alert = KitchenNewOrderAlert(context)
        alert.ringFromPush(Duration.ofSeconds(90))

        alert.silence()

        assertThat(manager.allNotifications).hasSize(1)
    }

    @Test
    fun `once the queue rings, it owns the alarm and may silence it`() {
        val alert = KitchenNewOrderAlert(context)
        alert.ringFromPush(Duration.ofSeconds(90))
        alert.ring(waitingOrders = 1)

        alert.silence()

        assertThat(manager.allNotifications).isEmpty()
    }

    /** The push data-only `food_order_new` carries (notification-service FoodPushData + BuildFCMMessage). */
    private val pushed = mapOf(
        "type" to "food_order_new",
        "order_id" to ORDER,
        "deep_link" to "/kitchen/orders/$ORDER?restaurant_id=$RESTAURANT",
        "accept_deadline_at" to "2026-11-08T06:31:00Z",
        "title" to "New order #FG1",
        "body" to "Accept before the timer runs out",
        "collapse_key" to "food_order_new:$ORDER",
    )

    private class RecordingAlert : NewOrderAlert {
        val rung = mutableListOf<Pair<Duration, String?>>()
        override fun ring(waitingOrders: Int) = Unit
        override fun silence() = Unit
        override fun ringFromPush(ringFor: Duration, deepLink: String?) {
            rung += ringFor to deepLink
        }
    }

    @Test
    fun `the push handler rings only for a new order, and drops one already past its deadline`() {
        val alert = RecordingAlert()
        val now = Instant.parse("2026-11-08T06:30:00Z")
        val handler = KitchenOrderPushHandler(alert, { now }, { block -> block() })

        assertThat(handler.onPush(mapOf("type" to "food_kitchen_order_updated"))).isFalse()
        assertThat(handler.onPush(pushed + ("accept_deadline_at" to "2026-11-08T06:29:00Z"))).isTrue()
        assertThat(alert.rung).isEmpty()
        assertThat(handler.onPush(pushed)).isTrue()
        assertThat(alert.rung.map { it.first }).containsExactly(Duration.ofSeconds(60))
    }

    @Test
    fun `the data-only push is drawn by the handler alone, and names the pushed order`() {
        val alert = RecordingAlert()
        val handler = KitchenOrderPushHandler(alert, { Instant.parse("2026-11-08T06:30:00Z") }, { block -> block() })

        // True: the generic presenter must not draw a second card for the same push.
        assertThat(handler.onPush(pushed)).isTrue()

        assertThat(alert.rung.single().second).isEqualTo("/kitchen/orders/$ORDER?restaurant_id=$RESTAURANT")
    }

    @Test
    fun `opening a push-rung alarm opens the pushed order`() {
        val link = "/kitchen/orders/$ORDER?restaurant_id=$RESTAURANT"
        KitchenNewOrderAlert(context).ringFromPush(Duration.ofSeconds(60), link)

        val opened = shadowOf(manager.allNotifications.single().contentIntent).savedIntent
        val target = kitchenPushTarget(opened.getStringExtra("type"), opened.getStringExtra("deep_link"))
        assertThat(target?.orderId).isEqualTo(ORDER)
        assertThat(target?.restaurantId).isEqualTo(RESTAURANT)
    }

    @Test
    fun `the queue's own alarm opens the kitchen, not an order`() {
        val alert = KitchenNewOrderAlert(context)
        alert.ringFromPush(Duration.ofSeconds(60), "/kitchen/orders/$ORDER?restaurant_id=$RESTAURANT")

        alert.ring(waitingOrders = 2)

        val opened = shadowOf(manager.allNotifications.single().contentIntent).savedIntent
        assertThat(opened.getStringExtra("deep_link")).isNull()
    }

    private companion object {
        const val ORDER = "0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015"
        const val RESTAURANT = "0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002"
    }
}
