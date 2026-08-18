// MatchingDeclarationName: this file's primary export is the PostCard
// composable; PostCardState is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
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
     * Where to fetch the first attachment.
     *
     * Null while an asset is still processing — the server sends no delivery
     * URL until it is ready — so the box reserves its space and stays empty
     * rather than the row collapsing and reflowing when it arrives.
     */
    val mediaUrl: String? = null,
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
/**
 * Suppressed rather than bundled into a callbacks object: a data class of
 * lambdas gets a new identity on every recomposition, which would make every
 * visible row in the feed recompose. Flat parameters keep each callback
 * individually stable.
 */
@Suppress("LongParameterList")
@Composable
fun PostCard(
    state: PostCardState,
    onClick: () -> Unit,
    onAuthorClick: () -> Unit,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            // Clip BEFORE the background so the surface, its border and the
            // ripple all stop at the same rounded edge. Clipping afterwards
            // leaves a square ripple escaping the corners on every tap.
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgCard)
            .border(
                width = HAIRLINE,
                color = UsTheme.extended.borderSubtle,
                shape = RoundedCornerShape(UsTheme.radii.large),
            )
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        CardHeader(state = state, onAuthorClick = onAuthorClick)

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
            PostMedia(
                url = state.mediaUrl,
                postType = state.postType,
                count = state.mediaCount,
                aspectRatio = state.mediaAspectRatio,
            )
        }

        // No divider. The card's own edge is the separator now; a rule inside
        // a bounded surface just cuts the card in half.
        PostActionBar(
            state = state.actions,
            onReact = onReact,
            onComment = onComment,
            onRepost = onRepost,
            onBookmark = onBookmark,
            onShare = onShare,
        )
    }
}

/**
 * The attachment on a feed row.
 *
 * The box is sized from the server's REAL width and height before any bytes
 * arrive, so the row never resizes when the image lands. Reflow mid-scroll is
 * the largest source of feed jank and it throws away the reader's place in the
 * list. Falls back to 16:9 only when the server sent no dimensions.
 */
@Composable
fun PostMedia(
    url: String?,
    postType: String,
    count: Int,
    aspectRatio: Float,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(aspectRatio)
            .clip(RoundedCornerShape(UsTheme.radii.large))
            // Painted under the image so the reserved box is a deliberate
            // surface while bytes are in flight, rather than a hole in the
            // list that reads as a failed row.
            .background(UsTheme.extended.bgCardHover),
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                // Crop, not Fit. The box is already the asset's true ratio, so
                // crop is a no-op for a correct image and quietly absorbs the
                // rounding error when it is not — Fit would letterbox that
                // error into visible bars.
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }

        // Video still plays only in reels; the feed shows its poster frame.
        // The mark is what tells a reader this is a video rather than a photo
        // that happens to be still.
        if (postType == VIDEO_POST) {
            Box(
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(PLAY_BADGE)
                    .background(Color.Black.copy(alpha = PLAY_BADGE_ALPHA), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = UsIcons.Play,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(PLAY_GLYPH),
                )
            }
        }

        // Only when there is more than one. A "1" on every single-image post
        // is noise that makes the count meaningless where it matters.
        if (count > 1) {
            Text(
                text = "1/$count",
                style = MaterialTheme.typography.labelSmall,
                color = Color.White,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.l)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(Color.Black.copy(alpha = COUNT_PILL_ALPHA))
                    .padding(
                        horizontal = UsTheme.spacing.m,
                        vertical = UsTheme.spacing.xs,
                    ),
            )
        }
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
        PostCard(state, {}, {}, {}, {}, {}, {}, {})
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

const val VIDEO_POST = "video"

private val PLAY_BADGE = 56.dp
private val PLAY_GLYPH = 26.dp
private const val PLAY_BADGE_ALPHA = 0.55f
private const val COUNT_PILL_ALPHA = 0.55f

/**
 * One physical pixel at common densities, not a design token.
 *
 * The border is meant to define the card's edge against a near-black
 * background, not to be seen as a line. Anything thicker reads as a frame.
 */
private val HAIRLINE = 1.dp

/**
 * Avatar, name and age — the card's title.
 *
 * Kept INSIDE the card surface, which is the whole point of the card: a header
 * floating on a shared background reads as a section heading over a stream,
 * not as something a person posted.
 */
@Composable
private fun CardHeader(state: PostCardState, onAuthorClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(onClick = onAuthorClick),
    ) {
        UsAvatar(
            name = state.authorName,
            size = UsAvatarSize.Small,
            seed = state.authorId,
        )
        // weight(1f) so a long display name ellipsises instead of shoving the
        // pinned marker off the card.
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = state.authorName,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.semantics { heading() },
            )
            // Omitted rather than shown blank: the formatter returns an empty
            // string for a timestamp it cannot parse, and an empty line under
            // the name looks like a layout fault.
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
}
