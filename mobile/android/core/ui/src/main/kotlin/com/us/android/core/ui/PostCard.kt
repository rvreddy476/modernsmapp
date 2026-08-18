// MatchingDeclarationName: this file's primary export is the PostCard
// composable; PostCardState is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Everything a feed row renders.
 *
 * A view-model-free value type: the feed builds it from a `FeedItem`, search
 * will build it from a search hit, and a profile grid from an author's posts.
 * None of those share a domain model, which is why the card takes its own
 * shape rather than any one of theirs.
 */
@Immutable
data class PostCardState(
    val postId: String,
    val authorId: String,
    val authorName: String,
    val text: String,
    val timestamp: String,
    /** `text`, `image`, `video`, … */
    val postType: String,
    val mediaCount: Int,
    /**
     * Aspect ratio of the first attachment, from the server's real width and
     * height. Reserving the true ratio before the bytes arrive is what stops
     * the row resizing mid-scroll. Defaults to 16:9 when unknown.
     */
    val mediaAspectRatio: Float = DEFAULT_MEDIA_ASPECT,
    val actions: PostActionState,
    val isPinned: Boolean = false,
)

/**
 * One post in a list.
 *
 * The whole card is clickable and the action bar sits inside it, so the row
 * has a click target nested in a click target. Compose resolves that
 * correctly, but a screen reader would otherwise announce the card's text
 * twice — once as the card and once per action — so the header and body are
 * grouped and the actions keep their own labelled nodes.
 *
 * Text is capped at [MAX_LINES]. A feed row that grows to a thousand words
 * destroys scroll performance and hides everything after it.
 */
@Composable
fun PostCard(
    state: PostCardState,
    onClick: () -> Unit,
    onAuthorClick: () -> Unit,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier.clickable(onClick = onAuthorClick),
        ) {
            UsAvatar(
                name = state.authorName,
                size = UsAvatarSize.Small,
                seed = state.authorId,
            )
            Column {
                Text(
                    text = state.authorName,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.semantics { heading() },
                )
                // Omitted rather than shown blank: the formatter returns an
                // empty string for a timestamp it cannot parse, and an empty
                // line under the name looks like a layout fault.
                if (state.timestamp.isNotBlank()) {
                    Text(
                        text = state.timestamp,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                }
            }
            if (state.isPinned) {
                Text(
                    text = "Pinned",
                    style = MaterialTheme.typography.labelSmall,
                    color = UsTheme.extended.statusWarning,
                )
            }
        }

        if (state.text.isNotBlank()) {
            Text(
                text = state.text,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = MAX_LINES,
                overflow = TextOverflow.Ellipsis,
            )
        }

        if (state.mediaCount > 0) {
            MediaPlaceholder(
                postType = state.postType,
                count = state.mediaCount,
                aspectRatio = state.mediaAspectRatio,
            )
        }

        PostActionBar(
            state = state.actions,
            onReact = onReact,
            onComment = onComment,
            onRepost = onRepost,
            onBookmark = onBookmark,
        )

        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}

/**
 * Stands in for media whose bytes are not on screen yet.
 *
 * The feed now carries authorized delivery URLs and real dimensions, so this
 * is no longer a "we cannot resolve this" placeholder — it is the pre-decode
 * frame. It reserves the item's TRUE aspect ratio so the row does not resize
 * when the image or first video frame arrives, which is the single largest
 * source of feed layout shift.
 *
 * Falls back to 16:9 only when the server sent no dimensions.
 */
@Composable
private fun MediaPlaceholder(
    postType: String,
    count: Int,
    aspectRatio: Float,
    modifier: Modifier = Modifier,
) {
    val label = when (postType) {
        "video" -> if (count > 1) "$count videos" else "Video"
        "image" -> if (count > 1) "$count images" else "Image"
        else -> if (count > 1) "$count attachments" else "Attachment"
    }
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(aspectRatio)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .padding(UsTheme.spacing.xxl),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

private const val MAX_LINES = 8

/** Used when the server sent no dimensions for an attachment. */
const val DEFAULT_MEDIA_ASPECT = 16f / 9f

/** Preview-only: enough lines to prove the ellipsis at MAX_LINES. */
private const val LONG_TEXT_LINES = 40

// ── Previews ────────────────────────────────────────────────────────────

private val previewActions = PostActionState(
    likeCount = 128,
    commentCount = 12,
    repostCount = 3,
    hasReacted = false,
    isBookmarked = false,
)

private val previewCard = PostCardState(
    postId = "3d752833",
    authorId = "719e2958",
    authorName = "Ada Lovelace",
    text = "Notes on the Analytical Engine, and why a machine that weaves " +
        "algebraic patterns is not merely a calculator.",
    timestamp = "2h",
    postType = "text",
    mediaCount = 0,
    actions = previewActions,
)

@Composable
private fun CardHost(state: PostCardState) = UsTheme {
    Column(modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
        PostCard(state, {}, {}, {}, {}, {}, {})
    }
}

@Preview(name = "Card — text", showBackground = true)
@Composable
private fun PostCardTextPreview() = CardHost(previewCard)

@Preview(name = "Card — video", showBackground = true)
@Composable
private fun PostCardVideoPreview() = CardHost(
    previewCard.copy(
        postType = "video",
        mediaCount = 1,
        text = "Landscape PostTube — approved contract fixture",
    ),
)

@Preview(name = "Card — pinned and engaged", showBackground = true)
@Composable
private fun PostCardPinnedPreview() = CardHost(
    previewCard.copy(
        isPinned = true,
        actions = previewActions.copy(hasReacted = true, isBookmarked = true),
    ),
)

@Preview(name = "Card — no text, media only", showBackground = true)
@Composable
private fun PostCardMediaOnlyPreview() = CardHost(
    previewCard.copy(text = "", postType = "image", mediaCount = 3),
)

@Preview(name = "Card — long text truncates", showBackground = true)
@Composable
private fun PostCardLongTextPreview() = CardHost(
    previewCard.copy(text = List(LONG_TEXT_LINES) { "Line $it of a very long post." }.joinToString(" ")),
)
