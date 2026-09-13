package com.us.android.feature.rider.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.onboarding.Vehicle
import com.us.android.feature.rider.ui.FieldError
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.SectionHeader
import com.us.android.feature.rider.ui.contentPadding

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ProfileScreen(onBack: () -> Unit, onSaved: () -> Unit, viewModel: ProfileViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(viewModel) { viewModel.savedEvents.collect { onSaved() } }

    RiderScreen(
        title = if (state.existing == null) "Become a rider" else "Profile and vehicle",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        if (state.loading) {
            LoadingPane()
            return@RiderScreen
        }
        val form = state.form
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(contentPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsTextField(
                value = form.fullName,
                onValueChange = { v -> viewModel.edit(ProfileField.NAME) { it.copy(fullName = v) } },
                label = "Full name",
                errorText = state.errors[ProfileField.NAME],
                enabled = !state.saving,
            )
            UsTextField(
                value = form.phone,
                onValueChange = { v -> viewModel.edit(ProfileField.PHONE) { it.copy(phone = v.take(PHONE_INPUT_MAX)) } },
                label = "Mobile number",
                placeholder = "98765 43210",
                keyboardType = KeyboardType.Phone,
                errorText = state.errors[ProfileField.PHONE],
                enabled = !state.saving,
            )
            UsTextField(
                value = form.city,
                onValueChange = { v -> viewModel.edit(null) { it.copy(city = v) } },
                label = "City",
                enabled = !state.saving,
            )
            SectionHeader("What you ride")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Vehicle.CHOICES.forEach { choice ->
                    UsPillButton(
                        text = choice.label,
                        filled = form.vehicleType == choice.wire,
                        onClick = { viewModel.edit(ProfileField.VEHICLE_TYPE) { it.copy(vehicleType = choice.wire) } },
                        enabled = !state.saving,
                    )
                }
            }
            FieldError(state.errors[ProfileField.VEHICLE_TYPE])
            if (form.vehicleType.isNotBlank() && state.needsRegistration) {
                UsTextField(
                    value = form.vehicleNumber,
                    onValueChange = { v -> viewModel.edit(ProfileField.VEHICLE_NUMBER) { it.copy(vehicleNumber = v.uppercase().take(REG_INPUT_MAX)) } },
                    label = "Registration number",
                    placeholder = "KA01AB1234",
                    errorText = state.errors[ProfileField.VEHICLE_NUMBER],
                    enabled = !state.saving,
                )
                InfoNote("A motorised vehicle needs your driving licence and its registration certificate (RC).")
            } else if (form.vehicleType.isNotBlank()) {
                InfoNote("Bicycles don't need a driving licence or an RC.")
            }
            UsButton(
                text = if (state.existing == null) "Continue" else "Save",
                onClick = viewModel::save,
                loading = state.saving,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

private const val PHONE_INPUT_MAX = 16
private const val REG_INPUT_MAX = 14
