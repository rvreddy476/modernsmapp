package com.us.android.core.maps

data class MapPin(val latitude: Double, val longitude: Double, val label: String) {
    val valid: Boolean get() = latitude.isFinite() && longitude.isFinite() && latitude in -90.0..90.0 && longitude in -180.0..180.0
}
