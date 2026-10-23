package com.us.android.core.memberships.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The members-only lock (paid memberships, 2026-10-23) — drawn INSTEAD of a
 * post's body, a reel's caption or a long video's player whenever
 * [com.us.android.core.model.MembersOnly.access] says Locked. It names what
 * is behind it (the [title] the server keeps on a redacted row, the video's
 * length) and offers Join, which opens [JoinMembershipSheet] for
 * [creatorId]. [onUnlocked] runs once the server confirmed the payment: the
 * host re-reads the post.
 */
@Composable
fun MembersOnlyLock(
    creatorId: String,
    creatorName: String,
    requiredLevelId: String?,
    onUnlocked: () -> Unit,
    modifier: Modifier = Modifier,
    style: MembersOnlyLockStyle = MembersOnlyLockStyle.Card,
    title: String = "",
    durationMs: Long = 0L,
    /** The viewer is signed out: Join would have nobody to charge, so it is not offered. */
    canJoin: Boolean = true,
    /** The level's price as the post carried it, for the Join sheet's "Includes this post". */
    requiredPricePaise: Long? = null,
) {
    var joining by rememberSaveable(creatorId) { mutableStateOf(false) }
    val onMedia = style == MembersOnlyLockStyle.OnMedia
    val ink = if (onMedia) UsTheme.extended.onMedia else UsTheme.extended.textPrimary
    val muted = if (onMedia) UsTheme.extended.onMediaMuted else UsTheme.extended.textMuted
    val shape = RoundedCornerShape(UsTheme.radii.card)
    Column(
        modifier = modifier
            .then(
                if (onMedia) {
                    Modifier
                } else {
                    Modifier
                        .clip(shape)
                        .background(UsTheme.extended.bgCardSolid, shape)
                        .border(1.dp, UsTheme.extended.borderSubtle, shape)
                },
            )
            .padding(UsTheme.spacing.xxl)
            .semantics { contentDescription = "Members only" }
            .testTag("members_only_lock"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box(
            modifier = Modifier
                .size(LOCK_DISC)
                .clip(CircleShape)
                .background(if (onMedia) UsTheme.extended.mediaPlate else UsTheme.extended.fillSubtle),
            contentAlignment = Alignment.Center,
        ) {
            Icon(imageVector = UsIcons.Lock, contentDescription = null, tint = ink, modifier = Modifier.size(LOCK_ICON))
        }
        Text(
            text = "Members only",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = ink,
        )
        if (title.isNotBlank()) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyMedium,
                color = ink,
                textAlign = TextAlign.Center,
                maxLines = 2
            )
        }
        Text(
            text = lockDetail(creatorName, durationMs),
            style = MaterialTheme.typography.bodySmall,
            color = muted,
            textAlign = TextAlign.Center,
        )
        if (canJoin) {
            UsPillButton(text = "Join", onClick = { joining = true }, modifier = Modifier.testTag("members_only_join"))
        } else {
            Text(text = "Sign in to join.", style = MaterialTheme.typography.labelLarge, color = ink)
        }
    }
    if (joining) {
        JoinMembershipSheet(
            creatorId = creatorId,
            creatorName = creatorName,
            requiredLevelId = requiredLevelId,
            requiredPricePaise = requiredPricePaise,
            onDismiss = { joining = false },
            onUnlocked = onUnlocked,
        )
    }
}

/** A small "Members" chip for a members-only post this viewer may see. */
@Composable
fun MembersOnlyBadge(modifier: Modifier = Modifier, onMedia: Boolean = false) {
    val tint = if (onMedia) UsTheme.extended.onMedia else UsTheme.extended.accentSolid
    Row(
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(if (onMedia) UsTheme.extended.mediaPlate else UsTheme.extended.fillSubtle)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("members_only_badge"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = UsIcons.Award, contentDescription = null, tint = tint, modifier = Modifier.size(BADGE_ICON))
        Text(
            text = "Members",
            style = MaterialTheme.typography.labelSmall,
            color = tint,
            fontWeight = FontWeight.SemiBold
        )
    }
}

/** "Join Asha to watch this 12 min video." — the length only when the row kept one. */
internal fun lockDetail(creatorName: String, durationMs: Long): String {
    val who = creatorName.ifBlank { "this creator" }
    val minutes = durationMs / MILLIS_PER_MINUTE
    return when {
        durationMs <= 0L -> "Join $who's membership to see this."
        minutes < 1 -> "Join $who's membership to watch this video."
        else -> "Join $who's membership to watch this $minutes min video."
    }
}

private const val MILLIS_PER_MINUTE = 60_000L
private val LOCK_DISC = 56.dp
private val LOCK_ICON = 26.dp
private val BADGE_ICON = 14.dp
