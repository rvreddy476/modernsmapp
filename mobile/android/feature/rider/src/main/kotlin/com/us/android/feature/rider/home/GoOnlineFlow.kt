package com.us.android.feature.rider.home

import com.us.android.feature.rider.location.OfflineReason

sealed interface DutyState {
    /** [reason] says why the rider went offline, when it was not their own tap. */
    data class Offline(val reason: OfflineReason? = null) : DutyState

    /** The prominent in-app disclosure is on screen. The system prompt has NOT been shown. */
    data object ShowingDisclosure : DutyState

    /** The foreground location prompt is up. */
    data object AwaitingPermission : DutyState

    /** Refused. [canAskAgain] false means only Settings can grant it now. */
    data class PermissionDenied(val canAskAgain: Boolean) : DutyState

    /** Telling the server the rider is available. */
    data object GoingOnline : DutyState

    /** The location service is running. */
    data object Online : DutyState

    data object GoingOffline : DutyState
}

sealed interface DutyEffect {
    data object None : DutyEffect

    /** Launch the foreground location permission request. */
    data object RequestPermission : DutyEffect

    /** `POST /v1/food/delivery/availability {is_online:true}`. */
    data object SetServerOnline : DutyEffect

    /** Start RiderLocationService (foreground, type location). */
    data object StartService : DutyEffect

    /** Stop the service; it tells the server the rider is offline. */
    data object StopService : DutyEffect
}

/**
 * The Go-online toggle, in the order Play's location policy requires:
 *
 *     prominent in-app disclosure → foreground location permission → service
 *
 * The ONLY path to [DutyEffect.RequestPermission] runs through
 * [DutyState.ShowingDisclosure] and the rider accepting it, and the ONLY path to
 * [DutyEffect.StartService] runs through a granted permission and the server
 * accepting availability. Every out-of-order call is ignored.
 *
 * A rider who already granted the permission AND accepted the disclosure
 * before goes straight online; if the permission is missing, the disclosure is
 * shown again right before the prompt, whatever was accepted earlier.
 *
 * No background-location permission is ever requested: the foreground service
 * keeps "while in use" access alive for as long as the rider is on duty.
 */
class GoOnlineFlow {

    var state: DutyState = DutyState.Offline()
        private set

    fun onGoOnlineTapped(permissionGranted: Boolean, disclosureAccepted: Boolean): DutyEffect {
        if (state !is DutyState.Offline && state !is DutyState.PermissionDenied) return DutyEffect.None
        return if (permissionGranted && disclosureAccepted) {
            state = DutyState.GoingOnline
            DutyEffect.SetServerOnline
        } else {
            state = DutyState.ShowingDisclosure
            DutyEffect.None
        }
    }

    fun onDisclosureAccepted(permissionGranted: Boolean): DutyEffect {
        if (state != DutyState.ShowingDisclosure) return DutyEffect.None
        return if (permissionGranted) {
            state = DutyState.GoingOnline
            DutyEffect.SetServerOnline
        } else {
            state = DutyState.AwaitingPermission
            DutyEffect.RequestPermission
        }
    }

    fun onDisclosureDeclined() {
        if (state == DutyState.ShowingDisclosure) state = DutyState.Offline()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean): DutyEffect {
        if (state != DutyState.AwaitingPermission) return DutyEffect.None
        return if (granted) {
            state = DutyState.GoingOnline
            DutyEffect.SetServerOnline
        } else {
            state = DutyState.PermissionDenied(canAskAgain)
            DutyEffect.None
        }
    }

    fun onServerAvailability(accepted: Boolean): DutyEffect {
        if (state != DutyState.GoingOnline) return DutyEffect.None
        return if (accepted) {
            state = DutyState.Online
            DutyEffect.StartService
        } else {
            state = DutyState.Offline(OfflineReason.SERVER_REFUSED)
            DutyEffect.None
        }
    }

    fun onGoOfflineTapped(): DutyEffect {
        if (state != DutyState.Online) return DutyEffect.None
        state = DutyState.GoingOffline
        return DutyEffect.StopService
    }

    /** The service reports it stopped — on the rider's tap or on its own (no fix, revoked, signed out). */
    fun onServiceStopped(reason: OfflineReason) {
        state = DutyState.Offline(reason.takeIf { it != OfflineReason.TOGGLED_OFF })
    }

    /** The service is found running (app reopened while on duty). */
    fun onServiceRunning() {
        state = DutyState.Online
    }
}
