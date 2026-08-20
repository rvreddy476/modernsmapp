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
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
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
    onOptionClick: () -> Unit = {},
    onFollow: () -> Unit = {},
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color(0xFF0C0C0F))
            .clickable(onClick = onClick),
    ) {
        // 1. Media or Text background canvas
        if (state.mediaCount > 0) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(Color.Black),
                contentAlignment = Alignment.Center,
            ) {
                if (state.mediaUrl != null) {
                    AsyncImage(
                        model = state.mediaUrl,
                        contentDescription = null,
                        contentScale = ContentScale.Crop,
                        modifier = Modifier.fillMaxSize(),
                    )
                } else {
                    Box(
                        modifier = Modifier
                            .fillMaxSize()
                            .background(
                                brush = Brush.verticalGradient(
                                    listOf(
                                        Color(0xFF1E1E26),
                                        Color(0xFF121217),
                                    ),
                                ),
                            ),
                    )
                }

                // Video play badge overlay
                if (state.postType == VIDEO_POST || state.postType == "flick" || state.postType == "long_video") {
                    Box(
                        modifier = Modifier
                            .size(64.dp)
                            .clip(CircleShape)
                            .background(Color.Black.copy(alpha = 0.6f))
                            .border(HAIRLINE, Color(0x40FFFFFF), CircleShape),
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(
                            imageVector = UsIcons.Play,
                            contentDescription = "Play video",
                            tint = Color.White,
                            modifier = Modifier.size(30.dp),
                        )
                    }
                }

                // Multiple media indicator
                if (state.mediaCount > 1) {
                    Box(
                        modifier = Modifier
                            .align(Alignment.TopEnd)
                            .padding(top = 16.dp, end = 16.dp)
                            .clip(RoundedCornerShape(UsTheme.radii.full))
                            .background(Color.Black.copy(alpha = 0.65f))
                            .border(HAIRLINE, Color(0x33FFFFFF), RoundedCornerShape(UsTheme.radii.full))
                            .padding(horizontal = 12.dp, vertical = 6.dp),
                    ) {
                        Text(
                            text = "1/${state.mediaCount}",
                            style = MaterialTheme.typography.labelSmall,
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                        )
                    }
                }
            }
        } else {
            // Text-only thought / status canvas
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(
                        brush = Brush.verticalGradient(
                            listOf(
                                Color(0xFF1C1C24),
                                Color(0xFF121217),
                                Color(0xFF09090C),
                            ),
                        ),
                    )
                    .padding(horizontal = 28.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = state.text,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = Color.White,
                    textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                    modifier = Modifier.padding(bottom = 120.dp),
                )
            }
        }

        // 2. Bottom-anchored Creator Info, Follow Button, Caption & Social Actions
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .background(
                    brush = Brush.verticalGradient(
                        listOf(
                            Color.Transparent,
                            Color.Black.copy(alpha = 0.75f),
                            Color.Black.copy(alpha = 0.96f),
                        ),
                    ),
                )
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(top = 48.dp, bottom = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            // Bottom Creator Row (Profile Icon, Name, Timestamp, Follow Button)
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                    modifier = Modifier
                        .weight(1f)
                        .clip(RoundedCornerShape(UsTheme.radii.medium))
                        .clickable(onClick = onAuthorClick),
                ) {
                    Box(
                        modifier = Modifier
                            .clip(CircleShape)
                            .border(
                                width = HAIRLINE,
                                color = Color(0x66FFFFFF),
                                shape = CircleShape,
                            ),
                    ) {
                        UsAvatar(
                            name = state.authorName,
                            size = UsAvatarSize.Medium,
                            seed = state.authorId,
                        )
                    }
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            text = state.authorName,
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.Bold,
                            color = Color.White,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.semantics { heading() },
                        )
                        if (state.timestamp.isNotBlank()) {
                            Text(
                                text = state.timestamp,
                                style = MaterialTheme.typography.bodySmall,
                                color = Color.White.copy(alpha = 0.7f),
                            )
                        }
                    }
                }

                // Follow Button
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(Color.White)
                        .clickable(onClick = onFollow)
                        .padding(horizontal = 14.dp, vertical = 6.dp),
                ) {
                    Text(
                        text = "Follow",
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = FontWeight.Bold,
                        color = Color.Black,
                    )
                }

                // Pinned badge
                if (state.isPinned) {
                    Box(
                        modifier = Modifier
                            .clip(RoundedCornerShape(UsTheme.radii.full))
                            .background(UsTheme.extended.statusWarning.copy(alpha = 0.2f))
                            .border(
                                width = HAIRLINE,
                                color = UsTheme.extended.statusWarning,
                                shape = RoundedCornerShape(UsTheme.radii.full),
                            )
                            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
                    ) {
                        Text(
                            text = "Pinned",
                            style = MaterialTheme.typography.labelSmall,
                            fontWeight = FontWeight.Bold,
                            color = UsTheme.extended.statusWarning,
                        )
                    }
                }

                IconButton(
                    onClick = onOptionClick,
                    modifier = Modifier.size(28.dp),
                ) {
                    Icon(
                        imageVector = UsIcons.More,
                        contentDescription = "Post options",
                        tint = Color.White.copy(alpha = 0.8f),
                        modifier = Modifier.size(18.dp),
                    )
                }
            }

            // Caption / Description for media posts
            if (state.mediaCount > 0 && state.text.isNotBlank()) {
                Text(
                    text = state.text,
                    style = MaterialTheme.typography.bodyMedium,
                    color = Color.White,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                )
            }

            // Social Action Bar (Like, Comment, Repost, Share, Bookmark)
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
}


/**
 * The attachment on a post detail screen.
 */
@Composable
fun PostMedia(
    url: String?,
    postType: String,
    count: Int,
    aspectRatio: Float,
    modifier: Modifier = Modifier,
) {
    val mediaShape = RoundedCornerShape(14.dp)
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(aspectRatio)
            .clip(mediaShape)
            .background(UsTheme.extended.bgCardHover)
            .border(
                width = HAIRLINE,
                color = Color(0x14FFFFFF),
                shape = mediaShape,
            ),
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }

        if (postType == VIDEO_POST || postType == "flick" || postType == "long_video") {
            Box(
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(PLAY_BADGE)
                    .clip(CircleShape)
                    .background(Color.Black.copy(alpha = PLAY_BADGE_ALPHA))
                    .border(HAIRLINE, Color(0x33FFFFFF), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = UsIcons.Play,
                    contentDescription = "Play video",
                    tint = Color.White,
                    modifier = Modifier.size(PLAY_GLYPH),
                )
            }
        }

        if (count > 1) {
            Box(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.l)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(Color.Black.copy(alpha = COUNT_PILL_ALPHA))
                    .border(HAIRLINE, Color(0x33FFFFFF), RoundedCornerShape(UsTheme.radii.full))
                    .padding(
                        horizontal = UsTheme.spacing.m,
                        vertical = UsTheme.spacing.xs,
                    ),
            ) {
                Text(
                    text = "1/$count",
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Medium,
                    color = Color.White,
                )
            }
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




