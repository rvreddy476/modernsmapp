package com.us.android.feature.kitchen.location

data class Coordinates(val latitude: Double, val longitude: Double)

sealed interface LocationStepState {
    data object Idle : LocationStepState

    /** The in-app explanation is on screen. The system prompt has NOT been shown. */
    data object ExplainingPermission : LocationStepState

    /** The system prompt is up. */
    data object AwaitingPermission : LocationStepState

    data object Locating : LocationStepState

    data class Located(val coordinates: Coordinates) : LocationStepState

    /** Refused. [canAskAgain] false means only Settings can grant it now. Manual entry stays open. */
    data class Denied(val canAskAgain: Boolean) : LocationStepState

    /** Permission granted but no fix came back. Manual entry stays open. */
    data object Unavailable : LocationStepState
}

/** What the screen must do after a transition. */
sealed interface LocationEffect {
    data object None : LocationEffect

    data object RequestPermission : LocationEffect

    data object FetchLocation : LocationEffect
}

/**
 * "Use my current location", with the rationale ALWAYS shown before the system
 * permission prompt.
 *
 * The order matters beyond politeness: Android lets an app ask only a couple of
 * times before a denial becomes permanent, and a cold system dialog on a
 * business onboarding form is the prompt most likely to be refused. So the only
 * path to [LocationEffect.RequestPermission] runs through
 * [LocationStepState.ExplainingPermission] and the partner choosing to continue.
 * Every other transition ignores out-of-order calls.
 */
class LocationPermissionFlow {

    var state: LocationStepState = LocationStepState.Idle
        private set

    fun onUseCurrentLocation(permissionGranted: Boolean): LocationEffect =
        if (permissionGranted) {
            state = LocationStepState.Locating
            LocationEffect.FetchLocation
        } else {
            state = LocationStepState.ExplainingPermission
            LocationEffect.None
        }

    fun onRationaleAccepted(): LocationEffect {
        if (state != LocationStepState.ExplainingPermission) return LocationEffect.None
        state = LocationStepState.AwaitingPermission
        return LocationEffect.RequestPermission
    }

    fun onRationaleDismissed() {
        if (state == LocationStepState.ExplainingPermission) state = LocationStepState.Idle
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean): LocationEffect {
        if (state != LocationStepState.AwaitingPermission) return LocationEffect.None
        return if (granted) {
            state = LocationStepState.Locating
            LocationEffect.FetchLocation
        } else {
            state = LocationStepState.Denied(canAskAgain)
            LocationEffect.None
        }
    }

    fun onLocationResult(coordinates: Coordinates?) {
        if (state != LocationStepState.Locating) return
        state = if (coordinates != null) LocationStepState.Located(coordinates) else LocationStepState.Unavailable
    }
}
