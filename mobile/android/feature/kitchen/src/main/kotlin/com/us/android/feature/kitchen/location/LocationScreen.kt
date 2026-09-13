package com.us.android.feature.kitchen.location

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.kyc.GstStateCodes
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.FieldError
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.SectionHeader
import com.us.android.feature.kitchen.ui.kitchenSliderColors
import java.util.Locale

@Composable
fun LocationScreen(onBack: () -> Unit, viewModel: LocationViewModel = hiltViewModel()) {
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
        viewModel.effects.collect { effect ->
            if (effect == LocationEffect.RequestPermission) {
                permissionLauncher.launch(
                    arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION),
                )
            }
        }
    }
    var pickingState by rememberSaveable { mutableStateOf(false) }

    // The rationale, ALWAYS before the system prompt (LocationPermissionFlow).
    if (state.step == LocationStepState.ExplainingPermission) {
        AlertDialog(
            onDismissRequest = viewModel::onRationaleDismissed,
            icon = { Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid) },
            title = { Text("Pin your kitchen") },
            text = {
                Text(
                    "Feast Kitchen uses your location once, right now, to place your kitchen for riders " +
                        "and customers. It isn't tracked in the background. You can type the address instead.",
                )
            },
            confirmButton = {
                TextButton(onClick = viewModel::onRationaleAccepted) {
                    Text("Continue", color = UsTheme.extended.accentSolid)
                }
            },
            dismissButton = {
                TextButton(onClick = viewModel::onRationaleDismissed) {
                    Text("Not now", color = UsTheme.extended.textMuted)
                }
            },
            containerColor = UsTheme.extended.bgRaised,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
        )
    }
    if (pickingState) {
        StatePickerDialog(
            onPick = {
                viewModel.onStateChange(it)
                pickingState = false
            },
            onDismiss = { pickingState = false },
        )
    }

    KitchenScreen(
        title = "Location",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 96.dp),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            LocalMapPreview.current.Content(
                coordinates = state.coordinates,
                radiusKm = state.radiusKm,
                modifier = Modifier,
            )
            UsButton(
                text = if (state.coordinates == null) "Use my current location" else "Update from my current location",
                onClick = viewModel::onUseCurrentLocation,
                loading = state.step == LocationStepState.Locating,
                modifier = Modifier.fillMaxWidth(),
            )
            LocationStatus(
                state = state,
                onOpenSettings = {
                    context.startActivity(
                        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null))
                            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                    )
                },
            )

            SectionHeader("Address")
            UsTextField(
                value = state.line1,
                onValueChange = viewModel::onLine1Change,
                label = "Building and street",
                errorText = state.errors["address_line1"],
            )
            UsTextField(
                value = state.line2,
                onValueChange = viewModel::onLine2Change,
                label = "Area or landmark (optional)",
                errorText = state.errors["address_line2"],
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsTextField(
                    value = state.city,
                    onValueChange = viewModel::onCityChange,
                    label = "City",
                    errorText = state.errors["city"],
                    modifier = Modifier.weight(1f),
                )
                UsTextField(
                    value = state.postalCode,
                    onValueChange = viewModel::onPostalCodeChange,
                    label = "PIN code",
                    errorText = state.errors["postal_code"],
                    keyboardType = KeyboardType.Number,
                    modifier = Modifier.weight(0.7f),
                )
            }
            StateField(value = state.state, errorText = state.errors["state"], onClick = { pickingState = true })
            UsSecondaryButton(
                text = if (state.locatingAddress) "Finding…" else "Find this address",
                onClick = viewModel::locateFromAddress,
                enabled = !state.locatingAddress,
                modifier = Modifier.fillMaxWidth(),
            )

            SectionHeader(
                title = "Delivery radius",
                trailing = {
                    Text(
                        text = String.format(Locale.US, "%.1f km", state.radiusKm),
                        style = MaterialTheme.typography.titleSmall,
                        color = UsTheme.extended.textPrimary,
                    )
                },
            )
            Slider(
                value = state.radiusKm,
                onValueChange = viewModel::onRadiusChange,
                valueRange = MIN_RADIUS_KM..MAX_RADIUS_KM,
                steps = RADIUS_STEPS,
                colors = kitchenSliderColors(),
            )
            FieldError(state.errors["delivery_radius_km"])
            InfoNote("Customers this far from your kitchen can order from you — between 1 and 15 km.")

            UsButton(
                text = "Save location",
                onClick = viewModel::save,
                loading = state.saving,
                modifier = Modifier.fillMaxWidth(),
            )
            state.saved?.let { saved ->
                KitchenCard {
                    CardHeading(
                        title = "Saved",
                        body = listOf(saved.addressLine1, saved.city, saved.state).filter { it.isNotBlank() }.joinToString(", "),
                        titleColor = UsTheme.extended.statusSuccess,
                    )
                }
            }
        }
    }
}

@Composable
private fun LocationStatus(state: LocationUiState, onOpenSettings: () -> Unit) {
    val step = state.step
    val pinError = state.errors["latitude"]
    when {
        pinError != null -> FieldError(pinError)
        step is LocationStepState.Denied -> Column {
            InfoNote(
                text = if (step.canAskAgain) {
                    "Location permission was declined. Type the address and tap Find this address instead."
                } else {
                    "Location is off for Feast Kitchen. Turn it on in Settings, or type the address below."
                },
                tone = PillTone.Warning,
            )
            if (!step.canAskAgain) {
                TextButton(onClick = onOpenSettings) {
                    Text("Open settings", color = UsTheme.extended.accentSolid)
                }
            }
        }
        step == LocationStepState.Unavailable -> InfoNote(
            text = "Couldn't get a location fix. Try near a window, or type the address below.",
            tone = PillTone.Warning,
        )
        state.coordinates != null -> InfoNote("Kitchen pinned. Check the address below matches.", tone = PillTone.Positive)
        else -> Unit
    }
}

@Composable
private fun StateField(value: String, errorText: String?, onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val borderColor = if (errorText != null) UsTheme.extended.statusDanger else UsTheme.extended.borderMedium
    Column {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(shape)
                .border(1.dp, borderColor, shape)
                .clickable(onClick = onClick)
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text("State", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                Text(
                    text = value.ifBlank { "Choose your state" },
                    style = MaterialTheme.typography.bodyLarge,
                    color = if (value.isBlank()) UsTheme.extended.textDim else UsTheme.extended.textPrimary,
                )
            }
            Icon(UsIcons.ChevronDown, contentDescription = null, tint = UsTheme.extended.textMuted)
        }
        FieldError(errorText)
    }
}

@Composable
private fun StatePickerDialog(onPick: (String) -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("State") },
        text = {
            LazyColumn(modifier = Modifier.heightIn(max = 420.dp)) {
                items(GstStateCodes.locationStateNames) { name ->
                    Text(
                        text = name,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable { onPick(name) }
                            .padding(vertical = 12.dp),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text("Close", color = UsTheme.extended.textMuted) }
        },
        containerColor = UsTheme.extended.bgRaised,
        titleContentColor = UsTheme.extended.textPrimary,
    )
}

/** 1–15 km in half-kilometre stops: 29 values, 27 between the ends. */
private const val RADIUS_STEPS = 27
