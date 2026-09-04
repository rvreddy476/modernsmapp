// MatchingDeclarationName: this file's primary export is the PostCard
// composable; PostCardState is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
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
    /** "@handle", shown under the name when the surface has one; else the time goes there. */
    val authorHandle: String? = null,
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
    /**
     * Every attachment, in the author's order — Creator Studio P0-A.
     *
     * Empty means "this surface has not adopted carousels yet", and the card
     * falls back to the single-attachment fields above. Search and profile
     * cards still do that; the feed supplies real pages.
     *
     * Each page carries ITS OWN description, because the same photo cropped two
     * ways across two pages can honestly need two different ones. Announcing
     * page 1's text for page 3 would be confidently wrong, which is worse for a
     * screen-reader user than saying nothing.
     */
    val mediaPages: List<PostCardMediaPage> = emptyList(),
    val actions: PostActionState,
    val isPinned: Boolean = false,
    /** Present exactly when the post is a poll. */
    val poll: PostCardPoll? = null,
) {
    /**
     * What the Instagram header and caption print: the username when the
     * surface has one, the display name otherwise. Instagram leads with the
     * handle; an account without one still needs a name on its post.
     */
    val username: String
        get() = authorHandle?.removePrefix("@")?.takeIf { it.isNotBlank() } ?: authorName
}

/**
 * A poll ready to render: server-computed counts and percentages, plus the
 * viewer's own votes (with any this-session tap already layered in by the
 * surface that built this state).
 */
data class PostCardPoll(
    val options: List<PostCardPollOption>,
    val totalVotes: Long,
    val votedOptionIds: Set<String>,
    val hasEnded: Boolean,
) {
    val showResults: Boolean get() = hasEnded || votedOptionIds.isNotEmpty()
}

data class PostCardPollOption(
    val id: String,
    val label: String,
    val voteCount: Long,
    /** 0..100, computed by the server. */
    val percentage: Double,
)

/**
 * One page of a post's carousel, already resolved to a URL.
 *
 * The card is handed resolved URLs rather than media ids because resolution
 * needs the variant ladder and the viewer's signed delivery, neither of which
 * belongs in a component that also renders search results.
 */
data class PostCardMediaPage(
    val mediaId: String,
    val url: String?,
    val aspectRatio: Float = DEFAULT_MEDIA_ASPECT,
    /** This page's own description, or null for decorative/undeclared. */
    val contentDescription: String? = null,
)

/**
 * The attachment area: one image, or a swipeable ordered carousel — always
 * inside the same 4:5 frame (see [PostMediaFrame]), edge to edge.
 *
 * ## WHY THE SINGLE-IMAGE BRANCH STAYS
 *
 * Not every surface has adopted carousels. Search and profile cards still pass
 * only the single-attachment fields, and this renders those exactly as before.
 * Deleting that branch would have meant rewriting three unrelated surfaces
 * inside a slice whose scope is the feed and post detail.
 */
@Composable
private fun PostMediaCarousel(state: PostCardState, onClick: () -> Unit) {
    val pages = state.mediaPages
    if (pages.size <= 1) {
        val single = pages.firstOrNull()
        PostMedia(
            url = single?.url ?: state.mediaUrl,
            postType = state.postType,
            count = state.mediaCount,
            contentDescription = single?.contentDescription ?: state.mediaContentDescription,
            modifier = Modifier.clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            ),
        )
        return
    }

    val pagerState = rememberPagerState(pageCount = { pages.size })
    Box(modifier = Modifier.fillMaxWidth()) {
        HorizontalPager(state = pagerState, modifier = Modifier.fillMaxWidth()) { index ->
            val page = pages[index]
            PostMedia(
                url = page.url,
                postType = state.postType,
                // 1, not mediaCount: PostMedia draws an "N" badge, and at
                // mediaCount it would stamp "3" onto every page of the very
                // carousel it is meant to be counting.
                count = 1,
                contentDescription = page.contentDescription,
                modifier = Modifier.clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onClick,
                ),
            )
        }
        // "2/5", top-right, and the position pips over the bottom edge —
        // both INSIDE the frame, the way Instagram overlays them, so the
        // frame stays exactly 4:5 with or without a carousel.
        MediaCountPill(
            text = "${pagerState.currentPage + 1}/${pages.size}",
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(UsTheme.spacing.l),
        )
        CarouselPips(
            pageCount = pages.size,
            currentPage = pagerState.currentPage,
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .padding(bottom = UsTheme.spacing.l),
        )
    }
}

/**
 * Position pips.
 *
 * Semantics cleared on purpose: a screen-reader user swipes the pager and hears
 * each page's own description, so announcing "dot dot dot" adds nothing and
 * interrupts the part that does.
 */
@Composable
private fun CarouselPips(pageCount: Int, currentPage: Int, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier.clearAndSetSemantics { },
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(pageCount) { index ->
            // White over the photo, dimmed when not current: the pips sit on
            // the media now, and a themed colour can vanish into any frame.
            Box(
                modifier = Modifier
                    .size(if (index == currentPage) PIP_SELECTED else PIP_UNSELECTED)
                    .clip(CircleShape)
                    .background(Color.White.copy(alpha = if (index == currentPage) 1f else PIP_DIM_ALPHA)),
            )
        }
    }
}

private val PIP_SELECTED = 7.dp
private val PIP_UNSELECTED = 5.dp
private const val PIP_DIM_ALPHA = 0.35f

/**
 * The post, laid out exactly as Instagram lays out a feed post, on
 * Momentum's palette (founder, 2026-09-04).
 *
 * No contained card: no gutters, no border, no shadow, no corner radius. The
 * header row at 16dp side padding; the media EDGE TO EDGE in a uniform 4:5
 * frame; the action row; then the caption stack. A text-only post is the
 * same header, the text at 15sp with 16dp padding, then the same action row
 * — its height follows the text. This is THE post component: Home, Friends,
 * a hashtag's posts and the in-place viewer all render it.
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
    /** The media was tapped. A text-only post has nothing to open and never fires it. */
    onClick: () -> Unit,
    onAuthorClick: () -> Unit,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
    modifier: Modifier = Modifier,
    /**
     * The ⋮ "More" glyph at the header's right end. Null hides it.
     *
     * It previously defaulted to an empty lambda and rendered unconditionally,
     * so every card carried an overflow button that did nothing on every
     * surface that had no menu to show. Nullable makes "no action" impossible
     * to render by accident.
     */
    onMore: (() -> Unit)? = null,
    /**
     * "· Follow" beside the name. Null hides it — the host passes it only
     * when the viewer is KNOWN not to follow the author and the post is not
     * their own.
     */
    onFollow: (() -> Unit)? = null,
    /** Casts a vote in this card's poll. Null on surfaces that cannot vote. */
    onVotePoll: ((optionId: String) -> Unit)? = null,
    /**
     * Something to draw in the 4:5 frame INSTEAD of the poster — the feed
     * puts the playing video of its most visible video card there, and the
     * in-place viewer does the same on a video page. Null renders the media
     * as usual. The override owns its own tap: the poster's [onClick] is not
     * wired under it.
     */
    mediaOverride: (@Composable () -> Unit)? = null,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .testTag("post_card"),
    ) {
        PostCardHeader(state, onAuthorClick, onFollow, onMore)

        // Poll block — ballots until the viewer votes (or the poll ends),
        // results after.
        state.poll?.let { poll ->
            Box(modifier = Modifier.padding(horizontal = SIDE_PADDING, vertical = UsTheme.spacing.m)) {
                PostPollBlock(poll = poll, onVote = onVotePoll)
            }
        }

        if (state.mediaCount > 0) {
            // The media is the post's centre of gravity: directly under the
            // header, edge to edge, and the caption below reads as
            // commentary on it.
            if (mediaOverride != null) {
                PostMediaFrame(modifier = Modifier.testTag("post_media_frame")) { mediaOverride() }
            } else {
                PostMediaCarousel(state, onClick)
            }
        } else if (state.text.isNotBlank()) {
            // Text-only: the text IS the body, at 15sp with the page gutter.
            Text(
                text = state.text,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = SIDE_PADDING, vertical = UsTheme.spacing.m)
                    .testTag("post_text_body"),
            )
        }

        // Like, comment, repost, share, save at equal distance across the
        // row (founder, 2026-09-04). Glyphs only — the counts are written
        // out as lines in the caption stack.
        PostActionBar(
            state = state.actions,
            onReact = onReact,
            onComment = onComment,
            onRepost = onRepost,
            onBookmark = onBookmark,
            onShare = onShare,
            showCounts = false,
            showRepost = true,
            evenSpacing = true,
            glyphSize = ACTION_GLYPH,
            spacing = UsTheme.spacing.xxl,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = SIDE_PADDING),
        )

        PostCardCaption(state, onComment, captionShown = state.mediaCount > 0)
    }
}

/**
 * The uniform media frame: full width, 4:5, edge to edge, on the canvas
 * colour while the bytes load. EVERY media post — image, carousel, reel
 * poster or playing video — sits in this one frame, cropped to fill, so the
 * feed scrolls at a steady rhythm instead of jumping between a 16:9 strip
 * and a full-height portrait.
 */
@Composable
fun PostMediaFrame(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(MEDIA_FRAME_ASPECT)
            .background(UsTheme.extended.bgCanvas),
        content = content,
    )
}

/**
 * The author row: a 32dp avatar, the username at 14sp semibold with the
 * "· Follow" text button in the accent beside it, the time under it at
 * 12sp muted, and the ⋮ at the right end. 16dp side padding.
 *
 * At least [POST_CARD_HEADER_HEIGHT] tall — see that constant for why the
 * feed needs to know where the media frame under it begins.
 */
@Composable
private fun PostCardHeader(
    state: PostCardState,
    onAuthorClick: () -> Unit,
    onFollow: (() -> Unit)?,
    onMore: (() -> Unit)?,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = POST_CARD_HEADER_HEIGHT)
            .padding(start = SIDE_PADDING, end = UsTheme.spacing.xs)
            .testTag("post_header"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        UsAvatar(
            name = state.authorName,
            seed = state.authorId,
            size = UsAvatarSize.Small,
            modifier = Modifier.clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onAuthorClick,
            ),
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(start = UsTheme.spacing.l),
        ) {
            PostCardNameRow(state, onAuthorClick)
            Text(
                text = state.timestamp,
                style = MaterialTheme.typography.bodySmall,
                fontSize = META_SIZE,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (onFollow != null) {
            // A real button at the row's right end, in the one follow colour
            // the whole app uses (founder, 2026-09-04) — the inline "· Follow"
            // text beside the name read as a typo.
            UsFollowButton(
                onClick = onFollow,
                modifier = Modifier
                    .padding(start = UsTheme.spacing.m)
                    .testTag("post_follow"),
            )
        }
        if (onMore != null) {
            IconButton(onClick = onMore, modifier = Modifier.testTag("post_more")) {
                Icon(
                    imageVector = UsIcons.More,
                    contentDescription = "More",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

/** The username, then "· Pinned" when the author pinned it. */
@Composable
private fun PostCardNameRow(state: PostCardState, onAuthorClick: () -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = state.username,
            style = MaterialTheme.typography.bodyMedium,
            fontSize = NAME_SIZE,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .weight(1f, fill = false)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onAuthorClick,
                ),
        )
        if (state.isPinned) {
            Text(
                text = "· Pinned",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(start = UsTheme.spacing.xs),
            )
        }
    }
}

/**
 * The caption stack under the action row, at the page gutter: "N likes"
 * (14sp bold), "username caption" clamped to three lines with "more",
 * "View all N comments" (muted, opens the comments), and the time in 11sp
 * uppercase muted.
 *
 * [captionShown] is false for a text-only post, whose text is already the
 * body above the action row; repeating it here would print it twice.
 *
 * Zero counts are not information: the lines appear the moment there is
 * something to count, so a fresh post is not three rows of "0".
 */
@Composable
private fun PostCardCaption(state: PostCardState, onComment: () -> Unit, captionShown: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = SIDE_PADDING)
            .padding(bottom = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        val likes = state.actions.likeCount
        if (likes > 0) {
            Text(
                text = formatCount(likes) + if (likes == 1) " like" else " likes",
                style = MaterialTheme.typography.bodyMedium,
                fontSize = NAME_SIZE,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
        }
        if (captionShown && state.text.isNotBlank()) {
            CaptionText(postId = state.postId, authorName = state.username, text = state.text)
        }
        val comments = state.actions.commentCount
        if (comments > 0) {
            Text(
                text = "View all " + formatCount(comments) + if (comments == 1) " comment" else " comments",
                style = MaterialTheme.typography.bodyMedium,
                fontSize = NAME_SIZE,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onComment,
                ),
            )
        }
        Text(
            text = state.timestamp.uppercase(),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** Instagram's page gutter: 16dp, not the 18dp Momentum page gutter. */
private val SIDE_PADDING = 16.dp

/**
 * The header row's height: the 32dp avatar with the old 10dp above and
 * below, plus the room the two text lines take at the default font scale.
 *
 * A KNOWN height rather than a content-sized one, because the feed decides
 * which video to play from the list's layout info — each row's offset and
 * size — and needs to find the 4:5 frame inside the row without measuring
 * it: the frame starts this far below the row's top. A larger font scale
 * can push the row past this (it is a minimum, so nothing clips), and the
 * frame estimate is then out by a few dp on a frame several hundred tall —
 * well inside the 60% rule's tolerance.
 */
val POST_CARD_HEADER_HEIGHT = 56.dp
private val NAME_SIZE = 14.sp
private val META_SIZE = 12.sp

/** 24dp glyphs on the Instagram card; post detail and reels keep the 20dp default. */
private val ACTION_GLYPH = 24.dp

/** Every media post's frame: Instagram's portrait limit, applied uniformly. */
const val MEDIA_FRAME_ASPECT = 4f / 5f

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
 * The media attachment for a post card: image or video poster frame in the
 * uniform 4:5 [PostMediaFrame], cropped to fill, with a count pill for
 * carousels. A video's poster carries no label: the frame plays when the
 * feed decides it should ([mediaOverride] on [PostCard]), and is a plain
 * still otherwise.
 */
@Composable
fun PostMedia(
    url: String?,
    // Kept so the card's contract does not change under three surfaces; the
    // poster no longer draws anything from it.
    @Suppress("UNUSED_PARAMETER") postType: String,
    count: Int,
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
    PostMediaFrame(modifier = modifier.testTag("post_media_frame")) {
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

        // No "Reel" label and no play glyph over a video's poster (founder,
        // 2026-09-05): the feed PLAYS the video in this frame when the card
        // is the most visible one, so the frame itself says what it is.
        // A label would sit on top of a moving picture, and a play button
        // would promise a tap does something it does not — a tap opens
        // Reels.

        if (count > 1) {
            MediaCountPill(
                text = "1/$count",
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.l),
            )
        }
    }
}

/** "2/5" on a dark plate over the media's top-right corner. */
@Suppress("MagicNumber") // The plate's hairline is a one-off ARGB literal, not a token.
@Composable
private fun MediaCountPill(text: String, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(Color.Black.copy(alpha = COUNT_PILL_ALPHA))
            .border(HAIRLINE, Color(0x33FFFFFF), RoundedCornerShape(UsTheme.radii.full))
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Medium,
            color = Color.White,
        )
    }
}

/**
 * The poll body: ballots until the viewer votes (or the poll ends), then
 * results with the server's percentages as filled bars.
 *
 * One block, two honest states — an option row is a BUTTON only while a vote
 * can still change something. After that it is a result, and rendering it as
 * anything tappable would be a lie the server corrects with an error.
 */
@Composable
private fun PostPollBlock(poll: PostCardPoll, onVote: ((String) -> Unit)?) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        poll.options.forEach { option ->
            if (poll.showResults) {
                PollResultRow(option = option, chosen = option.id in poll.votedOptionIds)
            } else {
                PollBallotRow(option = option, onVote = onVote)
            }
        }
        Text(
            text = buildString {
                append(poll.totalVotes)
                append(if (poll.totalVotes == 1L) " vote" else " votes")
                if (poll.hasEnded) append(" · Final results")
            },
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun PollBallotRow(option: PostCardPollOption, onVote: ((String) -> Unit)?) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .border(
                HAIRLINE_FULL,
                MaterialTheme.colorScheme.primary,
                RoundedCornerShape(UsTheme.radii.full),
            )
            .then(
                if (onVote != null) {
                    Modifier.clickable { onVote(option.id) }
                } else {
                    Modifier
                },
            )
            .padding(vertical = UsTheme.spacing.m, horizontal = UsTheme.spacing.l),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = option.label,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.primary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun PollResultRow(option: PostCardPollOption, chosen: Boolean) {
    val fraction = (option.percentage / PERCENT_D).toFloat().coerceIn(0f, 1f)
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCard),
    ) {
        // The bar IS the datum: width = the server's percentage.
        Box(modifier = Modifier.matchParentSize()) {
            Box(
                modifier = Modifier
                    .fillMaxHeight()
                    .fillMaxWidth(fraction.coerceAtLeast(MIN_BAR_FRACTION))
                    .background(
                        if (chosen) {
                            MaterialTheme.colorScheme.primary.copy(alpha = RESULT_BAR_ALPHA)
                        } else {
                            UsTheme.extended.borderSubtle
                        },
                    ),
            )
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = UsTheme.spacing.m, horizontal = UsTheme.spacing.l),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = option.label + if (chosen) "  ✓" else "",
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = if (chosen) FontWeight.Bold else FontWeight.Normal,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = "${option.percentage.toInt()}%",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textSecondary,
            )
        }
    }
}

/**
 * The caption, three lines at most until the reader asks for the rest.
 *
 * A long post used to run eight lines under its media and push the next
 * card a screen away. Now it shows [CAPTION_LINES] and Instagram's "more" link,
 * which only appears when the text actually overflowed — measured from the
 * layout, not guessed from a character count, so a three-line caption never
 * gets a link that reveals nothing. Expanded state is per post and survives
 * a rotation, but resets when the card leaves the list, which is what a
 * reader expects of a feed.
 */
@Composable
private fun CaptionText(postId: String, authorName: String, text: String) {
    var expanded by rememberSaveable(postId) { mutableStateOf(false) }
    var overflowed by remember(postId, text) { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            text = buildAnnotatedString {
                withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(authorName) }
                append(" ")
                append(text)
            },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textBody,
            maxLines = if (expanded) Int.MAX_VALUE else CAPTION_LINES,
            overflow = TextOverflow.Ellipsis,
            onTextLayout = { if (!expanded) overflowed = it.hasVisualOverflow },
        )
        if (overflowed || expanded) {
            Text(
                text = if (expanded) "less" else "more",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textMuted,
                modifier = Modifier
                    .clickable { expanded = !expanded }
                    .semantics { role = Role.Button }
                    .testTag("post-caption-toggle"),
            )
        }
    }
}

/** Lines of caption shown before "Show more". */
private const val CAPTION_LINES = 3

private const val PERCENT_D = 100.0
private const val RESULT_BAR_ALPHA = 0.35f

/** A sliver even at 0% so every option reads as a bar, not a blank row. */
private const val MIN_BAR_FRACTION = 0.02f
private val HAIRLINE_FULL = 1.dp
const val DEFAULT_MEDIA_ASPECT = 16f / 9f

const val VIDEO_POST = "video"
private val HAIRLINE = 0.5.dp
private const val COUNT_PILL_ALPHA = 0.6f

// ── Previews ────────────────────────────────────────────────────────────

private val previewActions = PostActionState(
    likeCount = 128,
    commentCount = 12,
    repostCount = 3,
    hasReacted = false,
    isBookmarked = false,
)

@Preview(name = "Post — text only", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun PostCardPreview() {
    UsTheme {
        PostCard(
            state = PostCardState(
                postId = "post-1",
                authorId = "author-1",
                authorName = "Jane Doe",
                authorHandle = "@janedoe",
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
            onFollow = {},
            onMore = {},
        )
    }
}

@Preview(name = "Post — photo, 4:5 frame", showBackground = true, backgroundColor = 0xFF041122)
@Composable
private fun PostCardPhotoPreview() {
    UsTheme {
        PostCard(
            state = PostCardState(
                postId = "post-2",
                authorId = "author-1",
                authorName = "Jane Doe",
                authorHandle = "@janedoe",
                text = "Golden hour on the ridge. Three lines of caption at most before the reader is asked, " +
                    "and the rest waits behind a small more link exactly the way the reference app does it.",
                timestamp = "2h",
                postType = "image",
                mediaCount = 1,
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
