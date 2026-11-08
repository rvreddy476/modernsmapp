package com.us.feast.kitchen.alert

import android.Manifest
import android.app.Application
import android.app.Notification
import android.app.NotificationManager
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.NotificationChannelSpec
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

    @Test
    fun `the push handler rings only for a new order, and drops one already past its deadline`() {
        val rung = mutableListOf<Duration>()
        val alert = object : NewOrderAlert {
            override fun ring(waitingOrders: Int) = Unit
            override fun silence() = Unit
            override fun ringFromPush(ringFor: Duration) {
                rung += ringFor
            }
        }
        val now = Instant.parse("2026-11-08T06:30:00Z")
        val handler = KitchenOrderPushHandler(alert, { now }, { block -> block() })

        assertThat(handler.onPush(mapOf("type" to "food_kitchen_order_updated"))).isFalse()
        assertThat(handler.onPush(mapOf("type" to "food_order_new", "accept_deadline_at" to "2026-11-08T06:29:00Z")))
            .isTrue()
        assertThat(rung).isEmpty()
        assertThat(handler.onPush(mapOf("type" to "food_order_new", "accept_deadline_at" to "2026-11-08T06:31:00Z")))
            .isTrue()
        assertThat(rung).containsExactly(Duration.ofSeconds(60))
    }
}
