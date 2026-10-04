package com.us.android.push

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.NotificationChannelSpec
import org.junit.Test

/**
 * Doorstep pushes (2026-10-04): the 17 Momentum types of the registry
 * (contracts/doorstep/asyncapi.yaml x-push-types.momentum) route to the
 * booking — or the dues screen — and land on the doorstep_updates channel.
 * The professionals' doorstep.pro.* types are not Momentum's.
 */
class PushDestinationsDoorstepTest {

    private val booking = "9b3f0c55-0000-4000-8000-000000000000"

    @Test
    fun `the doorstep push types are exactly the registry's seventeen Momentum types`() {
        assertThat(PushDestinations.DOORSTEP_TYPES).containsExactly(
            "doorstep.booking.confirmed",
            "doorstep.booking.assigned",
            "doorstep.booking.reassigned",
            "doorstep.booking.pro_en_route",
            "doorstep.booking.pro_arrived",
            "doorstep.booking.started",
            "doorstep.booking.extras_proposed",
            "doorstep.booking.extras_payment_due",
            "doorstep.booking.completed",
            "doorstep.booking.cancelled",
            "doorstep.booking.expired",
            "doorstep.booking.refund_issued",
            "doorstep.booking.reminder",
            "doorstep.booking.pro_no_show",
            "doorstep.outstanding.due",
            "doorstep.message.new",
            "doorstep.rework.updated",
        )
        assertThat(PushDestinations.DOORSTEP_TYPES.none { it.startsWith("doorstep.pro.") }).isTrue()
        assertThat(PushDestinations.isDoorstepPush("doorstep.pro.offer.new")).isFalse()
        assertThat(PushDestinations.isDoorstepPush(null)).isFalse()
    }

    @Test
    fun `a booking push opens that booking from the bare deep link`() {
        val target = PushDestinations.doorstepTargetOf(
            PushDestination("doorstep.booking.assigned", entityId = "", deepLink = "/doorstep/bookings/$booking"),
        )
        assertThat(target).isEqualTo(DoorstepPushTarget.Booking(booking))
    }

    @Test
    fun `the registry's momentum scheme and sub-paths still open the booking`() {
        listOf(
            "momentum://doorstep/bookings/$booking",
            "momentum://doorstep/bookings/$booking/extras",
            "/doorstep/bookings/$booking/rate?from=push",
            "doorstep/bookings/$booking/chat#latest",
        ).forEach { link ->
            assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.booking.completed", "", link)))
                .isEqualTo(DoorstepPushTarget.Booking(booking))
        }
    }

    @Test
    fun `without a usable deep link the entity id is the booking`() {
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.booking.started", booking, "")))
            .isEqualTo(DoorstepPushTarget.Booking(booking))
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.message.new", booking, "/somewhere/else")))
            .isEqualTo(DoorstepPushTarget.Booking(booking))
    }

    @Test
    fun `the dues push opens the dues screen whatever its id`() {
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.outstanding.due", booking, "/doorstep/outstanding")))
            .isEqualTo(DoorstepPushTarget.Outstanding)
    }

    @Test
    fun `nothing usable, or not a doorstep push, routes nowhere`() {
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.booking.reminder", "", ""))).isNull()
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.booking.reminder", "../x", "/doorstep/bookings/../x")))
            .isNull()
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("ride.arrived", booking, "/doorstep/bookings/$booking"))).isNull()
        assertThat(PushDestinations.doorstepTargetOf(PushDestination("doorstep.pro.offer.new", booking, ""))).isNull()
    }

    @Test
    fun `every doorstep push type lands on the doorstep channel, which Momentum registers`() {
        PushDestinations.DOORSTEP_TYPES.forEach { type ->
            assertThat(NotificationChannelSpec.forType(type)).isEqualTo(NotificationChannelSpec.DOORSTEP_UPDATES)
        }
        assertThat(NotificationChannelSpec.DOORSTEP_UPDATES.id).isEqualTo("doorstep_updates")
        assertThat(NotificationChannelSpec.MOMENTUM).contains(NotificationChannelSpec.DOORSTEP_UPDATES)
        assertThat(NotificationChannelSpec.forType("doorstep.pro.offer.new")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }
}
