package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.location.CadenceEffect
import com.us.android.feature.doorsteppro.location.CadenceSpec
import com.us.android.feature.doorsteppro.location.DutyEffect
import com.us.android.feature.doorsteppro.location.DutyState
import com.us.android.feature.doorsteppro.location.GoOnDutyFlow
import com.us.android.feature.doorsteppro.location.LocationFix
import com.us.android.feature.doorsteppro.location.OfflineReason
import com.us.android.feature.doorsteppro.location.ProCadence
import com.us.android.feature.doorsteppro.location.toRequest
import org.junit.Test

/** The on-duty cadence state machine (Feast Rider's, copied) and the go-on-duty order. */
class ProCadenceTest {

    private var now = 0L
    private val cadence = ProCadence(clock = { now })

    private fun at(seconds: Double) {
        now = (seconds * 1_000).toLong()
    }

    private fun fix(northMeters: Double = 0.0) = LocationFix(
        latitude = BASE_LAT + northMeters / METERS_PER_DEGREE,
        longitude = BASE_LNG,
        accuracyMeters = 5.0,
        atMillis = now,
    )

    @Test
    fun `on duty and travelling use the rider cadence`() {
        assertThat(cadence.start(onJob = false)).containsExactly(CadenceEffect.Request(CadenceSpec.IDLE))
        assertThat(CadenceSpec.IDLE.intervalMillis).isEqualTo(30_000)
        assertThat(CadenceSpec.IDLE.minDistanceMeters).isEqualTo(50.0)
        assertThat(CadenceSpec.IDLE.heartbeatMillis).isEqualTo(60_000)
        assertThat(CadenceSpec.ON_JOB.intervalMillis).isEqualTo(8_000)
        assertThat(CadenceSpec.ON_JOB.minDistanceMeters).isEqualTo(15.0)
        assertThat(CadenceSpec.ON_JOB.highAccuracy).isTrue()
        assertThat(cadence.onJobChanged(onJob = true)).containsExactly(CadenceEffect.Request(CadenceSpec.ON_JOB))
        assertThat(cadence.onJobChanged(onJob = true)).isEmpty()
    }

    @Test
    fun `idle pings need thirty seconds and fifty metres, or the sixty-second heartbeat`() {
        cadence.start(onJob = false)
        assertThat(cadence.onFix(fix())).hasSize(1)
        at(10.0)
        assertThat(cadence.onFix(fix(northMeters = 100.0))).isEmpty()
        at(30.0)
        assertThat(cadence.onFix(fix(northMeters = 20.0))).isEmpty()
        at(31.0)
        val moved = fix(northMeters = 120.0)
        assertThat(cadence.onFix(moved)).containsExactly(CadenceEffect.Ping(moved))
        at(91.0)
        val still = fix(northMeters = 120.0)
        assertThat(cadence.onFix(still)).containsExactly(CadenceEffect.Ping(still))
    }

    @Test
    fun `starting to travel sends the latest fix at once`() {
        cadence.start(onJob = false)
        cadence.onFix(fix())
        at(2.0)
        val latest = fix(northMeters = 3.0)
        assertThat(cadence.onFix(latest)).isEmpty()
        assertThat(cadence.onJobChanged(onJob = true))
            .containsExactly(CadenceEffect.Request(CadenceSpec.ON_JOB), CadenceEffect.Ping(latest)).inOrder()
    }

    @Test
    fun `two minutes without a fix, a revoked permission, a sign-out or the server going off duty each stop it once`() {
        cadence.start(onJob = false)
        at(119.9)
        assertThat(cadence.onTick()).isEmpty()
        at(120.0)
        assertThat(cadence.onTick()).containsExactly(CadenceEffect.GoOffline(OfflineReason.NO_FIX))
        assertThat(cadence.onTick()).isEmpty()
        assertThat(cadence.onFix(fix())).isEmpty()

        cadence.start(onJob = false)
        assertThat(cadence.onServerRefused()).containsExactly(CadenceEffect.GoOffline(OfflineReason.SERVER_REFUSED))
        assertThat(cadence.onServerRefused()).isEmpty()
        cadence.start(onJob = false)
        assertThat(cadence.onPermissionRevoked()).containsExactly(CadenceEffect.GoOffline(OfflineReason.PERMISSION_REVOKED))
        cadence.start(onJob = true)
        assertThat(cadence.onSignedOut()).containsExactly(CadenceEffect.GoOffline(OfflineReason.SIGNED_OUT))
        cadence.start(onJob = true)
        assertThat(cadence.onToggledOff()).containsExactly(CadenceEffect.GoOffline(OfflineReason.TOGGLED_OFF))
    }

    @Test
    fun `an older fix is dropped, and a ping carries lat, lng and a real accuracy only`() {
        cadence.start(onJob = true)
        at(20.0)
        cadence.onFix(fix())
        at(40.0)
        assertThat(cadence.onFix(fix(northMeters = 500.0).copy(atMillis = 5_000))).isEmpty()

        val request = fix().toRequest()
        assertThat(request.lat).isEqualTo(BASE_LAT)
        assertThat(request.accuracyM).isEqualTo(5.0)
        assertThat(fix().copy(accuracyMeters = Double.NaN).toRequest().accuracyM).isNull()
        assertThat(fix().copy(accuracyMeters = -1.0).toRequest().accuracyM).isNull()
    }

    @Test
    fun `going on duty is disclosure, then permission, then the server, then the service — in that order only`() {
        val flow = GoOnDutyFlow()
        assertThat(flow.onGoOnlineTapped(permissionGranted = false, disclosureAccepted = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.ShowingDisclosure)
        // Out of order: the server answer before any request is ignored.
        assertThat(flow.onServerAvailability(accepted = true)).isEqualTo(DutyEffect.None)
        assertThat(flow.onDisclosureAccepted(permissionGranted = false)).isEqualTo(DutyEffect.RequestPermission)
        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(DutyEffect.SetServerOnline)
        assertThat(flow.onServerAvailability(accepted = true)).isEqualTo(DutyEffect.StartService)
        assertThat(flow.state).isEqualTo(DutyState.Online)
        assertThat(flow.onGoOfflineTapped()).isEqualTo(DutyEffect.StopService)
        flow.onServiceStopped(OfflineReason.TOGGLED_OFF)
        assertThat(flow.state).isEqualTo(DutyState.Offline(null))

        // Refused by the server (not approved, suspended, no valid police certificate): no service.
        flow.onGoOnlineTapped(permissionGranted = true, disclosureAccepted = true)
        assertThat(flow.onServerAvailability(accepted = false)).isEqualTo(DutyEffect.None)
        assertThat(flow.state).isEqualTo(DutyState.Offline(OfflineReason.SERVER_REFUSED))
    }

    private companion object {
        const val BASE_LAT = 17.4401
        const val BASE_LNG = 78.3489
        const val METERS_PER_DEGREE = 6_371_000.0 * Math.PI / 180.0
    }
}
