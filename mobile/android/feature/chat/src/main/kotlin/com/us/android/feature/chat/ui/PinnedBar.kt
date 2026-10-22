package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PinnedMessage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The pinned bar at the top of a thread (web parity, 2026-10-22): one pin at
 * a time — "Pinned message 2 of 3" and a line of it — and a tap scrolls to
 * that message and steps on to the next pin, wrapping, as Telegram and the
 * web do. The × unpins the one shown.
 */
@Composable
internal fun PinnedBar(
    pin: PinnedMessage,
    index: Int,
    count: Int,
    message: Message?,
    onOpen: () -> Unit,
    onUnpin: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(UsTheme.extended.bgCardSolid)
                .clickable(role = Role.Button, onClick = onOpen)
                .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
                .semantics { contentDescription = "${pinnedBarTitle(index, count)}. Show it" }
                .testTag("thread-pinned-bar"),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Icon(
                imageVector = UsIcons.Pin,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(PIN_GLYPH),
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = pinnedBarTitle(index, count),
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textSecondary,
                )
                Text(
                    text = pinnedBarPreview(message),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.testTag("thread-pinned-text:${pin.messageId}"),
                )
            }
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(UNPIN_TARGET)
                    .clickable(role = Role.Button, onClick = onUnpin)
                    .semantics { contentDescription = "Unpin" }
                    .testTag("thread-pinned-unpin"),
            ) {
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = null,
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier.size(UNPIN_GLYPH),
                )
            }
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}

/** "Pinned message", or "Pinned message 2 of 3" when there are several (the web's words). */
internal fun pinnedBarTitle(index: Int, count: Int): String =
    if (count > 1) "Pinned message ${index.mod(count) + 1} of $count" else "Pinned message"

/**
 * The line under the title: the message's text, "Attachment" for one with
 * none, "This message was deleted" for one unsent, and "Tap to see the
 * message" while it is further back than the loaded history.
 */
internal fun pinnedBarPreview(message: Message?): String = when {
    message == null -> "Tap to see the message"
    message.deleted -> "This message was deleted"
    message.text.isNotBlank() -> message.text
    else -> "Attachment"
}

private val PIN_GLYPH = 16.dp
private val UNPIN_TARGET = 32.dp
private val UNPIN_GLYPH = 14.dp
