package com.us.android.core.maps

import android.content.pm.PackageManager
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.google.android.gms.maps.CameraUpdateFactory
import com.google.android.gms.maps.model.CameraPosition
import com.google.android.gms.maps.model.LatLng
import com.google.android.gms.maps.model.LatLngBounds
import com.google.maps.android.compose.GoogleMap
import com.google.maps.android.compose.MapProperties
import com.google.maps.android.compose.MapUiSettings
import com.google.maps.android.compose.Marker
import com.google.maps.android.compose.MarkerState
import com.google.maps.android.compose.Circle
import com.google.maps.android.compose.rememberCameraPositionState
import com.us.android.core.designsystem.theme.UsTheme

/** Displays only the points already authorised by the product. Never requests device location. */
@Composable
fun LocationMap(pins: List<MapPin>, modifier: Modifier = Modifier, radiusMeters: Double? = null, onPin: ((MapPin) -> Unit)? = null) {
    val context = LocalContext.current
    val configured = remember(context) {
        @Suppress("DEPRECATION")
        val info = context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
        !info.metaData?.getString("com.google.android.geo.API_KEY").isNullOrBlank()
    }
    val validPins = pins.filter { it.valid }
    if (!configured) {
        Column(modifier.padding(16.dp)) {
            Text("Map unavailable", style = MaterialTheme.typography.titleSmall)
            Text("You can still use the address and tracking details. Google Maps needs this app's configured key.", style = MaterialTheme.typography.bodySmall)
        }
        return
    }
    val first = validPins.firstOrNull()?.let { LatLng(it.latitude, it.longitude) } ?: LatLng(20.5937, 78.9629)
    val camera = rememberCameraPositionState { position = CameraPosition.fromLatLngZoom(first, if (validPins.isEmpty()) 5f else 16f) }
    var loaded by remember { mutableStateOf(false) }
    var fitted by remember { mutableStateOf(false) }
    LaunchedEffect(loaded, validPins, onPin != null) {
        // Keep a live tracking refresh from constantly stealing the user's camera.
        if (loaded && validPins.isNotEmpty() && (!fitted || onPin != null)) {
            if (validPins.size == 1) camera.move(CameraUpdateFactory.newLatLngZoom(first, 16f))
            else {
                val bounds = LatLngBounds.builder()
                validPins.forEach { bounds.include(LatLng(it.latitude, it.longitude)) }
                camera.move(CameraUpdateFactory.newLatLngBounds(bounds.build(), 80))
            }
            fitted = true
        }
    }
    Box(modifier) {
        GoogleMap(
            modifier = Modifier.fillMaxSize(), cameraPositionState = camera,
            properties = MapProperties(isMyLocationEnabled = false),
            uiSettings = MapUiSettings(myLocationButtonEnabled = false, mapToolbarEnabled = false),
            onMapLoaded = { loaded = true },
            onMapClick = { onPin?.invoke(MapPin(it.latitude, it.longitude, "Selected location")) },
        ) {
            validPins.forEach { pin ->
                val state = remember(pin.label) { MarkerState(position = LatLng(pin.latitude, pin.longitude)) }
                state.position = LatLng(pin.latitude, pin.longitude)
                Marker(state = state, title = pin.label)
            }
            if (radiusMeters != null && radiusMeters.isFinite() && radiusMeters > 0 && validPins.isNotEmpty()) {
                Circle(center = first, radius = radiusMeters, fillColor = UsTheme.extended.accentSolid.copy(alpha = .12f), strokeColor = UsTheme.extended.accentSolid, strokeWidth = 2f)
            }
        }
    }
}
