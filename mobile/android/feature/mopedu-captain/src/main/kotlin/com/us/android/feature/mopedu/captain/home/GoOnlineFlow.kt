package com.us.android.feature.mopedu.captain.home

import android.content.Context
import com.us.android.feature.mopedu.captain.location.OfflineReason
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

sealed interface DutyState {
    /** [reason] says why the captain went offline, when it was not their own tap. */
    data class Offline(val reason: OfflineReason? = null) : DutyState

    /** The prominent in-app disclosure is on screen. The system prompt has NOT been shown. */
    data object ShowingDisclosure : DutyState

    /** The foreground location prompt is up. */
    data object AwaitingPermission : DutyState

    /** Refused. [canAskAgain] false means only Settings can grant it now. */
    data class PermissionDenied(val canAskAgain: Boolean) : DutyState

    /** Telling the server the captain is online. */
    data object GoingOnline : DutyState

    /** The location service is running. */
    data object Online : DutyState

    data object GoingOffline : DutyState
}

sealed interface DutyEffect {
    data object None : DutyEffect

    data object RequestPermission : DutyEffect

    /** `POST /v1/rider/partners/me/online`. */
    data object SetServerOnline : DutyEffect

    /** Start CaptainLocationService (foreground, type location). */
    data object StartService : DutyEffect

    /** Stop the service; it tells the server the captain is offline. */
    data object StopService : DutyEffect
}

/**
 * The Go-online toggle, in the order Play's location policy requires:
 *
 *     prominent in-app disclosure → foreground location permission → service
 *
 * The Feast rider's GoOnlineFlow (features may not share code). The ONLY path
 * to [DutyEffect.RequestPermission] runs through [DutyState.ShowingDisclosure]
 * and the captain accepting it, and the ONLY path to [DutyEffect.StartService]
 * runs through a granted permission and the server accepting. Every
 * out-of-order call is ignored. No background-location permission is ever
 * requested.
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

    fun onServerOnline(accepted: Boolean): DutyEffect {
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

    /** The service reports it stopped — on the captain's tap or on its own (no fix, revoked, signed out). */
    fun onServiceStopped(reason: OfflineReason) {
        state = DutyState.Offline(reason.takeIf { it != OfflineReason.TOGGLED_OFF })
    }

    /** The service is found running (app reopened while on duty). */
    fun onServiceRunning() {
        state = DutyState.Online
    }
}

/** Whether this install has shown and the captain accepted the location disclosure. */
interface LocationDisclosureStore {
    fun accepted(): Boolean
    fun markAccepted()
}

@Singleton
class SharedPrefsLocationDisclosureStore @Inject constructor(
    @ApplicationContext context: Context,
) : LocationDisclosureStore {
    private val prefs = context.getSharedPreferences("captain_location_disclosure", Context.MODE_PRIVATE)

    override fun accepted(): Boolean = prefs.getBoolean(KEY, false)

    override fun markAccepted() {
        prefs.edit().putBoolean(KEY, true).apply()
    }

    private companion object {
        const val KEY = "accepted_v1"
    }
}
