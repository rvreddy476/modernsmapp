package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.credits.CreditGroup
import com.us.android.core.feed.data.credits.creditFactsLine
import com.us.android.core.feed.data.credits.creditGroups
import com.us.android.core.model.VideoCredit
import com.us.android.core.model.VideoCredits

/**
 * The Credits block under the description (video credits, 2026-10-23):
 * "Drama, Thriller · 2024 · Blue Hour Films" on one line, then the people
 * grouped by role — "Cast: A, B", "Director: D" — in the order the creator
 * listed them. A name opens Tube search for that name; a credit the person
 * ACCEPTED shows their avatar and opens their channel instead. A viewer
 * never sees a pending or declined link: those are plain names.
 */
/**
 * Whether the Credits block is drawn: only when there are people in it. The
 * list comes from the post DETAIL (`GET /v1/posts/:id`, laid over the row by
 * `withViewerStateOf`) — a feed or list row never carries `credits`, though
 * it does carry the genres, year and house — so a video opened from a list
 * shows the block once the detail lands, and a video with no credits (absent,
 * `[]`, or `""`, all read as none) shows nothing.
 */
internal fun watchCreditsShown(credits: VideoCredits): Boolean = credits.credits.isNotEmpty()

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun CreditsBlock(
    credits: VideoCredits,
    genreLabel: (String) -> String?,
    onSearchName: (String) -> Unit,
    onOpenUser: (userId: String) -> Unit,
    /** The viewer, so their own accepted credit offers "Remove me from this video". */
    ownUserId: String = "",
    onRemoveMe: (creditId: String) -> Unit = {},
) {
    val facts = creditFactsLine(credits, genreLabel)
    val mine = credits.credits.firstOrNull { ownUserId.isNotBlank() && it.user?.id == ownUserId && it.id.isNotBlank() }
    val groups = creditGroups(credits.credits)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("watch_credits"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Text(
            text = "Credits",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { heading() },
        )
        if (facts.isNotEmpty()) {
            Text(
                text = facts,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.testTag("watch_credits_facts"),
            )
        }
        groups.forEach { group -> GroupLine(group = group, onSearchName = onSearchName, onOpenUser = onOpenUser) }
        mine?.let { credit ->
            Text(
                text = "Remove me from this video",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.accentSolid,
                modifier = Modifier
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onRemoveMe(credit.id) }
                    .semantics { role = Role.Button }
                    .padding(vertical = UsTheme.spacing.xs)
                    .testTag("watch_credits_remove_me"),
            )
        }
    }
}

/** "Cast: A, B" — the label, then each name as its own target. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun GroupLine(group: CreditGroup, onSearchName: (String) -> Unit, onOpenUser: (String) -> Unit) {
    FlowRow(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("watch_credits_group:${group.label.lowercase()}"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = "${group.label}:",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        group.credits.forEachIndexed { index, credit ->
            CreditName(
                credit = credit,
                trailingComma = index < group.credits.lastIndex,
                onSearchName = onSearchName,
                onOpenUser = onOpenUser,
            )
        }
    }
}

@Composable
private fun CreditName(
    credit: VideoCredit,
    trailingComma: Boolean,
    onSearchName: (String) -> Unit,
    onOpenUser: (String) -> Unit,
) {
    val user = credit.user
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
            ) { if (user != null) onOpenUser(user.id) else onSearchName(credit.name) }
            .semantics {
                role = Role.Button
                contentDescription = if (user != null) {
                    "${credit.name}, ${credit.role}. Open channel"
                } else {
                    "${credit.name}, ${credit.role}. Search"
                }
            }
            .testTag("watch_credit:${credit.id.ifBlank { credit.name }}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        if (user != null) {
            UsAvatar(name = user.displayName, seed = user.id, size = UsAvatarSize.Small, imageUrl = user.avatarUrl)
        }
        Text(
            text = if (trailingComma) "${credit.name}," else credit.name,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
    }
}
