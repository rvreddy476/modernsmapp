package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.NotificationChannelSpec
import org.junit.Test

/** Mopedu pushes (2026-09-18): the seven ride types route to the ride screen and land on the ride channel. */
class PushDestinationsRideTest {

    @Test
    fun `the ride push types are exactly the seven the server emits`() {
        assertThat(PushDestinations.RIDE_TYPES).containsExactly(
            "ride.assigned",
            "ride.arriving",
            "ride.arrived",
            "ride.started",
            "ride.completed",
            "ride.cancelled",
            "ride.payment.paid",
        )
        assertThat(PushDestinations.isRidePush("ride.arrived")).isTrue()
        assertThat(PushDestinations.isRidePush("captain.offer")).isFalse()
        assertThat(PushDestinations.isRidePush(null)).isFalse()
    }

    @Test
    fun `every ride push type has a channel that is not SOCIAL`() {
        PushDestinations.RIDE_TYPES.forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.RIDE_UPDATES)
        }
    }
}
