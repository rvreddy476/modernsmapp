package com.us.android.feature.rider.job

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test
import java.time.ZoneId

class JobDetailsTest {

    private val accepted = assignment("delivery_assignment_current_get_200_accepted.json")
    private val assigned = assignment("delivery_assignment_current_get_200_assigned.json")

    @Test
    fun `an accepted job shows the customer's first name, address, landmark and instructions`() {
        val details = JobDetails.of(accepted)

        assertThat(details.customer).isEqualTo(
            CustomerDrop(
                firstName = "Asha",
                addressLines = listOf("42 Lake View Road", "Flat 3B", "Bengaluru 560038"),
                landmark = "Opp. City Park",
                instructions = "Ring the bell twice",
            ),
        )
        assertThat(details.navigation.drop).isNotNull()
    }

    @Test
    fun `nothing about the customer is in the state when the assignment has no drop`() {
        for (job in listOf(assigned, accepted.copy(drop = null))) {
            val details = JobDetails.of(job)
            assertThat(details.customer).isNull()
            assertThat(details.navigation.drop).isNull()
        }
    }

    @Test
    fun `a finished job keeps no customer even if a drop arrives`() {
        assertThat(JobDetails.of(accepted.copy(status = "DELIVERED")).customer).isNull()
    }

    @Test
    fun `the call button exists only when the restaurant has a phone`() {
        val withPhone = JobDetails.of(accepted)
        assertThat(withPhone.canCallRestaurant).isTrue()
        assertThat(withPhone.restaurantPhone).isEqualTo("08040000000")

        for (phone in listOf(null, "", "   ", "--")) {
            val details = JobDetails.of(accepted.copy(restaurant = accepted.restaurant?.copy(phone = phone)))
            assertThat(details.canCallRestaurant).isFalse()
        }
        assertThat(JobDetails.of(accepted.copy(restaurant = null)).canCallRestaurant).isFalse()
        assertThat(JobDetails.dialable(" +91 80 4000 0000 ")).isEqualTo("+918040000000")
    }

    @Test
    fun `pay, restaurant and ETA come from the job detail`() {
        val details = JobDetails.of(accepted)

        assertThat(details.pay).isEqualTo(Paise(2_320))
        assertThat(details.restaurantName).isEqualTo("Test Kitchen")
        assertThat(details.restaurantAddressLines).containsExactly("1 Test Lane", "Near Test Park", "Bengaluru").inOrder()
        assertThat(details.etaText(ZoneId.of("Asia/Kolkata"))).isEqualTo("12:22 PM")
        assertThat(JobDetails.of(accepted.copy(etaAt = null)).etaText(ZoneId.of("Asia/Kolkata"))).isNull()
        assertThat(JobDetails.of(accepted.copy(etaAt = "not a time")).etaAt).isNull()
    }

    private fun assignment(name: String): DeliveryAssignmentDto =
        RiderFixtures.success(name, DeliveryAssignmentDto.serializer()).value
}
