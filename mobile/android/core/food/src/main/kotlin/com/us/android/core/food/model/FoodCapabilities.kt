package com.us.android.core.food.model

/**
 * Which food roles the signed-in user holds — the gate each app reads before
 * showing a role's screens. Restaurant and delivery flags come from food-service's
 * own tables; admin and moderator from the gateway's scopes.
 */
data class FoodCapabilities(
    val userId: String,
    val isCustomer: Boolean,
    val isRestaurantOwner: Boolean,
    val isDeliveryPartner: Boolean,
    val isAdmin: Boolean,
    val isModerator: Boolean,
)
