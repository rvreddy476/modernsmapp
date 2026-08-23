package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure

/**
 * Tells the viewer that an engagement action did not stick, and offers a way out.
 *
 * WHY THIS EXISTS
 *
 * Both feed and post detail collected failures into state that nothing ever
 * rendered. A like would silently roll back with no explanation, which reads as
 * the app ignoring the tap — and leaves the viewer no way to complete the
 * action short of guessing that tapping again might work.
 *
 * Two exits, both explicit: retry the same intent, or dismiss and accept the
 * rolled-back state. There is no auto-retry, because a failure the user cannot
 * see repeating itself is how a rate limit becomes a ban.
 *
 * The row is a polite live region, so a screen reader announces the failure
 * when it appears rather than leaving it to be discovered by exploration.
 */
@Composable
fun EngagementFailureBar(
    failures: List<EngagementFailure>,
    onRetry: (postId: String, action: EngagementAction) -> Unit,
    onDismiss: (postId: String, action: EngagementAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (failures.isEmpty()) return
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        failures.forEach { failure ->
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.bgCardHover)
                    .padding(UsTheme.spacing.l)
                    .semantics { liveRegion = LiveRegionMode.Polite },
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    text = failure.action.failureMessage(),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.weight(1f),
                )
                UsSecondaryButton(
                    text = "Retry",
                    onClick = { onRetry(failure.postId, failure.action) },
                )
                UsSecondaryButton(
                    text = "Dismiss",
                    onClick = { onDismiss(failure.postId, failure.action) },
                )
            }
        }
    }
}

/**
 * Names the action, not the transport.
 *
 * "Couldn't save that like" is actionable; the underlying AppError is a
 * network/HTTP detail the viewer cannot do anything with, and surfacing it
 * risks leaking identifiers into the UI.
 */
private fun EngagementAction.failureMessage(): String = when (this) {
    EngagementAction.REACTION -> "Couldn't save your like."
    EngagementAction.BOOKMARK -> "Couldn't save this post."
    EngagementAction.REPOST -> "Couldn't repost that."
}
