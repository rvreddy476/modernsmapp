package com.us.android.feature.memberships.creator

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.LevelRules
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.rupees
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.usSwitchColors

/**
 * The creator's Memberships page (paid memberships, 2026-10-23): can they
 * offer memberships, and if not what is missing; their levels with members
 * and price; add, edit and turn off.
 */
@Composable
fun CreatorMembershipsScreen(
    onBack: () -> Unit,
    viewModel: CreatorMembershipsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "Memberships", onBack = onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).testTag("creator_memberships")) {
            when {
                state.loading && state.levels.isEmpty() -> UsLoadingState(label = "Loading memberships")
                state.unavailable != null -> UsEmptyState(
                    title = "Memberships aren't available yet",
                    detail = state.unavailable
                )
                state.loadError != null && state.levels.isEmpty() ->
                    UsErrorState(message = state.loadError.orEmpty(), onRetry = viewModel::refresh)
                else -> Content(state = state, viewModel = viewModel)
            }
            val message = remember(
                state.message
            ) { state.message?.let { UsMessage(text = it, type = UsMessageType.Info) } }
            UsMessageHost(
                message = message,
                onDismiss = viewModel::dismissMessage,
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
    }
    state.draft?.let { draft ->
        LevelDraftDialog(
            draft = draft,
            onName = { viewModel.editDraft(name = it) },
            onPrice = { viewModel.editDraft(price = it) },
            onPerks = { viewModel.editDraft(perks = it) },
            onSave = viewModel::saveDraft,
            onCancel = viewModel::cancelDraft,
        )
    }
}

@Composable
private fun Content(state: CreatorMembershipsUiState, viewModel: CreatorMembershipsViewModel) {
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(vertical = UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item(key = "intro") {
            Text(
                text = "Offer up to ${LevelRules.MAX_ACTIVE_LEVELS} monthly levels, " +
                    "from ${rupees(LevelRules.MIN_PRICE_PAISE)} " +
                    "to ${rupees(LevelRules.MAX_PRICE_PAISE)}, GST included. Members pay each month themselves.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        }
        item(key = "eligibility") { EligibilityCard(state) }
        if (state.levels.isNotEmpty()) {
            item(key = "members") {
                Text(
                    text = memberCountLabel(state.totalMembers),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.testTag("creator_memberships_members"),
                )
            }
        }
        items(state.levels, key = { it.id }) { level ->
            LevelCard(
                level = level,
                busy = state.busyLevelId == level.id,
                onEdit = { viewModel.startEdit(level) },
                onActive = { viewModel.setActive(level, it) },
            )
        }
        item(key = "add") {
            UsButton(
                text = if (state.levels.isEmpty()) "Create your first level" else "Add a level",
                onClick = viewModel::startNew,
                enabled = state.canAddLevel,
                modifier = Modifier.fillMaxWidth().testTag("creator_memberships_add"),
            )
            if (state.activeCount >= LevelRules.MAX_ACTIVE_LEVELS) {
                Text(
                    text = "You have ${LevelRules.MAX_ACTIVE_LEVELS} active levels. Turn one off to add another.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(top = UsTheme.spacing.s),
                )
            }
        }
    }
}

@Composable
private fun EligibilityCard(state: CreatorMembershipsUiState) {
    val eligibility = state.eligibility ?: return
    // A creator who already offers memberships needs no reminder of the bar.
    if (eligibility.eligible && state.levels.isNotEmpty()) return
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("creator_memberships_eligibility")) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)
        ) {
            Icon(
                imageVector = if (eligibility.eligible) UsIcons.ShieldCheck else UsIcons.Info,
                contentDescription = null,
                tint = if (eligibility.eligible) UsTheme.extended.statusSuccess else UsTheme.extended.statusWarning,
            )
            Text(
                text = if (eligibility.eligible) "You can offer memberships" else "Not yet — here's what's missing",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
            )
        }
        missingLines(eligibility.missing).forEach { line ->
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Text(text = "•", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                Text(text = line, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textBody)
            }
        }
    }
}

@Composable
private fun LevelCard(level: MembershipLevel, busy: Boolean, onEdit: () -> Unit, onActive: (Boolean) -> Unit) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("creator_level:${level.id}")) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(text = level.name, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    text = "${level.priceLabel} · ${memberCountLabel(level.memberCount)}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            Switch(
                checked = level.isActive,
                onCheckedChange = onActive,
                enabled = !busy,
                colors = usSwitchColors(),
                modifier = Modifier.testTag("creator_level_active:${level.id}"),
            )
        }
        if (!level.isActive) {
            Text(
                text = "Off — no new members. Current members keep it until their month ends.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        level.perks.forEach { perk ->
            Text(text = "• $perk", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textBody)
        }
        UsSecondaryButton(
            text = "Edit",
            onClick = onEdit,
            compact = true,
            modifier = Modifier.testTag("creator_level_edit:${level.id}")
        )
    }
}

@Composable
private fun LevelDraftDialog(
    draft: LevelDraft,
    onName: (String) -> Unit,
    onPrice: (String) -> Unit,
    onPerks: (String) -> Unit,
    onSave: () -> Unit,
    onCancel: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = { if (!draft.saving) onCancel() },
        containerColor = UsTheme.extended.bgSheet,
        title = { Text(if (draft.isNew) "New level" else "Edit level") },
        text = {
            Column(
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier.testTag("level_draft")
            ) {
                UsTextField(
                    value = draft.name,
                    onValueChange = onName,
                    label = "Name",
                    placeholder = "Supporter",
                    enabled = !draft.saving,
                )
                UsTextField(
                    value = draft.price,
                    onValueChange = onPrice,
                    label = "Price a month (₹)",
                    placeholder = "99",
                    enabled = !draft.saving,
                    keyboardType = KeyboardType.Number,
                )
                OutlinedTextField(
                    value = draft.perks,
                    onValueChange = onPerks,
                    label = { Text("Perks, one per line") },
                    enabled = !draft.saving,
                    minLines = 3,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Text),
                    modifier = Modifier.fillMaxWidth().testTag("level_draft_perks"),
                )
                (draft.problems + listOfNotNull(draft.error)).forEach { problem ->
                    Text(
                        text = problem,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.statusDanger
                    )
                }
                if (!draft.isNew) {
                    Text(
                        text = "A new price applies to new members. Current members keep theirs.",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onSave, enabled = !draft.saving, modifier = Modifier.testTag("level_draft_save")) {
                Text(if (draft.saving) "Saving…" else "Save", color = UsTheme.extended.accentSolid)
            }
        },
        dismissButton = {
            TextButton(
                onClick = onCancel,
                enabled = !draft.saving
            ) { Text("Cancel", color = UsTheme.extended.textPrimary) }
        },
    )
}

/** "1 member", "12 members". */
internal fun memberCountLabel(count: Int): String = if (count == 1) "1 member" else "$count members"
