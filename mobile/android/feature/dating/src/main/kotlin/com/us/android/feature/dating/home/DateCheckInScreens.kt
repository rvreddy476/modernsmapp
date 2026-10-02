package com.us.android.feature.dating.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.ProfileOption
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.SingleOptionChips
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.toneColor

/** One "How did it go?" card on the matches list. */
@Composable
internal fun CheckInCard(target: CheckInTarget, onOpen: () -> Unit) {
    DatingCard(onClick = onOpen) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            Icon(UsIcons.HeartHandshake, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(24.dp))
            Column(Modifier.weight(1f)) {
                Text(CheckInCopy.CARD_TITLE, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                Text(CheckInCopy.cardBody(target.name), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
            TextButton(onClick = onOpen) { Text(CheckInCopy.CARD_ACTION, color = UsTheme.extended.accentSolid) }
        }
    }
}

/** The check-in sheet and, from its supportive step, the report sheet. Draws nothing while neither is open. */
@Composable
internal fun CheckInSheets(state: DateCheckInUi, viewModel: DateCheckInViewModel) {
    state.sheet?.let { DateCheckInSheet(it, viewModel) }
    state.reporting?.let { target ->
        ReportSheet(
            initial = ReportDraft(targetId = target.userId),
            name = target.name,
            onSubmit = viewModel::report,
            onDismiss = viewModel::dismissReport,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DateCheckInSheet(sheet: CheckInSheetUi, viewModel: DateCheckInViewModel) {
    ModalBottomSheet(
        onDismissRequest = viewModel::dismissSheet,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgRaised,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            if (sheet.offerReport) UnsafeStep(sheet, viewModel) else Questions(sheet, viewModel)
        }
    }
}

@Composable
private fun Questions(sheet: CheckInSheetUi, viewModel: DateCheckInViewModel) {
    val enabled = !sheet.sending
    Text(CheckInCopy.sheetTitle(sheet.target.name), style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
    Question(CheckInCopy.MET)
    SingleOptionChips(
        options = DateMet.entries.map { ProfileOption(it.wire, it.label) },
        selected = sheet.met?.wire,
        onSelect = { code -> DateMet.entries.firstOrNull { it.wire == code }?.let(viewModel::chooseMet) },
        enabled = enabled,
    )
    if (sheet.asksMore) {
        InfoNote(CheckInCopy.OPTIONAL)
        Question(CheckInCopy.AGAIN)
        SingleOptionChips(
            options = DateAgain.entries.map { ProfileOption(it.wire, it.label) },
            selected = sheet.again?.wire,
            onSelect = { code -> DateAgain.entries.firstOrNull { it.wire == code }?.let(viewModel::chooseAgain) },
            enabled = enabled,
        )
        Question(CheckInCopy.FELT_SAFE)
        SingleOptionChips(
            options = listOf(ProfileOption(YES, CheckInCopy.YES), ProfileOption(NO, CheckInCopy.NO)),
            selected = when (sheet.feltSafe) {
                true -> YES
                false -> NO
                null -> null
            },
            onSelect = { code -> code?.let { viewModel.chooseFeltSafe(it == YES) } },
            enabled = enabled,
        )
    }
    sheet.error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = toneColor(Tone.Danger)) }
    UsButton(
        text = CheckInCopy.SEND,
        enabled = sheet.canSend,
        loading = sheet.sending,
        onClick = viewModel::send,
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.s),
    )
}

/** They did not feel safe: support first, then the way to report, never pushed. */
@Composable
private fun UnsafeStep(sheet: CheckInSheetUi, viewModel: DateCheckInViewModel) {
    Text(CheckInCopy.UNSAFE_TITLE, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
    Text(CheckInCopy.unsafeBody(sheet.target.name), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
    InfoNote(CheckInCopy.DANGER, tone = Tone.Danger)
    UsButton(text = CheckInCopy.reportAction(sheet.target.name), onClick = viewModel::startReport, modifier = Modifier.fillMaxWidth())
    UsSecondaryButton(text = CheckInCopy.NOT_NOW, onClick = viewModel::dismissSheet, modifier = Modifier.fillMaxWidth())
}

@Composable
private fun Question(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
}

private const val YES = "yes"
private const val NO = "no"
