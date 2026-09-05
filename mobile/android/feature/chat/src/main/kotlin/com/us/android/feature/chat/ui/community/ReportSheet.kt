package com.us.android.feature.chat.ui.community

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.chat.ui.home.ChatTogglePill

/** The reasons a report can carry; the wire value is the first, the label the second. */
private val REASONS = listOf(
    "spam" to "Spam",
    "harassment" to "Harassment",
    "hate" to "Hate",
    "misinformation" to "Misinformation",
    "other" to "Something else",
)

/** A short sheet: pick a reason, add a line, send. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReportSheet(
    title: String,
    onDismiss: () -> Unit,
    onSend: (reason: String, details: String) -> Unit,
) {
    var reason by rememberSaveable { mutableStateOf(REASONS.first().first) }
    var details by rememberSaveable { mutableStateOf("") }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        containerColor = UsTheme.extended.bgCardSolid,
        modifier = Modifier.testTag("chat_report_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxxxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            REASONS.chunked(REASONS_PER_ROW).forEach { row ->
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    row.forEach { (value, label) ->
                        ChatTogglePill(
                            text = label,
                            selected = value == reason,
                            onClick = { reason = value },
                            tag = "chat_report_reason:$value",
                        )
                    }
                }
            }
            ChatFormField(
                value = details,
                onValueChange = { details = it },
                label = "Anything we should know?",
                placeholder = "Optional",
                singleLine = false,
                minLines = DETAIL_LINES,
                tag = "chat_report_details",
            )
            UsButton(
                text = "Send report",
                onClick = { onSend(reason, details.trim()) },
                modifier = Modifier.fillMaxWidth().testTag("chat_report_send"),
            )
        }
    }
}

private const val DETAIL_LINES = 2
private const val REASONS_PER_ROW = 3
