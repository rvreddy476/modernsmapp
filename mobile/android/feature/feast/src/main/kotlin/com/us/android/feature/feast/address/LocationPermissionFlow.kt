package com.us.android.feature.feast.address

sealed interface LocationStep {
    data object Idle : LocationStep

    /** The in-app explanation is on screen. The system prompt has NOT been shown. */
    data object ExplainingPermission : LocationStep

    /** The system prompt is up. */
    data object AwaitingPermission : LocationStep

    data object Locating : LocationStep

    data class Located(val coordinates: Coordinates) : LocationStep

    /** Refused. [canAskAgain] false means only Settings can grant it now. Manual entry stays open. */
    data class Denied(val canAskAgain: Boolean) : LocationStep

    /** Permission granted but no fix came back. Manual entry stays open. */
    data object Unavailable : LocationStep
}

/** What the screen must do after a transition. */
sealed interface LocationEffect {
    data object None : LocationEffect

    data object RequestPermission : LocationEffect

    data object FetchLocation : LocationEffect
}

/**
 * "Use my current location" with the rationale ALWAYS before the system prompt.
 *
 * Android lets an app ask only a couple of times before a denial is permanent,
 * and a cold system dialog on a checkout is the prompt most likely to be
 * refused. The only path to [LocationEffect.RequestPermission] runs through
 * [LocationStep.ExplainingPermission] and the customer choosing to continue;
 * out-of-order calls are ignored. (The Kitchen app's location step follows the
 * same rule; features may not share code, so this is Feast's own copy.)
 */
class LocationPermissionFlow {

    var step: LocationStep = LocationStep.Idle
        private set

    fun onUseCurrentLocation(permissionGranted: Boolean): LocationEffect =
        if (permissionGranted) {
            step = LocationStep.Locating
            LocationEffect.FetchLocation
        } else {
            step = LocationStep.ExplainingPermission
            LocationEffect.None
        }

    fun onRationaleAccepted(): LocationEffect {
        if (step != LocationStep.ExplainingPermission) return LocationEffect.None
        step = LocationStep.AwaitingPermission
        return LocationEffect.RequestPermission
    }

    fun onRationaleDismissed() {
        if (step == LocationStep.ExplainingPermission) step = LocationStep.Idle
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean): LocationEffect {
        if (step != LocationStep.AwaitingPermission) return LocationEffect.None
        return if (granted) {
            step = LocationStep.Locating
            LocationEffect.FetchLocation
        } else {
            step = LocationStep.Denied(canAskAgain)
            LocationEffect.None
        }
    }

    fun onLocationResult(coordinates: Coordinates?) {
        if (step != LocationStep.Locating) return
        step = if (coordinates != null) LocationStep.Located(coordinates) else LocationStep.Unavailable
    }
}
