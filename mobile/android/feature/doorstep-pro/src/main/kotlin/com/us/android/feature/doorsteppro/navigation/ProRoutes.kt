package com.us.android.feature.doorsteppro.navigation

import androidx.lifecycle.SavedStateHandle
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import kotlinx.serialization.Serializable

/*
 * The professional's destinations. Type-safe Navigation Compose routes; ids
 * only, never a payload — every screen re-reads the server.
 */

@Serializable
data object HomeRoute

@Serializable
data object OnboardingRoute

@Serializable
data object ProfileStepRoute

@Serializable
data object DigiLockerRoute

@Serializable
data object SelfieRoute

@Serializable
data object SkillsRoute

@Serializable
data object AreaRoute

@Serializable
data object HoursRoute

@Serializable
data object BankRoute

@Serializable
data object PoliceCertificateRoute

@Serializable
data object AgreementRoute

@Serializable
data object PanRoute

@Serializable
data class OfferRoute(val offerId: String)

@Serializable
data object JobsRoute

@Serializable
data class JobRoute(val bookingId: String)

@Serializable
data class ChatRoute(val bookingId: String)

@Serializable
data object EarningsRoute

@Serializable
data object AccountRoute

/** The screen behind each checklist step. */
fun stepRoute(step: OnboardingStep): Any = when (step) {
    OnboardingStep.PROFILE -> ProfileStepRoute
    OnboardingStep.AADHAAR -> DigiLockerRoute
    OnboardingStep.SELFIE -> SelfieRoute
    OnboardingStep.SKILLS -> SkillsRoute
    OnboardingStep.SERVICE_AREA -> AreaRoute
    OnboardingStep.WEEKLY_HOURS -> HoursRoute
    OnboardingStep.BANK -> BankRoute
    OnboardingStep.POLICE_CERTIFICATE -> PoliceCertificateRoute
    OnboardingStep.AGREEMENT -> AgreementRoute
    OnboardingStep.PAN -> PanRoute
}

/** A required navigation argument, failing loudly when the route was built without it. */
fun SavedStateHandle.requireArg(name: String): String =
    checkNotNull(get<String>(name)) { "navigation argument '$name' is missing" }
