package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Channel routing for "{creator} is live" pushes (2026-10-02).
 *
 * Before the LIVE channel existed `creator_went_live` fell through to SOCIAL,
 * so a viewer who muted likes muted live alerts too. This pins the type to
 * its own channel, the channel's identity (a changed id orphans the user's
 * setting), and that only Momentum registers it: the partner apps send no
 * such push, so a switch for it there would be noise.
 */
class LiveChannelRoutingTest {

    @Test
    fun `a went-live push routes to LIVE`() {
        assertThat(NotificationChannelSpec.forType("creator_went_live")).isEqualTo(NotificationChannelSpec.LIVE)
    }

    @Test
    fun `the channel's id, copy and importance are pinned`() {
        val spec = NotificationChannelSpec.LIVE
        assertThat(spec.id).isEqualTo("live")
        assertThat(spec.title).isEqualTo("Live streams")
        assertThat(spec.description).isEqualTo("When creators you follow go live")
        assertThat(spec.importance).isEqualTo(NotificationChannelSpec.NEW_VIDEOS.importance)
        assertThat(spec.importance).isEqualTo(android.app.NotificationManager.IMPORTANCE_DEFAULT)
        assertThat(spec.alertSound).isFalse()
        assertThat(spec.privateOnLockScreen).isFalse()
    }

    @Test
    fun `Momentum registers LIVE and no partner app does`() {
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.LIVE)
        assertThat(NotificationChannelSpec.KITCHEN.map { it.id }).containsExactly("food_orders", "kitchen_new_order")
        assertThat(NotificationChannelSpec.RIDER.map { it.id })
            .containsExactly("food_orders", "rider_job_offer", "rider_on_duty")
        assertThat(NotificationChannelSpec.CAPTAIN.map { it.id })
            .containsExactly("captain_offer", "captain_on_duty", "captain_earnings", "captain_account")
    }

    @Test
    fun `uploads and social types are untouched`() {
        assertThat(NotificationChannelSpec.forType("creator_uploaded_video"))
            .isEqualTo(NotificationChannelSpec.NEW_VIDEOS)
        assertThat(NotificationChannelSpec.forType("reaction")).isEqualTo(NotificationChannelSpec.SOCIAL)
        assertThat(NotificationChannelSpec.forType("live_gift")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }
}
