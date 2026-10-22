package com.us.android.feature.chat.location

import com.us.android.core.chat.data.ChatLocation

/** Where "Share location" is (founder, 23 Oct 2026). */
sealed interface ShareLocationStep {
    data object Idle : ShareLocationStep

    /** The in-app explanation is on screen. The system prompt has NOT been shown. */
    data object ExplainingPermission : ShareLocationStep

    /** The system prompt is up. */
    data object AwaitingPermission : ShareLocationStep

    data object Locating : ShareLocationStep

    /** A fix, ready to confirm and send. */
    data class Located(val location: ChatLocation) : ShareLocationStep

    /** Refused. [canAskAgain] false means only Settings can grant it now. */
    data class Denied(val canAskAgain: Boolean) : ShareLocationStep

    /** Permission granted but no fix came back (location services off, no signal). */
    data object Unavailable : ShareLocationStep
}

/** What the screen must do after a transition. */
sealed interface ShareLocationEffect {
    data object None : ShareLocationEffect

    data object RequestPermission : ShareLocationEffect

    data object FetchLocation : ShareLocationEffect
}

/**
 * "Share location" with the rationale ALWAYS before the system prompt.
 *
 * Android lets an app ask only a couple of times before a denial is
 * permanent, and a cold system dialog in the middle of a chat is the prompt
 * most likely to be refused. The only path to
 * [ShareLocationEffect.RequestPermission] runs through
 * [ShareLocationStep.ExplainingPermission] and the person choosing to
 * continue; out-of-order calls are ignored. (Feast's address step follows the
 * same rule; features may not share code, so this is chat's own copy.)
 */
class ShareLocationFlow {

    var step: ShareLocationStep = ShareLocationStep.Idle
        private set

    fun onShareLocation(permissionGranted: Boolean): ShareLocationEffect =
        if (permissionGranted) {
            step = ShareLocationStep.Locating
            ShareLocationEffect.FetchLocation
        } else {
            step = ShareLocationStep.ExplainingPermission
            ShareLocationEffect.None
        }

    fun onRationaleAccepted(): ShareLocationEffect {
        if (step != ShareLocationStep.ExplainingPermission) return ShareLocationEffect.None
        step = ShareLocationStep.AwaitingPermission
        return ShareLocationEffect.RequestPermission
    }

    fun onRationaleDismissed() {
        if (step == ShareLocationStep.ExplainingPermission) step = ShareLocationStep.Idle
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean): ShareLocationEffect {
        if (step != ShareLocationStep.AwaitingPermission) return ShareLocationEffect.None
        return if (granted) {
            step = ShareLocationStep.Locating
            ShareLocationEffect.FetchLocation
        } else {
            step = ShareLocationStep.Denied(canAskAgain)
            ShareLocationEffect.None
        }
    }

    fun onLocationResult(location: ChatLocation?) {
        if (step != ShareLocationStep.Locating) return
        step = if (location != null && location.valid) ShareLocationStep.Located(location) else ShareLocationStep.Unavailable
    }

    /** Back to the start (the sheet closed, or "Try again" after a failure). */
    fun reset() {
        step = ShareLocationStep.Idle
    }
}
