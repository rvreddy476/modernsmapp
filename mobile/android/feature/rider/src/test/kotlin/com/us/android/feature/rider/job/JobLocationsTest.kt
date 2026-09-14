package com.us.android.feature.rider.job

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.ItemsDto
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test

class JobLocationsTest {

    private val accepted = assignment("delivery_assignment_current_get_200_accepted.json")
    private val assigned = assignment("delivery_assignment_current_get_200_assigned.json")

    @Test
    fun `an accepted job navigates by the server's pickup and drop links`() {
        val navigation = JobLocations.of(accepted)

        assertThat(navigation.pickup).isEqualTo(NavIntent(checkNotNull(accepted.navigation?.pickupUrl), NavIntent.Source.SERVER_LINK))
        assertThat(navigation.drop).isEqualTo(NavIntent(checkNotNull(accepted.navigation?.dropUrl), NavIntent.Source.SERVER_LINK))
        assertThat(navigation.pickup?.uri)
            .isEqualTo("https://www.google.com/maps/dir/?api=1&destination=12.9716,77.5946&travelmode=two-wheeler")
    }

    @Test
    fun `an assigned job has a pickup but no drop target`() {
        val navigation = JobLocations.of(assigned)

        assertThat(navigation.pickup?.source).isEqualTo(NavIntent.Source.SERVER_LINK)
        assertThat(navigation.drop).isNull()
    }

    @Test
    fun `a finished job has no navigation`() {
        val history = RiderFixtures.success("delivery_history_get_200.json", ItemsDto.serializer(DeliveryAssignmentDto.serializer())).value.items
        for (job in history) {
            assertThat(JobLocations.of(job)).isEqualTo(JobNavigation.NONE)
        }
        assertThat(JobLocations.of(accepted.copy(status = "DELIVERED"))).isEqualTo(JobNavigation.NONE)
        assertThat(JobLocations.of(accepted.copy(status = "CANCELLED"))).isEqualTo(JobNavigation.NONE)
    }

    @Test
    fun `without server links the coordinates become geo pins`() {
        val navigation = JobLocations.of(accepted.copy(navigation = null))

        assertThat(navigation.pickup)
            .isEqualTo(NavIntent("geo:12.9716,77.5946?q=12.9716,77.5946(Test+Kitchen)", NavIntent.Source.COORDINATES))
        assertThat(navigation.drop).isEqualTo(NavIntent("geo:12.978449,77.640812?q=12.978449,77.640812", NavIntent.Source.COORDINATES))
    }

    @Test
    fun `without links or pins pickup searches by name and the drop has no target`() {
        val bare = accepted.copy(
            navigation = null,
            restaurant = accepted.restaurant?.copy(latitude = null, longitude = null),
            drop = accepted.drop?.copy(latitude = null, longitude = null),
        )
        val navigation = JobLocations.of(bare)

        assertThat(navigation.pickup).isEqualTo(NavIntent("geo:0,0?q=Test+Kitchen", NavIntent.Source.NAME_SEARCH))
        assertThat(navigation.drop).isNull()

        val noRestaurant = JobLocations.of(bare.copy(restaurant = null))
        assertThat(noRestaurant.pickup?.uri).isEqualTo("geo:0,0?q=Test+Kitchen")
    }

    @Test
    fun `a drop link with no drop is ignored, and only https links are taken`() {
        assertThat(JobLocations.of(accepted.copy(drop = null)).drop).isNull()

        val hostile = accepted.copy(navigation = accepted.navigation?.copy(pickupUrl = "intent://evil#Intent;end", dropUrl = "javascript:alert(1)"))
        val navigation = JobLocations.of(hostile)
        assertThat(navigation.pickup?.source).isEqualTo(NavIntent.Source.COORDINATES)
        assertThat(navigation.drop?.source).isEqualTo(NavIntent.Source.COORDINATES)
    }

    @Test
    fun `a 0,0 pin is treated as no pin`() {
        val zero = accepted.copy(navigation = null, restaurant = accepted.restaurant?.copy(latitude = 0.0, longitude = 0.0))
        assertThat(JobLocations.of(zero).pickup?.source).isEqualTo(NavIntent.Source.NAME_SEARCH)
    }

    private fun assignment(name: String): DeliveryAssignmentDto =
        RiderFixtures.success(name, DeliveryAssignmentDto.serializer()).value
}
