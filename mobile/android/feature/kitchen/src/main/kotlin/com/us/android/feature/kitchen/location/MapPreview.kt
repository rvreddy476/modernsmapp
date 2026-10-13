package com.us.android.feature.kitchen.location

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.maps.LocationMap
import com.us.android.core.maps.MapPin

interface MapPreview {
    @Composable fun Content(coordinates: Coordinates?, radiusKm: Float, modifier: Modifier)
}
object GoogleMapPreview : MapPreview {
    @Composable
    override fun Content(coordinates: Coordinates?, radiusKm: Float, modifier: Modifier) {
        LocationMap(
            pins = listOfNotNull(coordinates?.let { MapPin(it.latitude, it.longitude, "Kitchen") }),
            radiusMeters = radiusKm.toDouble() * 1000,
            modifier = modifier.fillMaxWidth().height(260.dp),
        )
    }
}
val LocalMapPreview = staticCompositionLocalOf<MapPreview> { GoogleMapPreview }
