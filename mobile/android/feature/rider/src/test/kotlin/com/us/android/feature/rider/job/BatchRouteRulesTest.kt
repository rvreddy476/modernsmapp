package com.us.android.feature.rider.job

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.BatchRoutePreviewDto
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryBatchDto
import com.us.android.core.food.network.DeliveryBatchMemberDto
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test

internal fun routeAssignment(): DeliveryAssignmentDto = RiderFixtures.success(
    "delivery_assignment_current_get_200_accepted.json", DeliveryAssignmentDto.serializer(),
).value

internal fun routeBatch(a: DeliveryAssignmentDto = routeAssignment()) = DeliveryBatchDto(
    id = "batch", restaurantId = a.restaurantId, status = "assigned", createdAt = "now",
    members = listOf(DeliveryBatchMemberDto(a.orderId, 1), DeliveryBatchMemberDto("sibling-order", 2)),
)

internal fun routeSuggestion(b: DeliveryBatchDto = routeBatch()) = BatchRoutePreviewDto(
    orderIds = b.members.reversed().map { it.orderId }, source = "google_route_optimization", advisory = true, note = "advice only",
)

class BatchRouteRulesTest {
    @Test
    fun `only assigned batches before pickup are eligible`() {
        val a = routeAssignment()
        val b = routeBatch(a)
        assertThat(BatchRouteRules.eligible(a, b)).isTrue()
        assertThat(BatchRouteRules.eligible(a.copy(status = "ARRIVED_AT_RESTAURANT"), b)).isTrue()
        for (status in listOf("ASSIGNED", "PICKED_UP", "DELIVERED", "CANCELLED")) {
            assertThat(BatchRouteRules.eligible(a.copy(status = status), b)).isFalse()
        }
        assertThat(BatchRouteRules.eligible(a.copy(orderStatus = "CANCELLED_BY_ADMIN"), b)).isFalse()
        assertThat(BatchRouteRules.eligible(a, null)).isFalse()
        assertThat(BatchRouteRules.eligible(a, b.copy(restaurantId = "another"))).isFalse()
        assertThat(BatchRouteRules.eligible(a, b.copy(status = "completed"))).isFalse()
        assertThat(BatchRouteRules.eligible(a, b.copy(members = b.members.take(1)))).isFalse()
        assertThat(BatchRouteRules.eligible(a, b.copy(members = listOf(b.members.first(), b.members.first())))).isFalse()
    }

    @Test
    fun `a suggestion must be advisory and a complete exact permutation`() {
        val b = routeBatch()
        val p = routeSuggestion(b)
        assertThat(BatchRouteRules.valid(p, b)).isTrue()
        for (ids in listOf(emptyList(), p.orderIds.take(1), listOf("unknown", p.orderIds[1]), listOf(p.orderIds[0], p.orderIds[0]))) {
            assertThat(BatchRouteRules.valid(p.copy(orderIds = ids), b)).isFalse()
        }
        assertThat(BatchRouteRules.valid(p.copy(advisory = false), b)).isFalse()
    }

    @Test
    fun `readiness needs every owned order confirmed and no cancelled members`() {
        val a = routeAssignment()
        val b = routeBatch(a)
        val sibling = a.copy(id = "sibling", orderId = "sibling-order")
        assertThat(BatchRouteRules.ready(a, b, listOf(a, sibling))).isTrue()
        assertThat(BatchRouteRules.ready(a, b, listOf(a))).isFalse()
        assertThat(BatchRouteRules.ready(a, b, listOf(a, sibling.copy(status = "ASSIGNED")))).isFalse()
        assertThat(BatchRouteRules.ready(a, b, listOf(a, sibling.copy(status = "CANCELLED")))).isFalse()
        assertThat(BatchRouteRules.ready(a, b, listOf(a, sibling.copy(deliveryPartnerId = "stranger")))).isFalse()
        assertThat(BatchRouteRules.ready(a, b, listOf(a, sibling.copy(restaurantId = "another")))).isFalse()
    }

    @Test
    fun `error messages explain recovery and never show server internals`() {
        assertThat(BatchRouteRules.failure(FoodError.Unexpected(503, "BATCH_ROUTE_UNAVAILABLE", "private secret")))
            .contains("saved stop order")
        assertThat(BatchRouteRules.failure(FoodError.Unexpected(429, "BATCH_ROUTE_BUSY", "private secret")))
            .contains("wait")
        assertThat(BatchRouteRules.failure(FoodError.Unexpected(404, "BATCH_NOT_FOUND", "private secret")))
            .contains("accepted")
        assertThat(BatchRouteRules.label(routeAssignment().orderId, routeAssignment())).contains(routeAssignment().orderNumber)
        assertThat(BatchRouteRules.label("sibling-order", routeAssignment())).isEqualTo("Order ID …ng-order")
    }
}
