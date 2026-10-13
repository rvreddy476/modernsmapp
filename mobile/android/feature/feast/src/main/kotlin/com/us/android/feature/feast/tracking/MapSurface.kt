package com.us.android.feature.feast.tracking

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.maps.LocationMap
import com.us.android.core.maps.MapPin

data class MapPoint(val latitude: Double, val longitude: Double)
interface MapSurface {
    @Composable fun Content(rider: MapPoint?, destination: MapPoint?, modifier: Modifier)
}
object GoogleMapSurface : MapSurface {
    @Composable
    override fun Content(rider: MapPoint?, destination: MapPoint?, modifier: Modifier) {
        LocationMap(
            pins = listOfNotNull(
                rider?.let { MapPin(it.latitude, it.longitude, "Delivery partner") },
                destination?.let { MapPin(it.latitude, it.longitude, "Delivery address") },
            ),
            modifier = modifier.fillMaxWidth().height(260.dp),
        )
    }
}
val LocalMapSurface = staticCompositionLocalOf<MapSurface> { GoogleMapSurface }
