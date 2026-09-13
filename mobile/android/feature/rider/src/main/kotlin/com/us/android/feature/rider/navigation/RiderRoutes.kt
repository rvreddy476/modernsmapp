package com.us.android.feature.rider.navigation

import androidx.lifecycle.SavedStateHandle
import com.us.android.feature.rider.onboarding.RiderStep
import kotlinx.serialization.Serializable

/*
 * The Rider's destinations. Arguments travel in the route so each ViewModel
 * reads them from its own SavedStateHandle and survives process death.
 */

/** [userId] names the rider's realtime topic, `food.delivery_partner.<user_id>.assignments`. */
@Serializable
data class HomeRoute(val userId: String)

@Serializable
data object VerificationRoute

@Serializable
data object ProfileRoute

@Serializable
data object DigiLockerRoute

@Serializable
data object DocumentsRoute

@Serializable
data object SelfieRoute

@Serializable
data object PayoutRoute

@Serializable
data class OfferRoute(val offerId: String)

@Serializable
data object ActiveJobRoute

@Serializable
data object EarningsRoute

internal fun stepRoute(step: RiderStep): Any = when (step) {
    RiderStep.VEHICLE -> ProfileRoute
    RiderStep.AADHAAR_DIGILOCKER -> DigiLockerRoute
    RiderStep.DRIVING_LICENCE, RiderStep.VEHICLE_RC -> DocumentsRoute
    RiderStep.SELFIE -> SelfieRoute
    RiderStep.PAYOUT_ACCOUNT -> PayoutRoute
}

internal fun SavedStateHandle.requireArg(name: String): String =
    checkNotNull(get<String>(name)) { "navigation argument '$name' is missing" }
