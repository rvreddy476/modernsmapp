package com.us.android.feature.feast.tracking

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/** A point on a map. */
data class MapPoint(val latitude: Double, val longitude: Double)

/**
 * The seam a live map drops into.
 *
 * `maps-compose` is NOT in the offline Gradle cache, so Momentum ships no map
 * today: [PlaceholderMapSurface] is the only implementation. When the library
 * arrives, provide a GoogleMap-backed [MapSurface] through [LocalMapSurface] in
 * `:app` (with Momentum's MAPS_API_KEY) — the tracking screen does not change.
 */
interface MapSurface {
    @Composable
    fun Content(rider: MapPoint?, destination: MapPoint?, modifier: Modifier)
}

object PlaceholderMapSurface : MapSurface {
    @Composable
    override fun Content(rider: MapPoint?, destination: MapPoint?, modifier: Modifier) {
        val shape = RoundedCornerShape(UsTheme.radii.large)
        Column(
            modifier = modifier
                .fillMaxWidth()
                .height(132.dp)
                .background(UsTheme.extended.bgRaised, shape)
                .border(1.dp, UsTheme.extended.borderSubtle, shape)
                .padding(UsTheme.spacing.xxl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.textDim, modifier = Modifier.size(24.dp))
            Text(
                text = if (rider != null) "Your delivery partner is on the move" else "Live map coming soon",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textSecondary,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            )
        }
    }
}

val LocalMapSurface = staticCompositionLocalOf<MapSurface> { PlaceholderMapSurface }
