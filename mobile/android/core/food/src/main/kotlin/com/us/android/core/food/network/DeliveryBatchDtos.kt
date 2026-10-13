package com.us.android.core.food.network

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** food-service DeliveryBatch, read only for the caller's assigned order. */
@Serializable
data class DeliveryBatchDto(
    val id: String,
    @SerialName("restaurant_id") val restaurantId: String,
    val status: String,
    @SerialName("created_at") val createdAt: String,
    @SerialName("assigned_at") val assignedAt: String? = null,
    @SerialName("completed_at") val completedAt: String? = null,
    val members: List<DeliveryBatchMemberDto> = emptyList(),
)

@Serializable
data class DeliveryBatchMemberDto(
    @SerialName("order_id") val orderId: String,
    val sequence: Int,
)

/** Kept in memory only. Contains no route geometry, navigation or customer ETA. */
@Serializable
data class BatchRoutePreviewDto(
    @SerialName("order_ids") val orderIds: List<String>,
    val source: String,
    val advisory: Boolean,
    val note: String,
)

/** Go marshals an empty assignment slice as null. */
@Serializable
data class DeliveryAssignmentsDto(val items: List<DeliveryAssignmentDto>? = null)
