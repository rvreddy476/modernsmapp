package com.us.android.core.location

import com.us.android.core.mobility.model.CaptainTelemetry
import com.us.android.core.mobility.model.GeoPoint
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * High-accuracy location tracker with battery-efficient updates and mock provider support.
 */
interface LocationTracker {
    val locationFlow: StateFlow<GeoPoint>
    val telemetryFlow: StateFlow<CaptainTelemetry>
    fun updateLocation(lat: Double, lng: Double, address: String = "")
}

@Singleton
class MopeduLocationTracker @Inject constructor() : LocationTracker {
    // Default to Hyderabad central coordinates for pilot seed
    private val defaultPoint = GeoPoint(17.4485, 78.3748, "HITEC City, Hyderabad")
    private val _locationFlow = MutableStateFlow(defaultPoint)
    override val locationFlow: StateFlow<GeoPoint> = _locationFlow.asStateFlow()

    private var sequenceNum: Long = 0
    private val _telemetryFlow = MutableStateFlow(
        CaptainTelemetry(
            lat = defaultPoint.lat,
            lng = defaultPoint.lng,
            speedMps = 0.0,
            heading = 0.0,
            accuracyMeters = 5.0,
            sequence = 0L,
            timestampEpochMs = System.currentTimeMillis(),
        )
    )
    override val telemetryFlow: StateFlow<CaptainTelemetry> = _telemetryFlow.asStateFlow()

    override fun updateLocation(lat: Double, lng: Double, address: String) {
        sequenceNum++
        val point = GeoPoint(lat, lng, address)
        _locationFlow.value = point
        _telemetryFlow.value = CaptainTelemetry(
            lat = lat,
            lng = lng,
            speedMps = 5.5,
            heading = 90.0,
            accuracyMeters = 4.0,
            sequence = sequenceNum,
            timestampEpochMs = System.currentTimeMillis(),
        )
    }
}
