package com.us.android.feature.dating.safety

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ui.InfoNote

/**
 * The report sheet: the nine reasons, details (required for "Something else",
 * at most 500 characters), and the evidence the opening screen supplied.
 * Sending it also blocks the person.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReportSheet(
    initial: ReportDraft,
    name: String?,
    onSubmit: (ReportDraft) -> Unit,
    onDismiss: () -> Unit,
) {
    var draft by remember(initial) { mutableStateOf(initial) }
    val hasEvidence = initial.photoIds.isNotEmpty() || initial.sparkIds.isNotEmpty() || initial.messageIds.isNotEmpty()
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgRaised,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Text(
                text = if (name.isNullOrBlank()) "Report this person" else "Report $name",
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
            )
            InfoNote("They won't know you reported them. We'll also block them for you.")
            ReportReason.entries.forEach { reason ->
                Row(
                    modifier = Modifier.fillMaxWidth().clickable { draft = draft.copy(reason = reason) },
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RadioButton(selected = draft.reason == reason, onClick = { draft = draft.copy(reason = reason) })
                    Text(reason.label, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textSecondary)
                }
            }
            val length = ReportRules.detailsLength(draft.details)
            UsTextField(
                value = draft.details,
                onValueChange = { draft = draft.copy(details = it) },
                label = if (draft.reason == ReportReason.OTHER) "What happened?" else "Anything else? (optional)",
                singleLine = false,
                errorText = if (length > ReportRules.MAX_DETAILS) "$length / ${ReportRules.MAX_DETAILS}" else null,
            )
            if (hasEvidence) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = draft.includeEvidence, onCheckedChange = { draft = draft.copy(includeEvidence = it) })
                    Text(
                        if (initial.sparkIds.isNotEmpty()) "Include their spark" else "Include their photo",
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textSecondary,
                    )
                }
            }
            val problem = ReportRules.problem(draft)
            UsButton(
                text = "Report and block",
                enabled = problem == null,
                onClick = { onSubmit(draft) },
                modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
            )
        }
    }
}
