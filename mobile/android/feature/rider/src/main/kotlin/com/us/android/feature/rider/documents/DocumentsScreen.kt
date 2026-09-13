package com.us.android.feature.rider.documents

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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.kycui.DocumentUploadField
import com.us.android.feature.rider.ui.CardHeading
import com.us.android.feature.rider.ui.DocumentStatusText
import com.us.android.feature.rider.ui.InfoNote
import com.us.android.feature.rider.ui.LabeledValue
import com.us.android.feature.rider.ui.LoadingPane
import com.us.android.feature.rider.ui.MessagePane
import com.us.android.feature.rider.ui.PillTone
import com.us.android.feature.rider.ui.RiderCard
import com.us.android.feature.rider.ui.RiderPill
import com.us.android.feature.rider.ui.RiderScreen
import com.us.android.feature.rider.ui.contentPadding

@Composable
fun DocumentsScreen(onBack: () -> Unit, viewModel: DocumentsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var picking by remember { mutableStateOf<DrivingDocument?>(null) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        val document = picking
        if (uri != null && document != null) viewModel.onPhotoPicked(document, uri.toString())
        picking = null
    }

    RiderScreen(title = "Licence and RC", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        when {
            state.loading -> LoadingPane()
            !state.required -> MessagePane(
                title = "Not needed for a bicycle",
                body = "Bicycle riders don't submit a driving licence or an RC.",
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            else -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(contentPadding(padding)),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                InfoNote("If DigiLocker already verified your licence and RC, they show as approved and there's nothing to add here.")
                DrivingDocument.entries.forEach { document ->
                    val draft = state.drafts.getValue(document)
                    RiderCard {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            CardHeading(document.title, modifier = Modifier.weight(1f))
                            draft.submitted?.let { RiderPill(DocumentStatusText.label(it.status), DocumentStatusText.tone(it.status)) }
                        }
                        draft.submitted?.let { submitted ->
                            submitted.numberMasked?.let { LabeledValue("Number", it) }
                            submitted.rejectionReason?.let { InfoNote(it, tone = PillTone.Danger) }
                        }
                        UsTextField(
                            value = draft.number,
                            onValueChange = { viewModel.onNumber(document, it) },
                            label = if (document == DrivingDocument.DRIVING_LICENCE) "Licence number" else "Registration number",
                            placeholder = if (document == DrivingDocument.DRIVING_LICENCE) "KA0120200000001" else "KA01AB1234",
                            errorText = draft.numberError,
                            enabled = !draft.submitting,
                        )
                        DocumentUploadField(
                            label = "Photo of the ${document.title.lowercase()}",
                            state = draft.upload,
                            onPick = {
                                picking = document
                                picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                            },
                        )
                        UsButton(
                            text = if (draft.submitted == null) "Submit" else "Submit again",
                            onClick = { viewModel.submit(document) },
                            loading = draft.submitting,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
    }
}
