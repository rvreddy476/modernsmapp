package com.us.android.feature.kitchen.location

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
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The seam a real map drops into.
 *
 * `maps-compose` and Places are NOT in the offline Gradle cache, so the Kitchen
 * app ships no map: [UnavailableMapPreview] is the only implementation. When
 * the libraries arrive, provide a GoogleMap-backed [MapPreview] through
 * [LocalMapPreview] in :app-kitchen (which already carries the per-app
 * MAPS_API_KEY) — the location step itself does not change.
 */
interface MapPreview {
    @Composable
    fun Content(coordinates: Coordinates?, radiusKm: Float, modifier: Modifier)
}

object UnavailableMapPreview : MapPreview {
    @Composable
    override fun Content(coordinates: Coordinates?, radiusKm: Float, modifier: Modifier) {
        val shape = RoundedCornerShape(UsTheme.radii.panel)
        Column(
            modifier = modifier
                .fillMaxWidth()
                .height(140.dp)
                .background(UsTheme.extended.bgRaised, shape)
                .border(1.dp, UsTheme.extended.borderSubtle, shape)
                .padding(UsTheme.spacing.xxl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Icon(
                imageVector = UsIcons.MapPin,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier.size(24.dp),
            )
            Text(
                text = "Map preview unavailable",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            )
            Text(
                text = coordinates?.let { "Pinned at %.5f, %.5f · %.1f km".format(it.latitude, it.longitude, radiusKm) }
                    ?: "Use your current location to pin the kitchen",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
    }
}

val LocalMapPreview = staticCompositionLocalOf<MapPreview> { UnavailableMapPreview }
