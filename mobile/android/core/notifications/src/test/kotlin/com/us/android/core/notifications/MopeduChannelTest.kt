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
        assertThat(NotificationChannelSpec.forType("captain.payment.received")).isEqualTo(NotificationChannelSpec.CAPTAIN_EARNINGS)
        assertThat(NotificationChannelSpec.forType("ride.something_new")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    /**
     * The captain's own account (2026-09-18): every plan and onboarding push
     * lands on captain_account — DEFAULT, never the alarm-toned offer channel —
     * and nothing else does.
     */
    @Test
    fun `every subscription and onboarding push lands on the captain account channel`() {
        listOf(
            "captain.subscription.expiring",
            "captain.subscription.expired",
            "captain.subscription.renewed",
            "captain.subscription.payment_failed",
            "captain.approved",
            "captain.under_review",
        ).forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.CAPTAIN_ACCOUNT)
        }
        assertThat(NotificationChannelSpec.CAPTAIN_ACCOUNT.importance).isEqualTo(NotificationManager.IMPORTANCE_DEFAULT)
        assertThat(NotificationChannelSpec.CAPTAIN_ACCOUNT.alertSound).isFalse()
        // An offer never lands on the account channel, and a subscription push never on the offer channel.
        assertThat(NotificationChannelSpec.forType("captain.offer")).isNotEqualTo(NotificationChannelSpec.CAPTAIN_ACCOUNT)
        assertThat(NotificationChannelSpec.forType("captain.subscription.something_new")).isEqualTo(NotificationChannelSpec.SOCIAL)
        assertThat(NotificationChannelSpec.MOMENTUM).doesNotContain(NotificationChannelSpec.CAPTAIN_ACCOUNT)
        assertThat(NotificationChannelSpec.KITCHEN).doesNotContain(NotificationChannelSpec.CAPTAIN_ACCOUNT)
        assertThat(NotificationChannelSpec.RIDER).doesNotContain(NotificationChannelSpec.CAPTAIN_ACCOUNT)
    }

    @Test
    fun `the captain set is exactly its four channels and Momentum registers only ride updates`() {
        assertThat(NotificationChannelSpec.CAPTAIN.map { it.id }).containsExactly("captain_offer", "captain_on_duty", "captain_earnings", "captain_account")
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.RIDE_UPDATES)
        assertThat(NotificationChannelSpec.MOMENTUM).containsNoneOf(NotificationChannelSpec.CAPTAIN_OFFER, NotificationChannelSpec.CAPTAIN_ON_DUTY)
        assertThat(NotificationChannelSpec.KITCHEN).containsNoneOf(NotificationChannelSpec.RIDE_UPDATES, NotificationChannelSpec.CAPTAIN_OFFER)
        assertThat(NotificationChannelSpec.RIDER).containsNoneOf(NotificationChannelSpec.RIDE_UPDATES, NotificationChannelSpec.CAPTAIN_ON_DUTY)

        val context = ApplicationProvider.getApplicationContext<Context>()
        NotificationChannelSpec.createAll(context, NotificationChannelSpec.CAPTAIN)
        val manager = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        assertThat(manager.notificationChannels.map { it.id }).containsExactly("captain_offer", "captain_on_duty", "captain_earnings", "captain_account")
        assertThat(checkNotNull(manager.getNotificationChannel("captain_offer")).importance).isEqualTo(NotificationManager.IMPORTANCE_HIGH)
        assertThat(checkNotNull(manager.getNotificationChannel("captain_on_duty")).importance).isEqualTo(NotificationManager.IMPORTANCE_LOW)
        assertThat(checkNotNull(manager.getNotificationChannel("captain_account")).importance).isEqualTo(NotificationManager.IMPORTANCE_DEFAULT)
    }
}
