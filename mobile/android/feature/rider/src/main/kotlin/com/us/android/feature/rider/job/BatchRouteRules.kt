package com.us.android.feature.rider.job

import com.us.android.core.food.network.BatchRoutePreviewDto
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryBatchDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.code
import com.us.android.feature.rider.ui.userMessage

/** Presentation checks only; the server rechecks ownership and every member's acceptance. */
internal object BatchRouteRules {
    fun batchWindow(assignment: DeliveryAssignmentDto?): Boolean = assignment != null &&
        assignment.status in setOf("ASSIGNED", "ACCEPTED", "ARRIVED_AT_RESTAURANT") &&
        assignment.orderStatus == "DELIVERY_ASSIGNED"

    fun beforePickup(assignment: DeliveryAssignmentDto?): Boolean = assignment != null &&
        assignment.status in setOf("ACCEPTED", "ARRIVED_AT_RESTAURANT") &&
        assignment.orderStatus == "DELIVERY_ASSIGNED"

    fun eligible(assignment: DeliveryAssignmentDto?, batch: DeliveryBatchDto?): Boolean {
        return beforePickup(assignment) && visible(assignment, batch)
    }

    fun visible(assignment: DeliveryAssignmentDto?, batch: DeliveryBatchDto?): Boolean {
        if (!batchWindow(assignment) || assignment == null || batch == null) return false
        return batch.status == "assigned" && batch.restaurantId == assignment.restaurantId &&
            batch.members.size in 2..3 && batch.members.map { it.orderId }.toSet().size == batch.members.size &&
            batch.members.any { it.orderId == assignment.orderId } &&
            batch.members.all { it.orderId.isNotBlank() && it.sequence > 0 } &&
            batch.members.map { it.sequence }.toSet().size == batch.members.size
    }

    /** Never act on history, another restaurant, another partner, or a partial list. */
    fun members(assignment: DeliveryAssignmentDto, batch: DeliveryBatchDto, jobs: List<DeliveryAssignmentDto>): List<DeliveryAssignmentDto> {
        val partnerId = assignment.deliveryPartnerId?.takeIf { it.isNotBlank() } ?: return emptyList()
        val result = batch.members.map { member ->
            jobs.singleOrNull { it.orderId == member.orderId && it.restaurantId == batch.restaurantId && it.deliveryPartnerId == partnerId }
                ?: return emptyList()
        }
        return result.takeIf { it.all(::batchWindow) } ?: emptyList()
    }

    fun ready(assignment: DeliveryAssignmentDto?, batch: DeliveryBatchDto?, jobs: List<DeliveryAssignmentDto>): Boolean {
        if (assignment == null || batch == null || !eligible(assignment, batch)) return false
        val owned = members(assignment, batch, jobs)
        return owned.size == batch.members.size && owned.all(::beforePickup)
    }

    fun valid(preview: BatchRoutePreviewDto, batch: DeliveryBatchDto): Boolean =
        preview.advisory && preview.orderIds.size == batch.members.size &&
            preview.orderIds.toSet().size == preview.orderIds.size &&
            preview.orderIds.toSet() == batch.members.map { it.orderId }.toSet()

    fun failure(error: FoodError): String = when (error.code) {
        "BATCH_NOT_FOUND" -> "This batch isn't ready for a suggestion. All orders must be accepted and still waiting for pickup."
        "BATCH_ROUTE_BUSY" -> "Please wait a moment, then try again. Your saved stop order hasn't changed."
        "BATCH_ROUTE_UNAVAILABLE" -> "Route suggestions are unavailable right now. Follow your saved stop order."
        else -> error.userMessage()
    }

    // The DTO has no sibling order numbers: use the authorized ID, never invent a number/name/address.
    fun label(orderId: String, assignment: DeliveryAssignmentDto): String =
        if (orderId == assignment.orderId) "Order ${assignment.orderNumber} (current)" else "Order ID …${orderId.takeLast(8)}"
}
