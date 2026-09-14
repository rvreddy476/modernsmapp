package com.us.android.feature.feast.address

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.feast.ui.BottomAction
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.Pill
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone
import com.us.android.feature.feast.ui.listPadding

@Composable
fun AddressesScreen(
    onBack: () -> Unit,
    onAdd: () -> Unit,
    onPicked: () -> Unit,
    viewModel: AddressesViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.load()
        onPauseOrDispose { }
    }

    FeastScreen(
        title = "Delivery address",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Add a new address", onClick = onAdd) },
    ) { padding ->
        when {
            state.loading -> LoadingPane()
            state.error != null && state.addresses.isEmpty() -> MessagePane(
                title = "Couldn't load your addresses",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            state.addresses.isEmpty() -> MessagePane(
                title = "No saved addresses",
                body = "Add where you'd like your food delivered.",
                icon = UsIcons.MapPin,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                items(state.addresses, key = { it.id }) { address ->
                    val selected = address.id == state.selectedId
                    FeastCard(onClick = {
                        viewModel.select(address)
                        onPicked()
                    }) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            RadioButton(
                                selected = selected,
                                onClick = {
                                    viewModel.select(address)
                                    onPicked()
                                },
                                colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.accentSolid),
                            )
                            Column(Modifier.weight(1f)) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(address.label ?: "Address", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                                    if (address.latitude == null || address.longitude == null) {
                                        Spacer(Modifier.width(UsTheme.spacing.m))
                                        Pill("No pin", Tone.Warning)
                                    }
                                }
                                Text(
                                    listOfNotNull(address.addressLine1, address.addressLine2, address.landmark, address.city, address.postalCode)
                                        .filter { it.isNotBlank() }
                                        .joinToString(", "),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = UsTheme.extended.textMuted,
                                )
                            }
                            IconButton(onClick = { viewModel.delete(address) }, enabled = state.deletingId == null) {
                                Icon(UsIcons.Trash, contentDescription = "Remove address", tint = UsTheme.extended.textDim)
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun AddAddressScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
    viewModel: AddAddressViewModel = hiltViewModel(),
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
        viewModel.effects.collect { effect ->
            if (effect == LocationEffect.RequestPermission) {
                permissionLauncher.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
            }
        }
    }
    LaunchedEffect(state.saved) { if (state.saved != null) onSaved() }

    // The rationale, ALWAYS before the system prompt (LocationPermissionFlow).
    if (state.step == LocationStep.ExplainingPermission) {
        AlertDialog(
            onDismissRequest = viewModel::onRationaleDismissed,
            icon = { Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid) },
            title = { Text("Find your address") },
            text = {
                Text(
                    "Feast uses your location once, right now, to pin where your food should go and check that " +
                        "restaurants can reach you. It isn't tracked in the background. You can type the address instead.",
                )
            },
            confirmButton = {
                TextButton(onClick = viewModel::onRationaleAccepted) { Text("Continue", color = UsTheme.extended.accentSolid) }
            },
            dismissButton = {
                TextButton(onClick = viewModel::onRationaleDismissed) { Text("Not now", color = UsTheme.extended.textMuted) }
            },
            containerColor = UsTheme.extended.bgRaised,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
        )
    }

    FeastScreen(
        title = "New address",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save address", onClick = viewModel::save, loading = state.saving, enabled = !state.saving) },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 24.dp),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            FeastCard {
                val pin = state.coordinates
                Text(
                    text = when {
                        state.step == LocationStep.Locating -> "Finding you…"
                        pin != null -> "Address pinned"
                        else -> "Pin your address"
                    },
                    style = MaterialTheme.typography.titleMedium,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = pin?.let { String.format(java.util.Locale.ENGLISH, "%.5f, %.5f", it.latitude, it.longitude) }
                        ?: "Restaurants measure delivery range from this pin.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    UsPillButton(text = "Use my current location", onClick = viewModel::onUseCurrentLocation, busy = state.step == LocationStep.Locating)
                    UsPillButton(text = "Find typed address", onClick = viewModel::locateFromAddress, filled = false, busy = state.locatingAddress)
                }
                when (val step = state.step) {
                    is LocationStep.Denied -> {
                        InfoNote("Location is off for Feast. Type the address and use Find typed address.", tone = Tone.Warning)
                        if (!step.canAskAgain) {
                            UsSecondaryButton(
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
                    LocationStep.Unavailable -> InfoNote("We couldn't get a location fix. Type the address instead.", tone = Tone.Warning)
                    else -> Unit
                }
                state.errors["latitude"]?.let { InfoNote(it, tone = Tone.Danger) }
            }

            SectionLabel("Save as")
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                AddressLabel.entries.forEach { label ->
                    UsPillButton(text = label.wire, onClick = { viewModel.onLabel(label) }, filled = state.label == label)
                }
            }

            SectionLabel("Address")
            UsTextField(state.line1, viewModel::onLine1, "House / flat, street", Modifier.fillMaxWidth(), errorText = state.errors["address_line1"])
            UsTextField(state.line2, viewModel::onLine2, "Area / locality", Modifier.fillMaxWidth())
            UsTextField(state.landmark, viewModel::onLandmark, "Landmark (optional)", Modifier.fillMaxWidth())
            UsTextField(state.city, viewModel::onCity, "City", Modifier.fillMaxWidth(), errorText = state.errors["city"])
            UsTextField(state.state, viewModel::onState, "State", Modifier.fillMaxWidth())
            UsTextField(
                state.postalCode,
                viewModel::onPostalCode,
                "PIN code",
                Modifier.fillMaxWidth(),
                errorText = state.errors["postal_code"],
                keyboardType = KeyboardType.Number,
            )
        }
    }
}
