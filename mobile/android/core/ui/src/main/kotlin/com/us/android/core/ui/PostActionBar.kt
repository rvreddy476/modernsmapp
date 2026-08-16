// MatchingDeclarationName: this file's primary export is the PostActionBar
// composable; PostActionState is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.FavoriteBorder
import androidx.compose.material.icons.filled.MailOutline
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.disabled
import androidx.compose.ui.semantics.role
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The interaction state a post's action bar renders.
 *
 * [canReact] and [canComment] are author-set switches, not viewer permissions.
 * A post with comments turned off shows a disabled control rather than hiding
 * it, so the absence is legible instead of looking like a rendering bug.
 */
@Immutable
data class PostActionState(
    val likeCount: Int,
    val commentCount: Int,
    val repostCount: Int,
    val hasReacted: Boolean,
    val isBookmarked: Boolean,
    val canReact: Boolean = true,
    val canComment: Boolean = true,
    val canRepost: Boolean = true,
    /** True while any action is in flight; the whole bar goes inert. */
    val busy: Boolean = false,
)

/**
 * Like, comment, repost and bookmark.
 *
 * Lives in `:core:ui` because the feed, the post detail screen and later the
 * reels overlay all render exactly this row, and the accessibility contract
 * below must not diverge between them.
 *
 * Each control merges its icon and count into a single semantic node reading
 * "Like, 12" rather than exposing an unlabelled icon beside a bare number, and
 * every target is at least 48dp. Rendered naively this row is four unlabelled
 * glyphs — the single most common accessibility failure in a social feed.
 */
@Composable
fun PostActionBar(
    state: PostActionState,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ActionButton(
            icon = if (state.hasReacted) Icons.Filled.Favorite else Icons.Filled.FavoriteBorder,
            label = "Like",
            count = state.likeCount,
            active = state.hasReacted,
            enabled = state.canReact && !state.busy,
            activeTint = MaterialTheme.colorScheme.error,
            onClick = onReact,
        )
        ActionButton(
            icon = Icons.Filled.MailOutline,
            label = "Comment",
            count = state.commentCount,
            active = false,
            enabled = state.canComment && !state.busy,
            onClick = onComment,
        )
        ActionButton(
            icon = Icons.AutoMirrored.Filled.Send,
            label = "Repost",
            count = state.repostCount,
            active = false,
            enabled = state.canRepost && !state.busy,
            onClick = onRepost,
        )
        // The icon set has no outlined star, so saved state is carried by a
        // word as well as a tint. Colour alone would be the only signal, which
        // fails for anyone who cannot distinguish it.
        ActionButton(
            icon = Icons.Filled.Star,
            label = "Bookmark",
            count = null,
            text = if (state.isBookmarked) "Saved" else "Save",
            active = state.isBookmarked,
            enabled = !state.busy,
            activeTint = UsTheme.extended.statusWarning,
            onClick = onBookmark,
        )
    }
}

@Composable
private fun ActionButton(
    icon: ImageVector,
    label: String,
    count: Int?,
    active: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    text: String? = null,
    activeTint: Color = Color.Unspecified,
) {
    val tint = when {
        !enabled -> UsTheme.extended.textGhost
        active && activeTint != Color.Unspecified -> activeTint
        else -> UsTheme.extended.textSecondary
    }
    // The whole control is one node. Exposing the icon and the count
    // separately makes a screen reader read "button, 12" with no idea what
    // the twelve refers to.
    val readable = buildString {
        append(if (active) "$label, selected" else label)
        if (count != null) append(", $count")
    }
    val caption = text ?: count?.let { formatCount(it) }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .sizeIn(minWidth = 48.dp, minHeight = 48.dp)
            .clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = UsTheme.spacing.m)
            .clearAndSetSemantics {
                contentDescription = readable
                role = Role.Button
                if (!enabled) disabled()
            },
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint)
        if (caption != null) {
            Text(
                text = caption,
                style = MaterialTheme.typography.bodySmall,
                color = tint,
            )
        }
    }
}

private val previewState = PostActionState(
    likeCount = 128,
    commentCount = 12,
    repostCount = 3,
    hasReacted = false,
    isBookmarked = false,
)

@Preview(name = "Action bar", showBackground = true)
@Composable
private fun PostActionBarPreview() {
    UsTheme { PostActionBar(previewState, {}, {}, {}, {}) }
}

@Preview(name = "Action bar — engaged", showBackground = true)
@Composable
private fun PostActionBarActivePreview() {
    UsTheme {
        PostActionBar(
            state = previewState.copy(hasReacted = true, isBookmarked = true, likeCount = 129),
            onReact = {},
            onComment = {},
            onRepost = {},
            onBookmark = {},
        )
    }
}

@Preview(name = "Action bar — author disabled comments and likes", showBackground = true)
@Composable
private fun PostActionBarRestrictedPreview() {
    UsTheme {
        PostActionBar(
            state = previewState.copy(canComment = false, canReact = false),
            onReact = {},
            onComment = {},
            onRepost = {},
            onBookmark = {},
        )
    }
}

@Preview(name = "Action bar — busy", showBackground = true)
@Composable
private fun PostActionBarBusyPreview() {
    UsTheme { PostActionBar(previewState.copy(busy = true), {}, {}, {}, {}) }
}
