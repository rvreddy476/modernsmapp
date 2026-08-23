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
import androidx.compose.material3.HorizontalDivider
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
 * Everything a post component renders.
 *
 * A view-model-free value type: the feed builds it from a `FeedItem`, search
 * from a search hit, and a profile grid from an author's posts.
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
     * Where to fetch the first attachment. Null while still processing.
     */
    val mediaUrl: String? = null,
    /**
     * Aspect ratio of the first attachment. Defaults to 16:9 when unknown.
     */
    val mediaAspectRatio: Float = DEFAULT_MEDIA_ASPECT,
    /**
     * What a screen reader announces for the attachment, or null — Slice C,
     * C-CLB-3.
     *
     * Carried on the card state rather than derived here so every surface that
     * builds a card (feed, search, profile) resolves it from the same
     * `contentDescription` rule on the domain model.
     */
    val mediaContentDescription: String? = null,
    val actions: PostActionState,
    val isPinned: Boolean = false,
)

/**
 * Reusable compact post card for standard list feeds, search results, and
 * profile pages.
 */
// A Compose layout reads as one declaration; splitting it into helpers to
// satisfy a line budget spreads one visual structure across several functions
// and makes it harder to see, not easier. The parameter count is flat by
// design: bundling the callbacks into a data class would give them a new
// identity on every recomposition and recompose every visible row.
@Suppress("LongParameterList", "LongMethod")
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
    /**
     * Null hides the control.
     *
     * It previously defaulted to an empty lambda and rendered unconditionally,
     * so every card carried an overflow button that did nothing on every
     * surface that had no menu to show. Nullable makes "no action" impossible
     * to render by accident.
     */
    onOptionClick: (() -> Unit)? = null,
    onFollow: (() -> Unit)? = null,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        // Author header row
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier
                    .weight(1f, fill = false)
                    .clickable(onClick = onAuthorClick),
            ) {
                UsAvatar(
                    name = state.authorName,
                    size = UsAvatarSize.Medium,
                )
                Column {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                    ) {
                        Text(
                            text = state.authorName,
                            style = MaterialTheme.typography.titleSmall,
                            fontWeight = FontWeight.SemiBold,
                            color = UsTheme.extended.textPrimary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        if (state.isPinned) {
                            Text(
                                text = "• Pinned",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.primary,
                                fontWeight = FontWeight.Medium,
                            )
                        }
                    }
                    Text(
                        text = state.timestamp,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                    )
                }
            }

            Row(verticalAlignment = Alignment.CenterVertically) {
                if (onFollow != null) {
                    Box(
                        modifier = Modifier
                            .clip(RoundedCornerShape(UsTheme.radii.full))
                            .background(Color.White)
                            .clickable(onClick = onFollow)
                            .padding(horizontal = UsTheme.spacing.l, vertical = 6.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            text = "Follow",
                            style = MaterialTheme.typography.labelMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = Color.Black,
                        )
                    }
                }
                if (onOptionClick != null) {
                    IconButton(onClick = onOptionClick) {
                        Icon(
                            imageVector = UsIcons.More,
                            contentDescription = "Post options",
                            tint = UsTheme.extended.textSecondary,
                        )
                    }
                }
            }
        }

        // Post body text
        if (state.text.isNotBlank()) {
            Text(
                text = state.text,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = MAX_LINES,
                overflow = TextOverflow.Ellipsis,
            )
        }

        // Media attachment
        if (state.mediaCount > 0) {
            PostMedia(
                url = state.mediaUrl,
                postType = state.postType,
                count = state.mediaCount,
                aspectRatio = state.mediaAspectRatio,
                contentDescription = state.mediaContentDescription,
            )
        }

        // Social action bar
        PostActionBar(
            state = state.actions,
            onReact = onReact,
            onComment = onComment,
            onRepost = onRepost,
            onBookmark = onBookmark,
            onShare = onShare,
            modifier = Modifier.fillMaxWidth(),
        )

        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}

/**
 * Full-screen immersive post page used by VerticalPager for immersive media/video feeds.
 */
// See PostCard above for why this is one long composable rather than several.
// MagicNumber covers the inline alpha and gradient stops this presentation was
// written with; they are one-off visual constants, not shared tokens.
@Suppress("LongParameterList", "LongMethod", "MagicNumber")
@Composable
fun ImmersivePostPage(
    state: PostCardState,
    onClick: () -> Unit,
    onAuthorClick: () -> Unit,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
    modifier: Modifier = Modifier,
    /**
     * Null hides the control.
     *
     * It previously defaulted to an empty lambda and rendered unconditionally,
     * so every card carried an overflow button that did nothing on every
     * surface that had no menu to show. Nullable makes "no action" impossible
     * to render by accident.
     */
    onOptionClick: (() -> Unit)? = null,
    onFollow: (() -> Unit)? = null,
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
                        // Slice C / C-CLB-3. The immersive card is a second
                        // renderer of the same image and needs the same
                        // description; leaving it null here would make the
                        // full-screen feed the one surface where the photo is
                        // still unlabelled.
                        contentDescription = state.mediaContentDescription,
                        contentScale = ContentScale.Crop,
                        modifier = Modifier.fillMaxSize(),
                    )
                } else {
                    Box(
                        modifier = Modifier
                            .fillMaxSize()
                            .background(
                                brush = Brush.verticalGradient(
                                    colors = listOf(Color(0xFF1E1E28), Color(0xFF0E0E14)),
                                ),
                            ),
                    )
                }

                if (state.postType == VIDEO_POST || state.postType == "flick" || state.postType == "long_video") {
                    Box(
                        modifier = Modifier
                            .size(64.dp)
                            .clip(CircleShape)
                            .background(Color.Black.copy(alpha = 0.5f))
                            .border(HAIRLINE, Color(0x40FFFFFF), CircleShape),
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(
                            imageVector = UsIcons.Play,
                            contentDescription = "Play video",
                            tint = Color.White,
                            modifier = Modifier.size(32.dp),
                        )
                    }
                }

                if (state.mediaCount > 1) {
                    Box(
                        modifier = Modifier
                            .align(Alignment.TopEnd)
                            .padding(top = 72.dp, end = UsTheme.spacing.l)
                            .clip(RoundedCornerShape(UsTheme.radii.full))
                            .background(Color.Black.copy(alpha = COUNT_PILL_ALPHA))
                            .border(HAIRLINE, Color(0x33FFFFFF), RoundedCornerShape(UsTheme.radii.full))
                            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
                    ) {
                        Text(
                            text = "1/${state.mediaCount}",
                            style = MaterialTheme.typography.labelSmall,
                            fontWeight = FontWeight.Medium,
                            color = Color.White,
                        )
                    }
                }
            }
        } else {
            // Text-only thought canvas
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(
                        brush = Brush.verticalGradient(
                            colors = listOf(Color(0xFF14141E), Color(0xFF0A0A10)),
                        ),
                    )
                    .padding(horizontal = UsTheme.spacing.xxl, vertical = 96.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = state.text,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                    modifier = Modifier.semantics { heading() },
                )
            }
        }

        // 2. Bottom Scrim Gradient Overlay
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .background(
                    brush = Brush.verticalGradient(
                        colors = listOf(
                            Color.Transparent,
                            Color.Black.copy(alpha = 0.45f),
                            Color.Black.copy(alpha = 0.90f),
                        ),
                    ),
                )
                .padding(
                    start = UsTheme.spacing.l,
                    end = UsTheme.spacing.l,
                    bottom = UsTheme.spacing.l,
                    top = UsTheme.spacing.xxxl,
                ),
        ) {
            Column(
                modifier = Modifier.fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                // Creator Profile Row (Avatar + Name + Timestamp + Follow Button)
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                        modifier = Modifier
                            .weight(1f, fill = false)
                            .clickable(onClick = onAuthorClick),
                    ) {
                        UsAvatar(
                            name = state.authorName,
                            size = UsAvatarSize.Medium,
                            modifier = Modifier.border(1.dp, Color(0x33FFFFFF), CircleShape),
                        )
                        Column {
                            Row(
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                            ) {
                                Text(
                                    text = state.authorName,
                                    style = MaterialTheme.typography.titleMedium,
                                    fontWeight = FontWeight.Bold,
                                    color = Color.White,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                                if (state.isPinned) {
                                    Text(
                                        text = "• Pinned",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.primary,
                                        fontWeight = FontWeight.Medium,
                                    )
                                }
                            }
                            Text(
                                text = state.timestamp,
                                style = MaterialTheme.typography.bodySmall,
                                color = Color.White.copy(alpha = 0.7f),
                            )
                        }
                    }

                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                    ) {
                        if (onFollow != null) {
                            Box(
                                modifier = Modifier
                                    .clip(RoundedCornerShape(UsTheme.radii.full))
                                    .background(Color.White)
                                    .clickable(onClick = onFollow)
                                    .padding(horizontal = UsTheme.spacing.l, vertical = 6.dp),
                                contentAlignment = Alignment.Center,
                            ) {
                                Text(
                                    text = "Follow",
                                    style = MaterialTheme.typography.labelMedium,
                                    fontWeight = FontWeight.SemiBold,
                                    color = Color.Black,
                                )
                            }
                        }

                        if (onOptionClick != null) {
                            IconButton(onClick = onOptionClick) {
                                Icon(
                                    imageVector = UsIcons.More,
                                    contentDescription = "More options",
                                    tint = Color.White.copy(alpha = 0.8f),
                                )
                            }
                        }
                    }
                }

                // Post description / caption (if media present)
                if (state.mediaCount > 0 && state.text.isNotBlank()) {
                    Text(
                        text = state.text,
                        style = MaterialTheme.typography.bodyMedium,
                        color = Color.White,
                        maxLines = 3,
                        overflow = TextOverflow.Ellipsis,
                    )
                }

                // Social Action Bar
                PostActionBar(
                    state = state.actions,
                    onReact = onReact,
                    onComment = onComment,
                    onRepost = onRepost,
                    onBookmark = onBookmark,
                    onShare = onShare,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

/**
 * The media attachment for a post card: image or video poster frame, with a
 * play badge for video and a count pill for carousels.
 */
// The inline ARGB values below are one-off surface colours from the current
// visual design, not shared tokens. Promoting them to the design system is a
// deliberate design decision, recorded as backlog rather than made here.
@Suppress("MagicNumber")
@Composable
fun PostMedia(
    url: String?,
    postType: String,
    count: Int,
    aspectRatio: Float,
    modifier: Modifier = Modifier,
    /**
     * What a screen reader announces for this image, or null for silence —
     * Slice C, C-CLB-3.
     *
     * Null is correct for exactly two cases: a photo the author deliberately
     * marked decorative, and one from before descriptions were required. It is
     * NOT the default because a described photo passing through here silently
     * unlabelled is the defect this parameter exists to make impossible — the
     * composer demands a description and this was throwing it away.
     *
     * Callers derive it from `PostMediaRef.contentDescription` or
     * `FeedMedia.contentDescription` rather than re-deciding the rule here.
     */
    contentDescription: String?,
) {
    val mediaShape = RoundedCornerShape(UsTheme.radii.medium)

    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(aspectRatio)
            .clip(mediaShape)
            .background(Color(0xFF14141A))
            .border(
                width = HAIRLINE,
                color = Color(0x14FFFFFF),
                shape = mediaShape,
            ),
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                // The author's description, or null when they marked the photo
                // decorative. Coil forwards this straight to the semantics
                // node, so a described image is announced and a decorative one
                // stays silent — which is the whole point of the distinction.
                contentDescription = contentDescription,
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
const val DEFAULT_MEDIA_ASPECT = 16f / 9f

const val VIDEO_POST = "video"
private val HAIRLINE = 0.5.dp
private val PLAY_BADGE = 48.dp
private val PLAY_GLYPH = 24.dp
private const val PLAY_BADGE_ALPHA = 0.55f
private const val COUNT_PILL_ALPHA = 0.6f

// ── Previews ────────────────────────────────────────────────────────────

private val previewActions = PostActionState(
    likeCount = 128,
    commentCount = 12,
    repostCount = 3,
    hasReacted = false,
    isBookmarked = false,
)

@Preview(showBackground = true)
@Composable
private fun PostCardPreview() {
    UsTheme {
        PostCard(
            state = PostCardState(
                postId = "post-1",
                authorId = "author-1",
                authorName = "Jane Doe",
                text = "Exploring new possibilities with Compose & Kotlin!",
                timestamp = "2h",
                postType = "text",
                mediaCount = 0,
                actions = previewActions,
            ),
            onClick = {},
            onAuthorClick = {},
            onReact = {},
            onComment = {},
            onRepost = {},
            onBookmark = {},
            onShare = {},
        )
    }
}
