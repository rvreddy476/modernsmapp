package com.us.android.core.notifications

import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The Mopedu channels (2026-09-18): the customer's ride updates in Momentum
 * only; the captain's offer and on-duty channels in the Captain app only.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MopeduChannelTest {

    @Test
    fun `every ride push lands on ride updates, and the captain offer on its own channel`() {
        listOf(
            "ride.assigned",
            "ride.arriving",
            "ride.arrived",
            "ride.started",
            "ride.completed",
            "ride.cancelled",
            "ride.payment.paid",
        ).forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.RIDE_UPDATES)
        }
        assertThat(NotificationChannelSpec.forType("captain.offer")).isEqualTo(NotificationChannelSpec.CAPTAIN_OFFER)
        assertThat(NotificationChannelSpec.forType("ride.something_new")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    @Test
    fun `the captain set is exactly its two channels and Momentum registers only ride updates`() {
        assertThat(NotificationChannelSpec.CAPTAIN.map { it.id }).containsExactly("captain_offer", "captain_on_duty")
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.RIDE_UPDATES)
        assertThat(NotificationChannelSpec.MOMENTUM).containsNoneOf(NotificationChannelSpec.CAPTAIN_OFFER, NotificationChannelSpec.CAPTAIN_ON_DUTY)
        assertThat(NotificationChannelSpec.KITCHEN).containsNoneOf(NotificationChannelSpec.RIDE_UPDATES, NotificationChannelSpec.CAPTAIN_OFFER)
        assertThat(NotificationChannelSpec.RIDER).containsNoneOf(NotificationChannelSpec.RIDE_UPDATES, NotificationChannelSpec.CAPTAIN_ON_DUTY)

        val context = ApplicationProvider.getApplicationContext<Context>()
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.CAPTAIN)
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        assertThat(manager.notificationChannels.map { it.id }).containsExactly("captain_offer", "captain_on_duty")
        assertThat(checkNotNull(manager.getNotificationChannel("captain_offer")).importance).isEqualTo(NotificationManager.IMPORTANCE_HIGH)
        assertThat(checkNotNull(manager.getNotificationChannel("captain_on_duty")).importance).isEqualTo(NotificationManager.IMPORTANCE_LOW)
    }
}
