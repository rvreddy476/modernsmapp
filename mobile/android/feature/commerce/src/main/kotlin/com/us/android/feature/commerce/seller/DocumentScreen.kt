package com.us.android.feature.commerce.seller

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.commerce.ui.CommerceNotice

/**
 * Sending an identity document for review.
 *
 * The last thing a seller could not do in the app. Everything else in
 * onboarding could be completed here; this one requirement sent them to
 * another channel, so the shop could not be submitted and no seller could be
 * approved without an operator stepping in.
 *
 * ## What the four stages are for
 *
 * A media upload is reserve → push → confirm → attach, and each can fail
 * differently. They are shown as distinct stages rather than one spinner
 * because the last one is not a formality: the server verifies the media id
 * belongs to THIS caller, is ready, and has passed moderation. An upload that
 * completed can still be refused there, and a seller watching a spinner stop
 * with no explanation has no idea whether to try again.
 */
@Composable
fun DocumentScreen(
    onBack: () -> Unit,
    onAttached: () -> Unit,
    viewModel: DocumentViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // A document is not always an image — a PAN card is often a PDF — so this
    // is the general file picker rather than the photo picker. The MIME filter
    // is advisory; the ViewModel re-checks what actually came back.
    val pickDocument = rememberLauncherForActivityResult(
        ActivityResultContracts.GetContent(),
    ) { uri -> uri?.let { viewModel.upload(it.toString(), onAttached) } }

    UsScaffold(topBar = { UsTopBar(title = "Identity document", onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(vertical = UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "A reviewer checks this against the details you gave. " +
                    "A photo or a PDF, up to 10 MB.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )

            UsChoiceRow(
                options = SellerDocumentType.entries.map { UsChoice(it, it.label) },
                selected = state.type,
                onSelect = { it?.let(viewModel::setType) },
                label = "Which document?",
                enabled = !state.busy,
                allowDeselect = false,
            )

            UsTextField(
                value = state.documentNumber,
                onValueChange = viewModel::setNumber,
                label = "Document number (optional)",
                // Optional on purpose. A reviewer reads the number off the
                // document itself, and demanding it typed as well adds a
                // transcription error to a check that has the original.
                placeholder = "We read this off the document",
                enabled = !state.busy,
            )

            UploadProgress(state)

            state.error?.let { error ->
                Text(
                    text = error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            if (state.stage == DocumentUploadState.Stage.Done) {
                CommerceNotice(text = "Sent. A reviewer will look at it shortly.")
            }

            UsButton(
                text = if (state.stage == DocumentUploadState.Stage.Done) {
                    "Send another"
                } else {
                    "Choose a file"
                },
                onClick = { pickDocument.launch(PICKER_FILTER) },
                enabled = state.canPick,
                loading = state.busy,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/**
 * Which stage the upload is at, and how far through the bytes.
 *
 * Named stages rather than one indeterminate spinner: "confirming" and
 * "attaching" both look like nothing is happening, and attaching is where a
 * seller's upload can still be refused.
 */
@Composable
private fun UploadProgress(state: DocumentUploadState) {
    val label = when (state.stage) {
        DocumentUploadState.Stage.Idle, DocumentUploadState.Stage.Done -> null
        DocumentUploadState.Stage.Starting -> "Starting…"
        DocumentUploadState.Stage.Uploading -> "Sending…"
        DocumentUploadState.Stage.Confirming -> "Checking the file…"
        DocumentUploadState.Stage.Attaching -> "Attaching it to your shop…"
    } ?: return

    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
        val progress = state.progress
        if (progress != null && progress.second > 0) {
            LinearProgressIndicator(
                progress = { progress.first.toFloat() / progress.second.toFloat() },
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
        }
    }
}

/**
 * What the picker offers.
 *
 * Wider than what is accepted: Android's GetContent filter is a hint, some
 * providers ignore it, and the ViewModel re-checks the MIME type of whatever
 * actually comes back. Filtering here is a convenience, never the guard.
 */
private const val PICKER_FILTER = "*/*"
