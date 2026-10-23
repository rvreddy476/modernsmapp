package com.us.android.core.feed.ui.action

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionStatus
import com.us.android.core.model.PostAction

/** Where a pill sits: on a card (post page, home card, watch page) or over a video (reels, the home stage). */
enum class ActionPillStyle { Card, OnMedia }

/**
 * An item's Action button: "Apply now · epfindia.gov.in" (2026-10-23).
 *
 * Give it the row's `action` as it came; it decides what to draw through
 * [ActionButtons.visible] — nothing for a viewer when the button is not
 * live or the item is [locked] (members-only), the author's own button with
 * its status when it is not live (blocked or under review, which a tap
 * does not open). A tap goes through the links route and the leaving page
 * ([rememberActionLinkOpener]), never straight to a url.
 */
@Composable
fun ActionPill(
    action: PostAction?,
    modifier: Modifier = Modifier,
    /** Whether the viewer wrote it; by default, whether the row is the author's copy ([PostAction.isAuthorCopy]). */
    isAuthor: Boolean = action?.isAuthorCopy == true,
    locked: Boolean = false,
    style: ActionPillStyle = ActionPillStyle.Card,
) {
    val shown = ActionButtons.visible(action, isAuthor = isAuthor, locked = locked) ?: return
    val open = rememberActionLinkOpener()
    val live = shown.status == ActionStatus.ACTIVE
    val onMedia = style == ActionPillStyle.OnMedia
    val ink = when {
        onMedia -> UsTheme.extended.onMedia
        live -> UsTheme.extended.textPrimary
        else -> UsTheme.extended.textMuted
    }
    val shape = RoundedCornerShape(UsTheme.radii.pill)
    val words = if (live) shown.pillText else "${ActionButtons.statusNote(shown)?.title} · ${shown.labelText}"
    Row(
        modifier = modifier
            .clip(shape)
            .then(
                if (onMedia) {
                    Modifier.background(UsTheme.extended.mediaPlate, shape)
                } else {
                    Modifier.background(UsTheme.extended.fillSubtle, shape).border(1.dp, UsTheme.extended.borderSubtle, shape)
                },
            )
            .clickable(enabled = live, role = Role.Button) { open(shown) }
            .defaultMinSize(minHeight = PILL_HEIGHT)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics { contentDescription = "$words, opens a website" }
            .testTag("action_pill"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(
            imageVector = if (live) UsIcons.Link else UsIcons.ShieldAlert,
            contentDescription = null,
            tint = if (onMedia) ink else if (live) UsTheme.extended.accentSolid else UsTheme.extended.statusWarning,
            modifier = Modifier.size(GLYPH),
        )
        Text(
            text = words,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = ink,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
        if (live) {
            Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = ink, modifier = Modifier.size(GLYPH))
        }
    }
}

/**
 * The profile / channel Action button, beside Follow and Message: the label
 * alone ("Book now"), outlined like the Message button it sits next to.
 * Same rules as [ActionPill]: a viewer sees only a live one.
 */
@Composable
fun ProfileActionButton(
    action: PostAction?,
    modifier: Modifier = Modifier,
    isOwner: Boolean = action?.isAuthorCopy == true,
) {
    val shown = ActionButtons.visible(action, isAuthor = isOwner) ?: return
    val open = rememberActionLinkOpener()
    val live = shown.status == ActionStatus.ACTIVE
    val shape = RoundedCornerShape(UsTheme.radii.pill)
    Row(
        modifier = modifier
            .clip(shape)
            .border(1.dp, UsTheme.extended.borderMedium, shape)
            .clickable(enabled = live, role = Role.Button) { open(shown) }
            .defaultMinSize(minHeight = PROFILE_HEIGHT)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics { contentDescription = "${shown.pillText}, opens a website" }
            .testTag("profile_action_button"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs, Alignment.CenterHorizontally),
    ) {
        Icon(
            imageVector = if (live) UsIcons.Link else UsIcons.ShieldAlert,
            contentDescription = null,
            tint = if (live) UsTheme.extended.accentSolid else UsTheme.extended.statusWarning,
            modifier = Modifier.size(GLYPH),
        )
        Text(
            text = shown.labelText,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = if (live) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * "12 taps · 3 this week" for the creator's own item that has a button
 * (`GET /v1/posts/:id/action/stats`). Draws nothing until the count is read,
 * and nothing when it cannot be.
 */
@Composable
fun ActionTaps(
    postId: String,
    modifier: Modifier = Modifier,
    viewModel: ActionStatsViewModel = hiltViewModel(key = "action-stats"),
) {
    LaunchedEffect(postId) { viewModel.ensure(postId) }
    val stats by viewModel.stats.collectAsStateWithLifecycle()
    val read = stats[postId] ?: return
    Row(
        modifier = modifier.testTag("action_taps"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = UsIcons.Link, contentDescription = null, tint = UsTheme.extended.textMuted, modifier = Modifier.size(GLYPH))
        Text(
            text = "Action button: ${read.tapsText}",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

private val PILL_HEIGHT = 36.dp
private val PROFILE_HEIGHT = 32.dp
private val GLYPH = 16.dp
