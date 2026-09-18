package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.mopedu.captain.location.CadenceEffect
import com.us.android.feature.mopedu.captain.location.CadenceSpec
import com.us.android.feature.mopedu.captain.location.CaptainCadence
import com.us.android.feature.mopedu.captain.location.LocationFix
import com.us.android.feature.mopedu.captain.location.OfflineReason
import com.us.android.feature.mopedu.captain.location.toRequest
import org.junit.Test

class CaptainCadenceTest {

    private var now = 0L
    private val cadence = CaptainCadence(clock = { now })

    private fun fix(lat: Double = 17.44, lng: Double = 78.35, at: Long = now, heading: Double? = null) =
        LocationFix(latitude = lat, longitude = lng, headingDegrees = heading, atMillis = at)

    @Test
    fun `going on duty requests idle updates and pings the first fix`() {
        assertThat(cadence.start(onRide = false)).containsExactly(CadenceEffect.Request(CadenceSpec.IDLE))
        val effects = cadence.onFix(fix())
        assertThat(effects).hasSize(1)
        val ping = effects.single() as CadenceEffect.Ping
        assertThat(ping.sequence).isEqualTo(1L)
    }

    @Test
    fun `a fix inside the interval that has not moved is not pinged, a heartbeat is`() {
        cadence.start(onRide = false)
        cadence.onFix(fix())
        now = 10_000
        assertThat(cadence.onFix(fix())).isEmpty()
        now = 61_000
        assertThat(cadence.onFix(fix()).single()).isInstanceOf(CadenceEffect.Ping::class.java)
    }

    @Test
    fun `a moved fix after the interval is pinged, and pings are numbered`() {
        cadence.start(onRide = false)
        cadence.onFix(fix())
        now = 31_000
        val ping = cadence.onFix(fix(lat = 17.441)).single() as CadenceEffect.Ping // ~110 m north
        assertThat(ping.sequence).isEqualTo(2L)
    }

    @Test
    fun `a ride switches to the ride cadence and re-pings the latest fix`() {
        cadence.start(onRide = false)
        cadence.onFix(fix())
        val effects = cadence.onRideChanged(true)
        assertThat(effects.first()).isEqualTo(CadenceEffect.Request(CadenceSpec.ON_RIDE))
        assertThat(effects[1]).isInstanceOf(CadenceEffect.Ping::class.java)
        assertThat(cadence.onRideChanged(true)).isEmpty()
    }

    @Test
    fun `an out-of-order fix is dropped`() {
        cadence.start(onRide = false)
        cadence.onFix(fix(at = 100))
        now = 61_000
        assertThat(cadence.onFix(fix(at = 50))).isEmpty()
    }

    @Test
    fun `two minutes without a fix goes offline, and nothing fires once offline`() {
        cadence.start(onRide = false)
        now = 119_999
        assertThat(cadence.onTick()).isEmpty()
        now = 120_000
        assertThat(cadence.onTick()).containsExactly(CadenceEffect.GoOffline(OfflineReason.NO_FIX))
        assertThat(cadence.online).isFalse()
        assertThat(cadence.offlineReason).isEqualTo(OfflineReason.NO_FIX)
        assertThat(cadence.onFix(fix())).isEmpty()
        assertThat(cadence.onToggledOff()).isEmpty()
    }

    @Test
    fun `toggling off, signing out and a revoked permission each carry their reason`() {
        cadence.start(onRide = false)
        assertThat(cadence.onToggledOff()).containsExactly(CadenceEffect.GoOffline(OfflineReason.TOGGLED_OFF))
        cadence.start(onRide = false)
        assertThat(cadence.onSignedOut()).containsExactly(CadenceEffect.GoOffline(OfflineReason.SIGNED_OUT))
        cadence.start(onRide = false)
        assertThat(cadence.onPermissionRevoked()).containsExactly(CadenceEffect.GoOffline(OfflineReason.PERMISSION_REVOKED))
    }

    @Test
    fun `the ping request normalises the heading and carries the sequence`() {
        cadence.start(onRide = true)
        val ping = cadence.onFix(fix(heading = -90.0)).single() as CadenceEffect.Ping
        val request = ping.toRequest()
        assertThat(request.heading).isEqualTo(270.0)
        assertThat(request.sequence).isEqualTo(1L)
        assertThat(request.accuracyMeters).isNull()
        assertThat(request.lat).isEqualTo(17.44)
    }
}
