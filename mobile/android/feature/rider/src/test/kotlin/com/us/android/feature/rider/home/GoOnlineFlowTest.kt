package com.us.android.feature.rider.home

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.rider.location.OfflineReason
import org.junit.Test

class GoOnlineFlowTest {

    @Test
    fun `without permission the disclosure is shown and nothing is requested`() {
        val flow = GoOnlineFlow()

        val effect = flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)

        assertThat(effect).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.ShowingDisclosure)
    }

    @Test
    fun `a missing permission shows the disclosure again even if it was accepted before`() {
        val flow = GoOnlineFlow()
        assertThat(flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = true)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.ShowingDisclosure)
    }

    @Test
    fun `the permission is requested only after the disclosure is accepted`() {
        val flow = GoOnlineFlow()
        // Out of order: no disclosure on screen, so nothing is requested.
        assertThat(flow.onDisclosureAccepted(permissionGranted = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.Offline())

        flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)
        assertThat(flow.onDisclosureAccepted(permissionGranted = false)).isEqualTo(DutyEffect.RequestPermission)
        assertThat(flow.state).isEqualTo(DutyState.AwaitingPermission)
    }

    @Test
    fun `the service starts only after the permission is granted and the server accepts`() {
        val flow = GoOnlineFlow()
        flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)
        flow.onDisclosureAccepted(permissionGranted = false)

        // The server cannot start anything while the prompt is still up.
        assertThat(flow.onServerAvailability(accepted = true)).isEqualTo(DutyEffect.None)

        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(DutyEffect.SetServerOnline)
        assertThat(flow.state).isEqualTo(DutyState.GoingOnline)
        assertThat(flow.onServerAvailability(accepted = true)).isEqualTo(DutyEffect.StartService)
        assertThat(flow.state).isEqualTo(DutyState.Online)
    }

    @Test
    fun `every path to the prompt passes the disclosure, and to the service passes a grant`() {
        val calls = listOf<(GoOnlineFlow) -> DutyEffect>(
            { it.onDisclosureAccepted(false) },
            { it.onGoOnlineTapped(false, false) },
            { it.onServerAvailability(true) },
            { it.onDisclosureAccepted(false) },
            { it.onPermissionResult(granted = false, canAskAgain = true) },
            { it.onServerAvailability(true) },
            { it.onGoOnlineTapped(false, true) },
            { it.onDisclosureDeclined(); DutyEffect.None },
            { it.onPermissionResult(granted = true, canAskAgain = true) },
            { it.onGoOnlineTapped(false, false) },
            { it.onDisclosureAccepted(false) },
            { it.onPermissionResult(granted = true, canAskAgain = true) },
            { it.onServerAvailability(true) },
        )
        val flow = GoOnlineFlow()
        var disclosed = false
        var granted = false
        var started = 0
        for (call in calls) {
            val before = flow.state
            val effect = call(flow)
            if (flow.state == DutyState.ShowingDisclosure) disclosed = true
            if (effect == DutyEffect.RequestPermission) {
                assertThat(disclosed).isTrue()
                disclosed = false
            }
            if (before == DutyState.AwaitingPermission && effect == DutyEffect.SetServerOnline) granted = true
            if (effect == DutyEffect.StartService) {
                assertThat(granted).isTrue()
                started++
            }
        }
        assertThat(started).isEqualTo(1)
    }

    @Test
    fun `a denial never reaches the server or the service`() {
        val flow = GoOnlineFlow()
        flow.onGoOnlineTapped(false, false)
        flow.onDisclosureAccepted(false)

        assertThat(flow.onPermissionResult(granted = false, canAskAgain = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.PermissionDenied(canAskAgain = false))
        assertThat(flow.onServerAvailability(accepted = true)).isEqualTo(DutyEffect.None)
    }

    @Test
    fun `granted and disclosed before goes straight to the server`() {
        val flow = GoOnlineFlow()
        assertThat(flow.onGoOnlineTapped(permissionGranted = true, disclosureAccepted = true)).isEqualTo(DutyEffect.SetServerOnline)
    }

    @Test
    fun `a server refusal and a service stop land offline with their reason`() {
        val refused = GoOnlineFlow()
        refused.onGoOnlineTapped(true, true)
        assertThat(refused.onServerAvailability(accepted = false)).isEqualTo(DutyEffect.None)
        assertThat(refused.state).isEqualTo(DutyState.Offline(OfflineReason.SERVER_REFUSED))

        val online = GoOnlineFlow()
        online.onGoOnlineTapped(true, true)
        online.onServerAvailability(true)
        assertThat(online.onGoOfflineTapped()).isEqualTo(DutyEffect.StopService)
        online.onServiceStopped(OfflineReason.TOGGLED_OFF)
        assertThat(online.state).isEqualTo(DutyState.Offline())

        online.onServiceRunning()
        online.onServiceStopped(OfflineReason.NO_FIX)
        assertThat(online.state).isEqualTo(DutyState.Offline(OfflineReason.NO_FIX))
    }
}
