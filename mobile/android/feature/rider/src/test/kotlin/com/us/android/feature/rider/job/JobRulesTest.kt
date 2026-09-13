package com.us.android.feature.rider.job

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.VerifyDeliveryDto
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test

class JobRulesTest {

    @Test
    fun `verify-delivery failures map to what the rider is told`() {
        val expected = mapOf(
            "delivery_verify_delivery_post_422_code_invalid.json" to DeliveryCodeOutcome.WrongCode,
            "delivery_verify_delivery_post_429_attempts_exceeded.json" to DeliveryCodeOutcome.Locked,
            "delivery_verify_delivery_post_409_not_picked_up.json" to DeliveryCodeOutcome.NotPickedUp,
            "delivery_verify_delivery_post_404_not_your_assignment.json" to DeliveryCodeOutcome.NotYourJob,
            "delivery_verify_delivery_post_403_partner_not_active.json" to DeliveryCodeOutcome.NotActive,
        )
        for ((name, outcome) in expected) {
            assertWithMessage(name).that(DeliveryCodeOutcome.from(RiderFixtures.failure(name))).isEqualTo(outcome)
        }
        for (name in listOf("delivery_verify_delivery_post_400_invalid_body.json", "delivery_verify_delivery_post_400_invalid_assignment_id.json")) {
            assertThat(DeliveryCodeOutcome.from(RiderFixtures.failure(name))).isInstanceOf(DeliveryCodeOutcome.Failed::class.java)
        }
        assertThat(DeliveryCodeOutcome.from(RiderFixtures.success("delivery_verify_delivery_post_200.json", VerifyDeliveryDto.serializer())))
            .isEqualTo(DeliveryCodeOutcome.Delivered)
    }

    @Test
    fun `an assigned job is confirmed first and shows no pickup code`() {
        val assigned = assignment("delivery_assignment_current_get_200_assigned.json")
        val actions = JobActions.of(assigned)

        assertThat(actions.phase).isEqualTo(JobPhase.CONFIRM)
        assertThat(actions.step).isEqualTo(RiderAssignmentStep.ACCEPT)
        assertThat(actions.pickupCode).isNull()
        assertThat(actions.canEnterDeliveryCode).isFalse()
    }

    @Test
    fun `after accept the pickup code shows for the kitchen, and disappears after pickup`() {
        val accepted = assignment("delivery_assignment_current_get_200_accepted.json")
        val toRestaurant = JobActions.of(accepted)
        assertThat(toRestaurant.pickupCode).isEqualTo("4821")
        assertThat(toRestaurant.step).isEqualTo(RiderAssignmentStep.ARRIVED_AT_RESTAURANT)
        assertThat(toRestaurant.navigateToRestaurant).isTrue()

        val atRestaurant = JobActions.of(accepted.copy(status = "ARRIVED_AT_RESTAURANT"))
        assertThat(atRestaurant.pickupCode).isEqualTo("4821")
        assertThat(atRestaurant.step).isNull() // the kitchen verifies pickup, not the rider

        val pickedUp = JobActions.of(accepted.copy(status = "PICKED_UP"))
        assertThat(pickedUp.pickupCode).isNull()
        assertThat(pickedUp.canEnterDeliveryCode).isTrue()
        assertThat(pickedUp.navigateToCustomer).isTrue()
        assertThat(pickedUp.canRelease).isFalse()
        assertThat(pickedUp.step).isEqualTo(RiderAssignmentStep.ARRIVED_AT_CUSTOMER)

        assertThat(JobActions.of(accepted.copy(status = "DELIVERED")).isActive).isFalse()
    }

    @Test
    fun `navigation hands off two-wheeler directions, falling back to a geo pin or search`() {
        val restaurant = NavTarget(label = "Test Kitchen", latitude = 12.9716, longitude = 77.5946)
        assertThat(NavigationHandoff.twoWheelerUri(restaurant)).isEqualTo("google.navigation:q=12.9716,77.5946&mode=l")
        assertThat(NavigationHandoff.geoUri(restaurant)).isEqualTo("geo:12.9716,77.5946?q=12.9716,77.5946(Test+Kitchen)")

        val nameOnly = NavTarget(label = "Test Kitchen")
        assertThat(NavigationHandoff.twoWheelerUri(nameOnly)).isNull()
        assertThat(NavigationHandoff.geoUri(nameOnly)).isEqualTo("geo:0,0?q=Test+Kitchen")
    }

    private fun assignment(name: String): DeliveryAssignmentDto =
        RiderFixtures.success(name, DeliveryAssignmentDto.serializer()).value
}
