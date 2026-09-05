package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.theme.UsTheme

/** The four surfaces of the one chat screen (founder, 2026-09-05). */
enum class ChatHomeTab(val label: String) {
    Chats("Chats"),
    Groups("Groups"),
    Communities("Communities"),
    Suggestions("Suggestions"),
}

/**
 * "Chats | Groups | Communities | Suggestions" — the home feed's tab row
 * (FeedTabsRow) worn by Messages: four equal columns at 44dp, the label
 * 13sp bold, the active one in white over a 2dp WHITE underline the width
 * of the label, the others muted; a 1dp `borderMedium` rule closes the row.
 * Chats and Groups carry an unread dot beside the label while anything in
 * them is unread.
 *
 * Every tab is a `Role.Tab` in a selectable group, so TalkBack announces
 * "Groups, tab, 2 of 4, selected" rather than four unrelated buttons.
 */
@Composable
internal fun ChatHomeTabsRow(
    selected: ChatHomeTab,
    unread: Set<ChatHomeTab>,
    onSelect: (ChatHomeTab) -> Unit,
    modifier: Modifier = Modifier,
) {
    val rule = UsTheme.extended.borderMedium
    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(TABS_ROW_HEIGHT)
            .drawBehind {
                val stroke = RULE_HEIGHT.toPx()
                drawLine(
                    color = rule,
                    start = Offset(0f, size.height - stroke / 2),
                    end = Offset(size.width, size.height - stroke / 2),
                    strokeWidth = stroke,
                )
            }
            .selectableGroup()
            .testTag("chat_home_tabs"),
    ) {
        ChatHomeTab.entries.forEach { tab ->
            ChatHomeTabItem(
                tab = tab,
                active = tab == selected,
                unread = tab in unread,
                onClick = { onSelect(tab) },
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun ChatHomeTabItem(
    tab: ChatHomeTab,
    active: Boolean,
    unread: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxHeight()
            .selectable(selected = active, role = Role.Tab, onClick = onClick)
            .testTag("chat_home_tab:${tab.name.lowercase()}"),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            modifier = Modifier.width(IntrinsicSize.Max),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontSize = TAB_TEXT,
                    fontWeight = FontWeight.Bold,
                    color = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                    maxLines = 1,
                )
                if (unread) {
                    Box(
                        modifier = Modifier
                            .size(UNREAD_DOT)
                            .background(UsTheme.extended.accentSolid, CircleShape)
                            .testTag("chat_home_tab_unread:${tab.name.lowercase()}"),
                    )
                }
            }
            // Always laid out, transparent when inactive, so the label does
            // not shift by 2dp when the selection moves.
            Box(
                modifier = Modifier
                    .padding(top = UNDERLINE_GAP)
                    .fillMaxWidth()
                    .height(UNDERLINE_HEIGHT)
                    .background(if (active) UsTheme.extended.textPrimary else Color.Transparent),
            )
        }
    }
}

private val TABS_ROW_HEIGHT = 44.dp
private val RULE_HEIGHT = 1.dp
private val UNDERLINE_HEIGHT = 2.dp
private val UNDERLINE_GAP = 4.dp
private val UNREAD_DOT = 6.dp
private val TAB_TEXT = 13.sp
