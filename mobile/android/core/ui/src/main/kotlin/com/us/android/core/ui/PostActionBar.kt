// MatchingDeclarationName: this file's primary export is the PostActionBar
// composable; PostActionState is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.sizeIn
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
import com.us.android.core.designsystem.icon.UsIcons
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
    /**
     * Whether THIS viewer has reposted.
     *
     * The control used to be hardcoded inactive, so a post the viewer had
     * already reposted looked untouched and the only affordance offered was to
     * repost it again — which the server rejects with `409 ALREADY_REPOSTED`.
     * An action whose current state is invisible cannot be undone.
     */
    val hasReposted: Boolean = false,
    val isBookmarked: Boolean,
    val canReact: Boolean = true,
    val canComment: Boolean = true,
    val canRepost: Boolean = true,
    /** True while any action is in flight; the whole bar goes inert. */
    val busy: Boolean = false,
)

/**
 * Like, comment, repost, share and save.
 *
 * Lives in `:core:ui` because the feed, the post detail screen and the reels
 * overlay all render exactly this row, and the accessibility contract below
 * must not diverge between them.
 *
 * NO WORDS. Every control is its icon and, where it has one, its count. A
 * label under each glyph is five words of chrome repeated on every post in an
 * infinite list, and it pushes the content — which is the reason anyone is
 * here — further down the screen on every single row.
 *
 * That puts the entire burden on the icons being unambiguous, which is why
 * they are drawn for this product rather than borrowed, and why on/off states
 * change FILL and not just colour. Each control still merges its icon and
 * count into one semantic node reading "Like, 12", so what a screen reader
 * hears is unaffected by what the eye is spared.
 */
@Composable
fun PostActionBar(
    state: PostActionState,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ActionButton(
            icon = if (state.hasReacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            label = "Like",
            count = state.likeCount,
            active = state.hasReacted,
            enabled = state.canReact && !state.busy,
            activeTint = UsTheme.extended.liveRed,
            onClick = onReact,
        )
        ActionButton(
            icon = UsIcons.Comment,
            label = "Comment",
            count = state.commentCount,
            active = false,
            enabled = state.canComment && !state.busy,
            onClick = onComment,
        )
        ActionButton(
            icon = UsIcons.Repost,
            label = "Repost",
            count = state.repostCount,
            active = state.hasReposted,
            enabled = state.canRepost && !state.busy,
            activeTint = UsTheme.extended.statusSuccess,
            onClick = onRepost,
        )
        ActionButton(
            icon = UsIcons.Share,
            label = "Share",
            count = null,
            active = false,
            enabled = !state.busy,
            onClick = onShare,
        )

        // Save is pushed to the far edge. Everything to its left broadcasts;
        // save acts on the post for this viewer alone. Sitting them flush
        // together invites a private save to be read as one more way to post.
        Spacer(Modifier.weight(1f))

        ActionButton(
            icon = if (state.isBookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
            label = "Save",
            count = null,
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
    // A zero is not information. Showing "0" three times on every new post
    // makes an empty feed look like a failed one; the count appears the moment
    // there is something to count.
    val caption = count?.takeIf { it > 0 }?.let { formatCount(it) }
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
    UsTheme { PostActionBar(previewState, {}, {}, {}, {}, {}) }
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
            onShare = {},
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
            onShare = {},
        )
    }
}

@Preview(name = "Action bar — busy", showBackground = true)
@Composable
private fun PostActionBarBusyPreview() {
    UsTheme { PostActionBar(previewState.copy(busy = true), {}, {}, {}, {}, {}) }
}
