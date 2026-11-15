package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.VerificationKind

/**
 * Request verification (2026-10-24, the web's /settings/verification): one of
 * the four kinds trust-safety reviews, an optional note, Send. Once sent the
 * page says it is waiting for review.
 */
@Composable
fun VerificationScreen(
    onBack: () -> Unit,
    /** Opens the Official mark page; the row shows only while the mark serves (status read 200). */
    onOpenOfficialMark: () -> Unit = {},
    viewModel: VerificationViewModel = hiltViewModel(),
    officialMark: OfficialMarkEntryViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val officialShown by officialMark.shown.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Request verification", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
                .testTag("verification_screen"),
        ) {
            if (state.submitted) {
                Text(
                    text = "Your request is in. We'll review it and let you know.",
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.statusSuccess,
                    modifier = Modifier.testTag("verification_sent"),
                )
                if (officialShown) OfficialMarkRow(onOpenOfficialMark)
                return@Column
            }
            Text(
                text = "A verified badge shows people this account is who it says it is. Pick what describes you.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Column(modifier = Modifier.selectableGroup(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                VerificationKind.entries.forEach { kind ->
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                        modifier = Modifier
                            .fillMaxWidth()
                            .selectable(
                                selected = state.kind == kind,
                                role = Role.RadioButton,
                                enabled = !state.submitting,
                                onClick = { viewModel.select(kind) },
                            )
                            .testTag("verification_kind:${kind.wire}"),
                    ) {
                        RadioButton(selected = state.kind == kind, onClick = null)
                        Column {
                            Text(kind.label, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
                            Text(kind.detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                    }
                }
            }
            UsTextField(
                value = state.note,
                onValueChange = viewModel::onNoteChange,
                label = "Anything we should know (optional)",
                singleLine = false,
                enabled = !state.submitting,
                errorText = state.error,
                modifier = Modifier.fillMaxWidth().testTag("verification_note"),
            )
            UsButton(
                text = "Send request",
                onClick = viewModel::submit,
                enabled = state.canSubmit,
                loading = state.submitting,
                modifier = Modifier.fillMaxWidth().testTag("verification_send"),
            )
            if (officialShown) OfficialMarkRow(onOpenOfficialMark)
        }
    }
}

/** The way into the Official mark page (2026-11-15). */
@Composable
private fun OfficialMarkRow(onClick: () -> Unit) {
    UsSurfaceCard(onClick = onClick, modifier = Modifier.fillMaxWidth().testTag("verification_official_mark")) {
        Text("Official mark", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
        Text(
            "For organisations, brands, media outlets and public bodies.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}
