package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.mopedu.captain.home.DutyEffect
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.home.GoOnlineFlow
import com.us.android.feature.mopedu.captain.location.OfflineReason
import org.junit.Test

class GoOnlineFlowTest {

    private val flow = GoOnlineFlow()

    @Test
    fun `the only path to the permission prompt runs through the disclosure`() {
        assertThat(flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.ShowingDisclosure)
        assertThat(flow.onDisclosureAccepted(permissionGranted = false)).isEqualTo(DutyEffect.RequestPermission)
        assertThat(flow.state).isEqualTo(DutyState.AwaitingPermission)
        // Out of order: a second tap while the prompt is up is ignored.
        assertThat(flow.onGoOnlineTapped(permissionGranted = true, disclosureAccepted = true)).isEqualTo(DutyEffect.None)
    }

    @Test
    fun `the only path to the service runs through a grant and the server's yes`() {
        flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)
        flow.onDisclosureAccepted(permissionGranted = false)
        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(DutyEffect.SetServerOnline)
        assertThat(flow.state).isEqualTo(DutyState.GoingOnline)
        assertThat(flow.onServerOnline(accepted = true)).isEqualTo(DutyEffect.StartService)
        assertThat(flow.state).isEqualTo(DutyState.Online)
        assertThat(flow.onGoOfflineTapped()).isEqualTo(DutyEffect.StopService)
        flow.onServiceStopped(OfflineReason.TOGGLED_OFF)
        assertThat(flow.state).isEqualTo(DutyState.Offline())
    }

    @Test
    fun `a denial, a decline and a refusing server all stay offline`() {
        flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)
        flow.onDisclosureDeclined()
        assertThat(flow.state).isEqualTo(DutyState.Offline())

        flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = true)
        flow.onDisclosureAccepted(permissionGranted = false)
        assertThat(flow.onPermissionResult(granted = false, canAskAgain = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.PermissionDenied(canAskAgain = false))

        assertThat(flow.onGoOnlineTapped(permissionGranted = true, disclosureAccepted = true)).isEqualTo(DutyEffect.SetServerOnline)
        assertThat(flow.onServerOnline(accepted = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.Offline(OfflineReason.SERVER_REFUSED))
    }

    @Test
    fun `already granted and already disclosed goes straight to the server, and a running service is adopted`() {
        assertThat(flow.onGoOnlineTapped(permissionGranted = true, disclosureAccepted = true)).isEqualTo(DutyEffect.SetServerOnline)
        val fresh = GoOnlineFlow()
        fresh.onServiceRunning()
        assertThat(fresh.state).isEqualTo(DutyState.Online)
        fresh.onServiceStopped(OfflineReason.NO_FIX)
        assertThat(fresh.state).isEqualTo(DutyState.Offline(OfflineReason.NO_FIX))
    }
}
