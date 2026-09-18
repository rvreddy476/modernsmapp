package com.us.android.feature.mopedu.rider.map

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.GeoPoint

/**
 * The ride map's stand-in: a stylised road grid with the pickup, drop and
 * captain markers, drawn from Momentum tokens so it reads in both themes.
 *
 * `maps-compose` is NOT in the offline Gradle cache, so no live map ships
 * today — the same seam Feast's tracking screen uses (`PlaceholderMapSurface`).
 * The markers sit at fixed fractions of the canvas rather than at projected
 * coordinates; when the library arrives this composable is replaced, the
 * cards above it do not change.
 */
@Composable
fun RideMapPlaceholder(
    pickup: GeoPoint?,
    drop: GeoPoint?,
    captainLocation: GeoPoint? = null,
    modifier: Modifier = Modifier,
) {
    val ground = UsTheme.extended.bgRaised
    val road = UsTheme.extended.borderSubtle
    val pickupColor = UsTheme.extended.statusSuccess
    val dropColor = UsTheme.extended.statusDanger
    val captainColor = UsTheme.extended.accentSolid

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(ground),
        contentAlignment = Alignment.TopCenter,
    ) {
        Canvas(modifier = Modifier.fillMaxSize()) {
            val width = size.width
            val height = size.height

            // The city grid.
            val gridStep = GRID_STEP.toPx()
            val gridStroke = GRID_STROKE.toPx()
            var x = 0f
            while (x < width) {
                drawLine(road, Offset(x, 0f), Offset(x, height), gridStroke)
                x += gridStep
            }
            var y = 0f
            while (y < height) {
                drawLine(road, Offset(0f, y), Offset(width, y), gridStroke)
                y += gridStep
            }

            val pickupAt = Offset(width * PICKUP_X, height * PICKUP_Y)
            val dropAt = Offset(width * DROP_X, height * DROP_Y)

            if (pickup != null && drop != null) {
                drawLine(
                    color = pickupColor,
                    start = pickupAt,
                    end = dropAt,
                    strokeWidth = ROUTE_STROKE.toPx(),
                    pathEffect = PathEffect.dashPathEffect(floatArrayOf(DASH_ON, DASH_OFF), 0f),
                )
            }
            if (pickup != null) {
                drawCircle(pickupColor.copy(alpha = HALO_ALPHA), HALO_RADIUS.toPx(), pickupAt)
                drawCircle(pickupColor, MARKER_RADIUS.toPx(), pickupAt)
            }
            if (drop != null) {
                drawCircle(dropColor.copy(alpha = HALO_ALPHA), HALO_RADIUS.toPx(), dropAt)
                drawCircle(dropColor, MARKER_RADIUS.toPx(), dropAt)
            }
            if (captainLocation != null) {
                val captainAt = Offset(width * CAPTAIN_X, height * CAPTAIN_Y)
                drawCircle(captainColor.copy(alpha = HALO_ALPHA), HALO_RADIUS.toPx(), captainAt)
                drawCircle(captainColor, CAPTAIN_RADIUS.toPx(), captainAt)
            }
        }
        Text(
            text = "Live map coming soon",
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textDim,
            modifier = Modifier.padding(top = UsTheme.spacing.l),
        )
    }
}

private val GRID_STEP = 60.dp
private val GRID_STROKE = 1.dp
private val ROUTE_STROKE = 4.dp
private val HALO_RADIUS = 20.dp
private val MARKER_RADIUS = 8.dp
private val CAPTAIN_RADIUS = 9.dp
private const val HALO_ALPHA = 0.3f
private const val DASH_ON = 15f
private const val DASH_OFF = 10f
private const val PICKUP_X = 0.3f
private const val PICKUP_Y = 0.65f
private const val DROP_X = 0.7f
private const val DROP_Y = 0.35f
private const val CAPTAIN_X = 0.45f
private const val CAPTAIN_Y = 0.55f
