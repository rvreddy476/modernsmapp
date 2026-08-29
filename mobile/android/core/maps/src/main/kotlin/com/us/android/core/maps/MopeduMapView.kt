package com.us.android.core.maps

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.unit.dp
import com.us.android.core.mobility.model.GeoPoint

/**
 * Provider-neutral map rendering contract.
 * Renders stylized vector canvas representation of the route, markers, and active vehicle telemetry.
 */

data class MapMarker(
    val point: GeoPoint,
    val type: MarkerType,
    val title: String = "",
)

enum class MarkerType {
    PICKUP,
    DROP,
    CAPTAIN,
}

data class MapRoute(
    val waypoints: List<GeoPoint>,
    val color: Color = Color(0xFF00C853),
)

@Composable
fun MopeduMapView(
    pickup: GeoPoint?,
    drop: GeoPoint?,
    captainLocation: GeoPoint? = null,
    modifier: Modifier = Modifier,
    route: MapRoute? = null,
) {
    val darkMapBg = Color(0xFF1E222B)
    val roadColor = Color(0xFF2C3240)
    val pickupColor = Color(0xFF00C853)
    val dropColor = Color(0xFFFF5252)
    val captainColor = Color(0xFFFFD600)

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(darkMapBg),
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.fillMaxSize()) {
            val width = size.width
            val height = size.height

            // Stylized grid background representing city road grid
            val gridStep = 60.dp.toPx()
            var x = 0f
            while (x < width) {
                drawLine(
                    color = roadColor,
                    start = Offset(x, 0f),
                    end = Offset(x, height),
                    strokeWidth = 2.dp.toPx(),
                )
                x += gridStep
            }
            var y = 0f
            while (y < height) {
                drawLine(
                    color = roadColor,
                    start = Offset(0f, y),
                    end = Offset(width, y),
                    strokeWidth = 2.dp.toPx(),
                )
                y += gridStep
            }

            // Draw route between pickup and drop
            val pX = width * 0.3f
            val pY = height * 0.65f
            val dX = width * 0.7f
            val dY = height * 0.35f

            if (pickup != null && drop != null) {
                drawLine(
                    color = pickupColor,
                    start = Offset(pX, pY),
                    end = Offset(dX, dY),
                    strokeWidth = 4.dp.toPx(),
                    pathEffect = PathEffect.dashPathEffect(floatArrayOf(15f, 10f), 0f),
                )
            }

            // Draw Pickup Marker
            if (pickup != null) {
                drawCircle(
                    color = pickupColor.copy(alpha = 0.3f),
                    radius = 20.dp.toPx(),
                    center = Offset(pX, pY),
                )
                drawCircle(
                    color = pickupColor,
                    radius = 8.dp.toPx(),
                    center = Offset(pX, pY),
                )
            }

            // Draw Dropoff Marker
            if (drop != null) {
                drawCircle(
                    color = dropColor.copy(alpha = 0.3f),
                    radius = 20.dp.toPx(),
                    center = Offset(dX, dY),
                )
                drawCircle(
                    color = dropColor,
                    radius = 8.dp.toPx(),
                    center = Offset(dX, dY),
                )
            }

            // Draw Captain Marker
            if (captainLocation != null) {
                val cX = width * 0.45f
                val cY = height * 0.55f
                drawCircle(
                    color = captainColor.copy(alpha = 0.4f),
                    radius = 18.dp.toPx(),
                    center = Offset(cX, cY),
                )
                drawCircle(
                    color = captainColor,
                    radius = 9.dp.toPx(),
                    center = Offset(cX, cY),
                )
            }
        }
    }
}
