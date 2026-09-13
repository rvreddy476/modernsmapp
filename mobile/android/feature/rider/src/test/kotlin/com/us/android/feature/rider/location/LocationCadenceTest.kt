package com.us.android.feature.rider.location

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class LocationCadenceTest {

    private var now = 0L
    private val cadence = LocationCadence(clock = { now })

    private fun at(seconds: Double) {
        now = (seconds * 1_000).toLong()
    }

    /** A fix [northMeters] north of the origin, stamped at the current time. */
    private fun fix(northMeters: Double = 0.0, heading: Double? = null) = LocationFix(
        latitude = BASE_LAT + northMeters / METERS_PER_DEGREE,
        longitude = BASE_LNG,
        accuracyMeters = 5.0,
        headingDegrees = heading,
        atMillis = now,
    )

    @Test
    fun `idle and on-job use their own intervals, distances and accuracy`() {
        assertThat(cadence.start(onJob = false)).containsExactly(CadenceEffect.Request(CadenceSpec.IDLE))
        assertThat(CadenceSpec.IDLE.intervalMillis).isEqualTo(30_000)
        assertThat(CadenceSpec.IDLE.minDistanceMeters).isEqualTo(50.0)
        assertThat(CadenceSpec.IDLE.heartbeatMillis).isEqualTo(60_000)
        assertThat(CadenceSpec.IDLE.highAccuracy).isFalse()

        assertThat(cadence.onJobChanged(onJob = true)).containsExactly(CadenceEffect.Request(CadenceSpec.ON_JOB))
        assertThat(CadenceSpec.ON_JOB.intervalMillis).isEqualTo(8_000)
        assertThat(CadenceSpec.ON_JOB.minDistanceMeters).isEqualTo(15.0)
        assertThat(CadenceSpec.ON_JOB.highAccuracy).isTrue()

        assertThat(cadence.onJobChanged(onJob = true)).isEmpty()
        assertThat(cadence.onJobChanged(onJob = false)).containsExactly(CadenceEffect.Request(CadenceSpec.IDLE))
    }

    @Test
    fun `idle pings need thirty seconds and fifty metres`() {
        cadence.start(onJob = false)
        at(0.0)
        assertThat(cadence.onFix(fix())).hasSize(1)

        at(10.0)
        assertThat(cadence.onFix(fix(northMeters = 100.0))).isEmpty() // moved, too soon

        at(30.0)
        assertThat(cadence.onFix(fix(northMeters = 20.0))).isEmpty() // due, not far enough

        at(31.0)
        val moved = fix(northMeters = 120.0)
        assertThat(cadence.onFix(moved)).containsExactly(CadenceEffect.Ping(moved))
    }

    @Test
    fun `on a job pings need only eight seconds and fifteen metres`() {
        cadence.start(onJob = true)
        cadence.onFix(fix())

        at(8.0)
        val moved = fix(northMeters = 20.0)
        assertThat(cadence.onFix(moved)).containsExactly(CadenceEffect.Ping(moved))

        at(16.0)
        assertThat(cadence.onFix(fix(northMeters = 25.0))).isEmpty() // 5 m
    }

    @Test
    fun `a stationary idle rider still pings every sixty seconds`() {
        cadence.start(onJob = false)
        assertThat(cadence.onFix(fix())).hasSize(1)
        at(30.0)
        assertThat(cadence.onFix(fix())).isEmpty()
        at(59.0)
        assertThat(cadence.onFix(fix())).isEmpty()
        at(60.0)
        val heartbeat = fix()
        assertThat(cadence.onFix(heartbeat)).containsExactly(CadenceEffect.Ping(heartbeat))
    }

    @Test
    fun `two minutes without a fix takes the rider offline`() {
        cadence.start(onJob = false)
        at(10.0)
        cadence.onFix(fix())

        at(129.9)
        assertThat(cadence.onTick()).isEmpty()
        assertThat(cadence.online).isTrue()

        at(130.0)
        assertThat(cadence.onTick()).containsExactly(CadenceEffect.GoOffline(OfflineReason.NO_FIX))
        assertThat(cadence.online).isFalse()
        assertThat(cadence.offlineReason).isEqualTo(OfflineReason.NO_FIX)

        // Once offline, nothing more is sent.
        at(131.0)
        assertThat(cadence.onFix(fix())).isEmpty()
        assertThat(cadence.onTick()).isEmpty()
    }

    @Test
    fun `a first fix that never arrives also times out`() {
        cadence.start(onJob = true)
        at(119.0)
        assertThat(cadence.onTick()).isEmpty()
        at(120.0)
        assertThat(cadence.onTick()).containsExactly(CadenceEffect.GoOffline(OfflineReason.NO_FIX))
    }

    @Test
    fun `a revoked permission, a sign-out and the toggle each go offline exactly once`() {
        cadence.start(onJob = false)
        assertThat(cadence.onPermissionRevoked()).containsExactly(CadenceEffect.GoOffline(OfflineReason.PERMISSION_REVOKED))
        assertThat(cadence.online).isFalse()
        assertThat(cadence.onPermissionRevoked()).isEmpty()

        cadence.start(onJob = false)
        assertThat(cadence.onSignedOut()).containsExactly(CadenceEffect.GoOffline(OfflineReason.SIGNED_OUT))

        cadence.start(onJob = false)
        assertThat(cadence.onToggledOff()).containsExactly(CadenceEffect.GoOffline(OfflineReason.TOGGLED_OFF))
    }

    @Test
    fun `only the latest fix counts, an older one is dropped`() {
        cadence.start(onJob = true)
        at(20.0)
        cadence.onFix(fix())
        val stale = fix(northMeters = 500.0).copy(atMillis = 5_000)
        at(40.0)
        assertThat(cadence.onFix(stale)).isEmpty()
    }

    @Test
    fun `accepting a job sends the latest fix at once`() {
        cadence.start(onJob = false)
        val first = fix()
        cadence.onFix(first)
        at(2.0)
        val latest = fix(northMeters = 3.0)
        assertThat(cadence.onFix(latest)).isEmpty()

        assertThat(cadence.onJobChanged(onJob = true))
            .containsExactly(CadenceEffect.Request(CadenceSpec.ON_JOB), CadenceEffect.Ping(latest)).inOrder()
    }

    @Test
    fun `a ping carries a normalised heading only when the fix has one`() {
        assertThat(fix(heading = 90.0).toRequest().heading).isEqualTo(90.0)
        assertThat(fix(heading = 450.0).toRequest().heading).isEqualTo(90.0)
        assertThat(fix(heading = -90.0).toRequest().heading).isEqualTo(270.0)
        assertThat(fix(heading = null).toRequest().heading).isNull()
        assertThat(fix(heading = Double.NaN).toRequest().heading).isNull()
        assertThat(fix().toRequest().accuracyMeters).isEqualTo(5.0)
    }

    @Test
    fun `distance is haversine metres`() {
        assertThat(LocationCadence.distanceMeters(fix(), fix(northMeters = 100.0))).isWithin(0.5).of(100.0)
    }

    private companion object {
        const val BASE_LAT = 12.9716
        const val BASE_LNG = 77.5946
        /** Metres per degree of latitude on the 6 371 km sphere the cadence uses. */
        const val METERS_PER_DEGREE = 6_371_000.0 * Math.PI / 180.0
    }
}
