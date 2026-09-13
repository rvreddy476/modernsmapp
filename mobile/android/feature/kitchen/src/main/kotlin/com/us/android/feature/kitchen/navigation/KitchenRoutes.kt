package com.us.android.feature.kitchen.navigation

import androidx.lifecycle.SavedStateHandle
import com.us.android.feature.kitchen.onboarding.StepEditor
import kotlinx.serialization.Serializable

/*
 * The Kitchen's destinations. Every one carries the restaurant id, so each
 * screen's ViewModel reads it from its own SavedStateHandle — it survives
 * process death and never depends on a shared holder.
 */

@Serializable
data class KitchenHomeRoute(val restaurantId: String)

@Serializable
data class OnboardingRoute(val restaurantId: String)

@Serializable
data class LocationStepRoute(val restaurantId: String)

@Serializable
data class HoursStepRoute(val restaurantId: String)

@Serializable
data class ComplianceStepRoute(val restaurantId: String)

@Serializable
data class FssaiStepRoute(val restaurantId: String)

@Serializable
data class PayoutStepRoute(val restaurantId: String)

@Serializable
data class MenuRoute(val restaurantId: String)

/** [itemId] null creates a dish in [categoryId]. */
@Serializable
data class MenuItemRoute(val restaurantId: String, val categoryId: String, val itemId: String? = null)

@Serializable
data class OrderDetailRoute(val restaurantId: String, val orderId: String)

internal fun stepRoute(editor: StepEditor, restaurantId: String): Any = when (editor) {
    StepEditor.LOCATION -> LocationStepRoute(restaurantId)
    StepEditor.OPERATING_HOURS -> HoursStepRoute(restaurantId)
    StepEditor.COMPLIANCE -> ComplianceStepRoute(restaurantId)
    StepEditor.FSSAI -> FssaiStepRoute(restaurantId)
    StepEditor.PAYOUT -> PayoutStepRoute(restaurantId)
    StepEditor.MENU -> MenuRoute(restaurantId)
}

internal fun SavedStateHandle.requireArg(name: String): String =
    checkNotNull(get<String>(name)) { "navigation argument '$name' is missing" }

internal fun SavedStateHandle.restaurantId(): String = requireArg("restaurantId")
