package com.us.android.core.memberships.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.MembershipLevel

/**
 * "Members only" in a composer (paid memberships, 2026-10-23): Everyone, or
 * one of the creator's ACTIVE levels. Offered only when there is at least one
 * ([levels] non-empty) — a host that has none draws nothing. The pick is
 * applied after the post is created or edited, with
 * `PUT /v1/posts/:id/membership`.
 */
fun membersOnlySummary(levels: List<MembershipLevel>, selectedId: String?): String = when {
    selectedId == null -> "Everyone"
    else -> levels.firstOrNull { it.id == selectedId }?.let { "${it.name} members" } ?: "Members only"
}

/**
 * A row for hosts without a row style of their own: the label, what is
 * picked, and the picker sheet behind it.
 */
@Composable
fun MembersOnlyPickerRow(
    levels: List<MembershipLevel>,
    selectedId: String?,
    onSelect: (String?) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    // A post still gated on a level since turned off can be opened to everyone.
    if (levels.isEmpty() && selectedId == null) return
    var open by rememberSaveable { mutableStateOf(false) }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(enabled = enabled, role = Role.Button) { open = true }
            .padding(vertical = UsTheme.spacing.l)
            .testTag("members_only_row"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(imageVector = UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.textPrimary)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = "Who can see this",
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary
            )
            Text(
                text = membersOnlySummary(levels, selectedId),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
    }
    if (open) {
        MembersOnlyLevelSheet(
            levels = levels,
            selectedId = selectedId,
            onSelect = {
                onSelect(it)
                open = false
            },
            onDismiss = { open = false },
        )
    }
}

/** The choice itself: Everyone, or members of one level (and every level above it). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MembersOnlyLevelSheet(
    levels: List<MembershipLevel>,
    selectedId: String?,
    onSelect: (String?) -> Unit,
    onDismiss: () -> Unit,
    title: String = "Who can see this",
    detail: String = "Members of a level also see posts for the levels priced below it. " +
        "Everyone else sees the title, the cover and a lock.",
    /** Offer "Everyone" (no gate) as the first choice; off where a level must be picked. */
    includeEveryone: Boolean = true,
    /** The line under a level: its price by default. */
    levelLabel: (MembershipLevel) -> String = { "${it.name} members" },
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        scrimColor = UsTheme.extended.scrim,
        modifier = Modifier.testTag("members_only_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(text = title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(text = detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            Spacer(Modifier.height(UsTheme.spacing.m))
            if (includeEveryone) {
                Choice(
                    label = "Everyone",
                    detail = null,
                    selected = selectedId == null,
                    tag = "members_only_choice:everyone"
                ) {
                    onSelect(null)
                }
            }
            levels.sortedBy { it.pricePaise }.forEach { level ->
                Choice(
                    label = levelLabel(level),
                    detail = level.priceLabel,
                    selected = level.id == selectedId,
                    tag = "members_only_choice:${level.id}",
                ) { onSelect(level.id) }
            }
            Spacer(Modifier.height(UsTheme.spacing.xxl))
        }
    }
}

@Composable
private fun Choice(label: String, detail: String?, selected: Boolean, tag: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(role = Role.RadioButton, onClick = onClick)
            .background(if (selected) UsTheme.extended.fillSubtle else UsTheme.extended.bgSheet)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(
            selected = selected,
            onClick = null,
            colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.accentSolid),
            modifier = Modifier.size(RADIO),
        )
        Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)) {
            Text(text = label, style = MaterialTheme.typography.bodyLarge)
            if (detail != null) {
                Text(
                    text = detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted
                )
            }
        }
    }
}

private val RADIO = 40.dp
