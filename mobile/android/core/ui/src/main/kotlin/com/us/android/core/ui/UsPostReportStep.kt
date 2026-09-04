package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The report step, INSIDE the "more" sheet: a back arrow and "Report" in
 * the header, the eleven reasons as rows with a check on the chosen one, a
 * details field that appears for "Other", and one submit button.
 *
 * The outcome is the host's ([UsPostReportState]): while it is
 * [UsPostReportState.Sending] the button spins; [UsPostReportState.Sent] and
 * [UsPostReportState.AlreadyReported] replace the list with their one
 * sentence, and the sheet closes itself a beat later; a
 * [UsPostReportState.Failed] keeps the list and offers "Try again".
 *
 * Scrolls as a whole: eleven rows plus a field can outgrow a short screen,
 * and the keyboard for "Other" lifts the field with `imePadding`.
 */
@Composable
internal fun UsPostReportStep(
    report: UsPostReportState,
    onBack: () -> Unit,
    onSubmit: (reason: UsReportReason, details: String) -> Unit,
) {
    var chosen by rememberSaveable { mutableStateOf<UsReportReason?>(null) }
    var details by rememberSaveable { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("post_more_report"),
    ) {
        ReportHeader(onBack = onBack, showBack = report !is UsPostReportState.Sent)
        when (report) {
            UsPostReportState.Sent -> ReportOutcomeView(
                title = "Thanks for reporting",
                detail = "We'll take a look at this post.",
            )

            UsPostReportState.AlreadyReported -> ReportOutcomeView(
                title = "You've already reported this",
                detail = "We're still reviewing your earlier report.",
            )

            else -> ReportForm(
                report = report,
                chosen = chosen,
                details = details,
                onChoose = { chosen = it },
                onDetails = { details = it },
                onSubmit = { chosen?.let { onSubmit(it, details.trim()) } },
            )
        }
    }
}

/** The back arrow on the left, "Report" centred. */
@Composable
private fun ReportHeader(onBack: () -> Unit, showBack: Boolean) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(HEADER_HEIGHT),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "Report",
            style = MaterialTheme.typography.titleMedium.copy(fontSize = HEADER_TEXT_SIZE),
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        if (showBack) {
            val interaction = remember { MutableInteractionSource() }
            Icon(
                imageVector = UsIcons.Back,
                contentDescription = "Back to options",
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .align(Alignment.CenterStart)
                    .padding(start = SIDE)
                    .sheetPressScale(interaction)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        role = Role.Button,
                        onClick = onBack,
                    )
                    .size(BACK_GLYPH)
                    .testTag("post_more_report_back"),
            )
        }
    }
}

/** The reasons, the details field for "Other", the error line, the button. */
@Suppress("LongParameterList")
@Composable
private fun ReportForm(
    report: UsPostReportState,
    chosen: UsReportReason?,
    details: String,
    onChoose: (UsReportReason) -> Unit,
    onDetails: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    val sending = report == UsPostReportState.Sending
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .imePadding(),
    ) {
        Text(
            text = "Why are you reporting this post?",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(horizontal = SIDE, vertical = UsTheme.spacing.m),
        )
        UsReportReason.entries.forEach { reason ->
            SheetRow(
                icon = null,
                label = reason.label,
                enabled = !sending,
                onClick = { onChoose(reason) },
                trailing = if (reason == chosen) {
                    { ChosenCheck() }
                } else {
                    null
                },
                testTag = "post_more_reason:${reason.wire}",
                modifier = Modifier.height(REASON_ROW_HEIGHT),
            )
        }
        if (chosen?.asksForDetails == true) {
            DetailsField(value = details, enabled = !sending, onValueChange = onDetails)
        }
        if (report == UsPostReportState.Failed) {
            Text(
                text = "We couldn't send your report. Try again.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.liveRed,
                modifier = Modifier.padding(horizontal = SIDE, vertical = UsTheme.spacing.m),
            )
        }
        Spacer(Modifier.height(UsTheme.spacing.l))
        UsButton(
            text = if (report == UsPostReportState.Failed) "Try again" else "Submit report",
            onClick = onSubmit,
            enabled = chosen != null,
            loading = sending,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = SIDE)
                .testTag("post_more_report_submit"),
        )
    }
}

@Composable
private fun ChosenCheck() {
    Icon(
        imageVector = UsIcons.Check,
        contentDescription = "Selected",
        tint = UsTheme.extended.accentSolid,
        modifier = Modifier.size(CHECK_GLYPH),
    )
}

/** A raised field with a hairline, like the comment composer's. */
@Composable
private fun DetailsField(value: String, enabled: Boolean, onValueChange: (String) -> Unit) {
    val shape = RoundedCornerShape(FIELD_RADIUS)
    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        enabled = enabled,
        maxLines = FIELD_MAX_LINES,
        textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
        cursorBrush = SolidColor(UsTheme.extended.accentSolid),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = SIDE, vertical = UsTheme.spacing.m)
            .heightIn(min = FIELD_MIN_HEIGHT)
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(horizontal = FIELD_PADDING_H, vertical = FIELD_PADDING_V)
            .semantics { contentDescription = "Report details" }
            .testTag("post_more_report_details"),
        decorationBox = { inner ->
            Box(contentAlignment = Alignment.TopStart) {
                if (value.isEmpty()) {
                    Text(
                        text = "Tell us more (optional)",
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textDim,
                    )
                }
                inner()
            }
        },
    )
}

/** The settled states: one sentence, centred, in place of the list. */
@Composable
private fun ReportOutcomeView(title: String, detail: String) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = SIDE)
            .padding(top = UsTheme.spacing.xxl, bottom = OUTCOME_BOTTOM)
            .testTag("post_more_report_outcome"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(
            imageVector = UsIcons.Check,
            contentDescription = null,
            tint = UsTheme.extended.statusSuccess,
            modifier = Modifier.size(OUTCOME_GLYPH),
        )
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge.copy(fontSize = OUTCOME_TITLE_SIZE),
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = detail,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
    }
}

private const val FIELD_MAX_LINES = 4

private val SIDE = 18.dp
private val HEADER_HEIGHT = 44.dp
private val HEADER_TEXT_SIZE = 16.sp
private val BACK_GLYPH = 22.dp
private val REASON_ROW_HEIGHT = 46.dp
private val CHECK_GLYPH = 20.dp
private val HAIRLINE = 1.dp
private val FIELD_RADIUS = 16.dp
private val FIELD_MIN_HEIGHT = 72.dp
private val FIELD_PADDING_H = 16.dp
private val FIELD_PADDING_V = 12.dp
private val OUTCOME_GLYPH = 32.dp
private val OUTCOME_TITLE_SIZE = 18.sp
private val OUTCOME_BOTTOM = 28.dp
