package com.us.android.feature.chat.location

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.ChatLocation
import com.us.android.core.chat.data.LOCATION_PLACE_MAX
import com.us.android.core.chat.data.accuracyText
import com.us.android.core.chat.data.coordsText
import com.us.android.core.chat.data.geoUri
import com.us.android.core.chat.data.mapsWebUrl
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.maps.LocationMap
import com.us.android.core.maps.MapPin

/**
 * "Share location" (founder, 23 Oct 2026): rationale first, then the system
 * prompt, ONE fix, a confirm card with the map, then [onSend]. Closing the
 * sheet drops the fix.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
internal fun ShareLocationSheet(
    onSend: (ChatLocation) -> Unit,
    onDismiss: () -> Unit,
    viewModel: ShareLocationViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val activity = LocalActivity.current
    val context = LocalContext.current
    val permissionLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val canAskAgain = activity?.let {
            ActivityCompat.shouldShowRequestPermissionRationale(it, Manifest.permission.ACCESS_FINE_LOCATION)
        } ?: false
        viewModel.onPermissionResult(granted = grants.values.any { it }, canAskAgain = canAskAgain)
    }
    LaunchedEffect(viewModel) {
        viewModel.start()
        viewModel.effects.collect { effect ->
            if (effect == ShareLocationEffect.RequestPermission) {
                permissionLauncher.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
            }
        }
    }
    DisposableEffect(viewModel) { onDispose { viewModel.clear() } }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m)
                .testTag("share-location-sheet"),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = "Share location",
                style = MaterialTheme.typography.titleMedium,
                color = UsTheme.extended.textPrimary,
            )
            when (val step = state.step) {
                ShareLocationStep.Idle, ShareLocationStep.ExplainingPermission -> {
                    Note(
                        "VChat uses your location once, right now, to send where you are to this chat. " +
                            "It isn't updated after you send it, and it isn't tracked in the background.",
                    )
                    UsButton(text = "Continue", onClick = viewModel::onRationaleAccepted, modifier = Modifier.fillMaxWidth().testTag("share-location-continue"))
                    UsSecondaryButton(
                        text = "Not now",
                        onClick = {
                            viewModel.onRationaleDismissed()
                            onDismiss()
                        },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                ShareLocationStep.AwaitingPermission -> Note("Allow location access to share where you are.")
                ShareLocationStep.Locating -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    modifier = Modifier.testTag("share-location-locating"),
                ) {
                    CircularProgressIndicator(
                        color = UsTheme.extended.chatAccent,
                        strokeWidth = SPINNER_STROKE,
                        modifier = Modifier.size(SPINNER),
                    )
                    Text("Finding your location…", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                }
                is ShareLocationStep.Denied -> {
                    Note("Location is off for VChat, so your location can't be shared.", danger = true)
                    if (step.canAskAgain) {
                        UsButton(text = "Try again", onClick = viewModel::start, modifier = Modifier.fillMaxWidth())
                    } else {
                        UsButton(
                            text = "Open settings",
                            onClick = {
                                context.startActivity(
                                    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)),
                                )
                            },
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
                ShareLocationStep.Unavailable -> {
                    Note("We couldn't find your location. Check that location is on, then try again.", danger = true)
                    UsButton(text = "Try again", onClick = viewModel::start, modifier = Modifier.fillMaxWidth())
                }
                is ShareLocationStep.Located -> {
                    LocationPreview(step.location)
                    Text(
                        text = listOfNotNull(coordsText(step.location), accuracyText(step.location.accuracyM)).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                    step.location.address?.let {
                        Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                    }
                    UsTextField(
                        value = state.label,
                        onValueChange = { viewModel.onLabel(it.take(LOCATION_PLACE_MAX)) },
                        label = "Name this place (optional)",
                        placeholder = "e.g. Home",
                        modifier = Modifier.fillMaxWidth(),
                    )
                    UsButton(
                        text = "Send this location",
                        onClick = { viewModel.confirmed()?.let(onSend) },
                        modifier = Modifier.fillMaxWidth().testTag("share-location-send"),
                    )
                }
            }
        }
    }
}

@Composable
private fun Note(text: String, danger: Boolean = false) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = if (danger) UsTheme.extended.statusDanger else UsTheme.extended.textSecondary,
    )
}

/** The map when this build has a Maps key; otherwise the pin card. */
@Composable
private fun LocationPreview(location: ChatLocation) {
    val context = LocalContext.current
    val mapsConfigured = remember(context) { mapsKeyConfigured(context) }
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    if (mapsConfigured) {
        LocationMap(
            pins = listOf(MapPin(location.lat, location.lng, location.label ?: "Your location")),
            radiusMeters = location.accuracyM,
            modifier = Modifier
                .fillMaxWidth()
                .height(PREVIEW_HEIGHT)
                .clip(shape),
        )
    } else {
        PinCard(location, modifier = Modifier.fillMaxWidth().height(PREVIEW_HEIGHT).clip(shape))
    }
}

@Suppress("DEPRECATION")
private fun mapsKeyConfigured(context: Context): Boolean = runCatching {
    val info = context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
    !info.metaData?.getString("com.google.android.geo.API_KEY").isNullOrBlank()
}.getOrDefault(false)

/** A map-less place: a pin on a quiet ground, with the coordinates. */
@Composable
internal fun PinCard(location: ChatLocation, modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.background(UsTheme.extended.bgRaised),
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Icon(
                imageVector = UsIcons.MapPin,
                contentDescription = null,
                tint = UsTheme.extended.statusDanger,
                modifier = Modifier.size(PIN_ICON),
            )
            Text(coordsText(location), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
        }
    }
}

/**
 * The chat bubble: the pin card, the place's name / address, and "Open in
 * Maps" — any installed maps app through `geo:`, else the web link.
 */
@Composable
internal fun LocationBubble(location: ChatLocation, ink: androidx.compose.ui.graphics.Color) {
    val context = LocalContext.current
    Column(
        modifier = Modifier
            .width(BUBBLE_WIDTH)
            .clickable { openInMaps(context, location) }
            .testTag("bubble-location"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        PinCard(
            location,
            modifier = Modifier
                .fillMaxWidth()
                .height(BUBBLE_MAP_HEIGHT)
                .clip(RoundedCornerShape(UsTheme.radii.medium)),
        )
        val title = location.label ?: if (location.address == null) "Location" else null
        title?.let {
            Text(it, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, color = ink, maxLines = 1)
        }
        location.address?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, color = ink, maxLines = 2)
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.chatAccent, modifier = Modifier.size(LINK_ICON))
            Text("Open in Maps", style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.chatAccent)
        }
    }
}

/** `geo:` first (opens the person's own maps app), the https link when nothing handles it. */
internal fun openInMaps(context: Context, location: ChatLocation) {
    val geo = Intent(Intent.ACTION_VIEW, Uri.parse(geoUri(location) { Uri.encode(it) }))
    try {
        context.startActivity(geo)
    } catch (_: ActivityNotFoundException) {
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(mapsWebUrl(location)))) }
    }
}

private val PREVIEW_HEIGHT = 180.dp
private val BUBBLE_WIDTH = 220.dp
private val BUBBLE_MAP_HEIGHT = 120.dp
private val PIN_ICON = 32.dp
private val LINK_ICON = 14.dp
private val SPINNER = 20.dp
private val SPINNER_STROKE = 2.dp
