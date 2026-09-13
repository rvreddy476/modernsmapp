package com.us.android.feature.kitchen.fssai

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.network.FssaiDto
import com.us.android.feature.kitchen.kycui.DocumentUploadField
import com.us.android.feature.kitchen.ui.CardHeading
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenPill
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.LabeledValue
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.datePart

@Composable
fun FssaiScreen(onBack: () -> Unit, viewModel: FssaiViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri != null) viewModel.onPhotoPicked(uri.toString())
    }
    KitchenScreen(
        title = "FSSAI licence",
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
            state.saved?.let { SavedFssaiCard(it) }
            UsTextField(
                value = state.licence,
                onValueChange = viewModel::onLicence,
                label = "FSSAI licence number",
                placeholder = "14 digits",
                errorText = state.errors["licence_number"],
                keyboardType = KeyboardType.Number,
            )
            UsDatePickerField(
                value = state.expiresAt,
                onValueChange = viewModel::onExpiresAt,
                label = "Expires on",
                errorText = state.errors["expires_at"],
                minDate = viewModel.today.plusDays(1),
            )
            DocumentUploadField(
                label = "Photo of the licence",
                state = state.upload,
                onPick = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                errorText = state.errors["media_id"],
            )
            InfoNote("A clear photo of the whole licence. Feast checks it before your kitchen can take orders.")
            UsButton(
                text = "Save licence",
                onClick = viewModel::save,
                loading = state.saving,
                enabled = state.upload !is com.us.android.feature.kitchen.kycui.DocumentUploadUi.Uploading,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun SavedFssaiCard(saved: FssaiDto) {
    KitchenCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(title = "Licence on file", modifier = Modifier.weight(1f))
            val (label, tone) = when (saved.document?.status) {
                "APPROVED" -> "Approved" to PillTone.Positive
                "REJECTED" -> "Rejected" to PillTone.Danger
                else -> "In review" to PillTone.Warning
            }
            KitchenPill(label, tone)
        }
        if (saved.fssaiLicenceNumber.isNotBlank()) LabeledValue("Licence", saved.fssaiLicenceNumber)
        saved.fssaiExpiresAt?.let { LabeledValue("Expires", datePart(it)) }
        saved.document?.rejectionReason?.let { InfoNote(it, tone = PillTone.Danger) }
    }
}
