package com.us.android.feature.mopedu.rider.map

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.mutableStateOf
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.maps.LocationMap
import com.us.android.core.maps.MapPin
import com.us.android.core.maps3d.Map3DPreview
import com.us.android.core.maps3d.maps3DConfigured
import com.us.android.core.mobility.model.GeoPoint

/** Compatibility name for the real Google Maps surface; no artificial route line. */
@Composable
fun RideMapPlaceholder(
    pickup: GeoPoint?,
    drop: GeoPoint?,
    captainLocation: GeoPoint? = null,
    modifier: Modifier = Modifier,
) {
    var preview3D by remember { mutableStateOf(false) }
    val canPreview = maps3DConfigured() && pickup != null && captainLocation == null
    Box(modifier) {
    if (preview3D && canPreview && pickup != null) Map3DPreview(pickup.lat, pickup.lng, Modifier.matchParentSize()) else
    LocationMap(
        pins = listOfNotNull(
            pickup?.let { MapPin(it.lat, it.lng, "Pickup") },
            drop?.let { MapPin(it.lat, it.lng, "Destination") },
            captainLocation?.let { MapPin(it.lat, it.lng, "Captain") },
        ),
        modifier = Modifier.matchParentSize(),
    )
    if (canPreview) TextButton(onClick = { preview3D = !preview3D }, modifier = Modifier.align(Alignment.TopEnd).padding(8.dp)) {
        Text(if (preview3D) "Standard map" else "3D preview · experimental")
    }
    }
}
