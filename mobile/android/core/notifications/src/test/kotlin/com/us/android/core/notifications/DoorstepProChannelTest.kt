package com.us.android.core.notifications

import android.app.NotificationManager
import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.data.PushApp
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Doorstep Pro (2026-10-04): every professional push type of the registry
 * (contracts/doorstep/asyncapi.yaml `x-push-types.doorstep_pro`) lands on the
 * channel notification-service names for it (doorstep_push.go
 * doorstepPushSpecs), and only the pro app registers those channels.
 *
 * Ids and types are written out literally: a renamed id orphans the user's
 * preference, and a type that drifts from the server's lands on a channel the
 * pro app does not have.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class DoorstepProChannelTest {

    private val expected = mapOf(
        "doorstep.pro.offer.new" to "doorstep_pro_offers",
        "doorstep.pro.offer.expired" to "doorstep_pro_offers",
        "doorstep.pro.job.cancelled" to "doorstep_pro_jobs",
        "doorstep.pro.job.rescheduled" to "doorstep_pro_jobs",
        "doorstep.pro.job.reminder" to "doorstep_pro_jobs",
        "doorstep.pro.extras.approved" to "doorstep_pro_jobs",
        "doorstep.pro.extras.declined" to "doorstep_pro_jobs",
        "doorstep.pro.extras.paid" to "doorstep_pro_jobs",
        "doorstep.pro.message.new" to "doorstep_pro_jobs",
        "doorstep.pro.rating.received" to "doorstep_pro_jobs",
        "doorstep.pro.application.approved" to "doorstep_pro_account",
        "doorstep.pro.application.rejected" to "doorstep_pro_account",
        "doorstep.pro.account.suspended" to "doorstep_pro_account",
        "doorstep.pro.account.reinstated" to "doorstep_pro_account",
        "doorstep.pro.document.reviewed" to "doorstep_pro_account",
        "doorstep.pro.background_check.expiring" to "doorstep_pro_account",
        "doorstep.pro.settlement.computed" to "doorstep_pro_earnings",
    )

    @Test
    fun `every professional push type lands on the server's channel`() {
        assertThat(expected).hasSize(17)
        expected.forEach { (type, channel) ->
            assertThat(NotificationChannelSpec.forType(type).id).isEqualTo(channel)
        }
    }

    @Test
    fun `the pro app registers exactly its five channels, and Momentum none of them`() {
        assertThat(NotificationChannelSpec.DOORSTEP_PRO.map { it.id }).containsExactly(
            "doorstep_pro_offers",
            "doorstep_pro_jobs",
            "doorstep_pro_account",
            "doorstep_pro_earnings",
            "doorstep_pro_on_duty",
        )
        assertThat(NotificationChannelSpec.MOMENTUM.intersect(NotificationChannelSpec.DOORSTEP_PRO)).isEmpty()
        // Every pushed type lands on a channel the pro app actually registers.
        expected.keys.forEach { type ->
            assertThat(NotificationChannelSpec.DOORSTEP_PRO).contains(NotificationChannelSpec.forType(type))
        }
    }

    @Test
    fun `offers are loud, the rest are not alarms, and an unknown pro type stays in the pro app`() {
        assertThat(NotificationChannelSpec.DOORSTEP_PRO_OFFERS.importance).isEqualTo(NotificationManager.IMPORTANCE_HIGH)
        assertThat(NotificationChannelSpec.DOORSTEP_PRO_OFFERS.alertSound).isTrue()
        assertThat(NotificationChannelSpec.DOORSTEP_PRO_JOBS.alertSound).isFalse()
        assertThat(NotificationChannelSpec.DOORSTEP_PRO_ON_DUTY.importance).isEqualTo(NotificationManager.IMPORTANCE_LOW)
        assertThat(NotificationChannelSpec.forType("doorstep.pro.something_new")).isEqualTo(NotificationChannelSpec.DOORSTEP_PRO_JOBS)
        // The customer's doorstep.* types never reach a pro channel.
        assertThat(NotificationChannelSpec.forType("doorstep.booking.assigned")).isEqualTo(NotificationChannelSpec.DOORSTEP_UPDATES)
        assertThat(NotificationChannelSpec.forType("something.else")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    @Test
    fun `the pro app registers its devices as doorstep_pro`() {
        assertThat(PushApp.DOORSTEP_PRO.wire).isEqualTo("doorstep_pro")
    }
}
