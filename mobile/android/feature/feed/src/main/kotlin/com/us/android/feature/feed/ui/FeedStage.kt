package com.us.android.feature.feed.ui

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.media3.ui.compose.modifiers.resizeWithContentScale
import androidx.media3.ui.compose.state.rememberPresentationState
import coil3.compose.AsyncImage
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.component.usNotificationsDescription
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerPool
import com.us.android.core.media.ui.VideoLoadingIndicator
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPoll
import com.us.android.core.ui.PORTRAIT_SCREEN_ASPECT
import com.us.android.core.ui.PostCardMediaPage
import com.us.android.core.ui.PostCardPoll
import com.us.android.core.ui.PostCardPollOption
import com.us.android.core.ui.PostPollBlock
import com.us.android.core.ui.StageAuthorRow
import com.us.android.core.ui.StageBottomScrim
import com.us.android.core.ui.StageCaption
import com.us.android.core.ui.StageCaptionState
import com.us.android.core.ui.StagePageCounter
import com.us.android.core.ui.StagePageDots
import com.us.android.core.ui.StagePausedGlyph
import com.us.android.core.ui.StagePhoto
import com.us.android.core.ui.StagePhotoCarousel
import com.us.android.core.ui.StageRail
import com.us.android.core.ui.StageRailAuthor
import com.us.android.core.ui.StageRailBadge
import com.us.android.core.ui.StageRailSound
import com.us.android.core.ui.StageTextCard
import com.us.android.feature.feed.ui.reels.reelAuthorLabel
import com.us.android.feature.feed.ui.reels.reelHashtags
import com.us.android.feature.feed.ui.reels.reelTitle

/** Everything one full-screen page reads beyond its post, bundled so the pager passes one value. */
internal data class StagePageState(
    val overlay: EngagementOverlay,
    val pollVotes: Set<String>,
    /** The viewer can follow this author: the avatar wears its "+". */
    val offersFollow: Boolean,
    val pages: List<PostCardMediaPage>,
    val poster: String?,
    val playback: Playback?,
    /** The pager has settled on this page. */
    val active: Boolean,
    /** The settled page is held by a tap. */
    val paused: Boolean,
    /** How much of the top the header covers, for what sits just under it. */
    val topInset: Dp,
)

/**
 * One post, full screen (2026-10-18): the media for its kind ([stageKind]),
 * the bottom scrim, the caption block bottom-left and the shared rail
 * bottom-right. A tap on a video holds it (and plays it again); a long press
 * anywhere opens the post's More sheet.
 */
@Composable
internal fun FeedStagePage(
    item: FeedItem,
    page: Int,
    state: StagePageState,
    pool: PlayerPool,
    callbacks: FeedRowCallbacks,
    onTogglePause: () -> Unit,
    onOpenHashtag: ((String) -> Unit)?,
    onOpenSound: ((String) -> Unit)?,
    /** Full mode: only the description and its author row; no rail. */
    fullMode: Boolean = false,
    /** A double tap anywhere on the page switches full mode; null leaves double taps alone. */
    onToggleFullMode: (() -> Unit)? = null,
) {
    val kind = stageKind(item)
    val carousel = rememberPagerState(pageCount = { state.pages.size })
    val author = StageRailAuthor(
        name = item.author.nameForDisplay,
        seed = item.author.id,
        onOpen = { callbacks.onOpenAuthor(item.author.id) },
        badge = if (state.offersFollow) StageRailBadge("Follow") { callbacks.onFollow(item.author.id) } else null,
    )
    Box(
        modifier = Modifier
            .fillMaxSize()
            .clipToBounds()
            .background(UsTheme.extended.stage)
            .pointerInput(item.id, kind, onToggleFullMode) {
                // A single tap waits out the double-tap window before it
                // pauses, so a double tap is one mode change, not a pause too.
                detectTapGestures(
                    onTap = { if (kind == StageKind.VIDEO) onTogglePause() },
                    onDoubleTap = onToggleFullMode?.let { toggle -> { toggle() } },
                    onLongPress = { callbacks.onMore?.invoke(item) },
                )
            }
            .testTag("feed_page:${item.id}"),
    ) {
        when (kind) {
            StageKind.VIDEO -> if (state.playback != null) {
                val player = remember(page, state.playback) { pool.acquire(page, state.playback) }
                StageVideo(player = player, posterUrl = state.poster, aspect = state.pages.firstOrNull()?.aspectRatio)
            } else {
                // Still processing: its poster, and a word on why it does not move.
                state.pages.firstOrNull()?.let { StagePhoto(page = it.copy(url = state.poster)) }
            }
            StageKind.PHOTO -> StagePhotoCarousel(pages = state.pages, pagerState = carousel)
            StageKind.POLL -> StageTextCard(
                seed = item.id,
                text = item.text.ifBlank { item.poll?.question.orEmpty() },
                contentPadding = androidx.compose.foundation.layout.PaddingValues(top = state.topInset),
            ) {
                item.poll?.let { poll ->
                    // The ballots on the stage's dark ground, in the dark theme's colours.
                    UsTheme(darkTheme = true) {
                        PostPollBlock(
                            poll = poll.toCardPoll(state.pollVotes),
                            onVote = if (!poll.hasEnded) {
                                { optionId -> callbacks.onVotePoll(item.id, optionId) }
                            } else {
                                null
                            },
                        )
                    }
                }
            }
            StageKind.TEXT -> StageTextCard(
                seed = item.id,
                text = item.text,
                contentPadding = androidx.compose.foundation.layout.PaddingValues(top = state.topInset),
            )
        }

        if (kind == StageKind.VIDEO && state.active && state.paused) {
            StagePausedGlyph(modifier = Modifier.align(Alignment.Center))
        }
        if (kind == StageKind.PHOTO && state.pages.size > 1) {
            StagePageCounter(
                current = carousel.currentPage,
                count = state.pages.size,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(top = state.topInset + UsTheme.spacing.m, end = UsTheme.spacing.l),
            )
        }

        StageBottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
        StageCaption(
            state = stageCaption(item, kind),
            onOpenAuthor = { callbacks.onOpenAuthor(item.author.id) },
            onOpenHashtag = onOpenHashtag,
            onSound = item.sound?.let { sound -> onOpenSound?.let { open -> { open(sound.id) } } },
            above = {
                if (kind == StageKind.PHOTO && state.pages.size > 1) {
                    StagePageDots(pageCount = state.pages.size, currentPage = carousel.currentPage)
                }
            },
            author = if (fullMode) {
                { StageAuthorRow(author = author, tagPrefix = "feed_author_row") }
            } else {
                null
            },
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                // Full mode hides the bar, so the page reaches the gesture handle.
                .then(if (fullMode) Modifier.navigationBarsPadding() else Modifier)
                .padding(
                    start = UsTheme.spacing.xxl,
                    end = if (fullMode) UsTheme.spacing.xxl else RAIL_CLEARANCE,
                    bottom = UsTheme.spacing.xxl,
                ),
        )
        if (!fullMode) StageRail(
            author = author,
            controls = stageRailControls(
                item = item,
                overlay = state.overlay,
                onReact = callbacks.onReact,
                onComment = callbacks.onOpenComments,
                onBookmark = callbacks.onBookmark,
                onShare = callbacks.onShare,
            ),
            sound = item.sound?.let { sound ->
                StageRailSound(
                    name = item.author.nameForDisplay,
                    seed = item.author.id,
                    description = "Sound: ${sound.title}",
                    onClick = { onOpenSound?.invoke(sound.id) },
                )
            },
            tagPrefix = "feed_rail",
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .padding(end = UsTheme.spacing.m, bottom = UsTheme.spacing.xxl),
        )
    }
}

/**
 * A video post, full screen: cropped when it is close to the screen's shape,
 * fitted otherwise, its poster over it until the first frame. A SurfaceView
 * — the cheap path — because the crop only ever runs off the SIDES of a
 * vertical pager's page ([stageVideoScale]), never into the next page.
 */
@OptIn(UnstableApi::class)
@Composable
private fun StageVideo(player: Player, posterUrl: String?, aspect: Float?) {
    val presentation = rememberPresentationState(player)
    val scale = stageVideoScale(aspect ?: 0f)
    Box(modifier = Modifier.fillMaxSize().testTag("feed_video"), contentAlignment = Alignment.Center) {
        PlayerSurface(
            player = player,
            surfaceType = SURFACE_TYPE_SURFACE_VIEW,
            modifier = Modifier.resizeWithContentScale(scale, presentation.videoSizeDp),
        )
        if (presentation.coverSurface && posterUrl != null) {
            AsyncImage(model = posterUrl, contentDescription = null, contentScale = scale, modifier = Modifier.fillMaxSize())
        }
        VideoLoadingIndicator(player = player, onRetry = player::prepare)
    }
}

/**
 * How a video fills the stage: CROP only when the video is at least as wide
 * as the screen and not much wider — a 9:16 clip on a taller phone loses a
 * sliver at each side — FIT otherwise. A video narrower than the screen is
 * never cropped: that crop would run off the top and bottom, and a
 * SurfaceView is not clipped by its page. Pure.
 */
internal fun stageVideoScale(mediaAspect: Float, screenAspect: Float = PORTRAIT_SCREEN_ASPECT): ContentScale {
    if (mediaAspect <= 0f) return ContentScale.Fit
    val ratio = mediaAspect / screenAspect
    return if (ratio in 1f..VIDEO_CROP_HIGH) ContentScale.Crop else ContentScale.Fit
}

/** What the caption block says for a post. A text or poll page already shows its words, so the caption does not repeat them. */
internal fun stageCaption(item: FeedItem, kind: StageKind = stageKind(item)) = StageCaptionState(
    postId = item.id,
    handle = reelAuthorLabel(item.author.username, item.author.nameForDisplay),
    date = formatRelativeTime(item.createdAt).takeIf { it.isNotBlank() },
    title = reelTitle(item),
    text = if (kind == StageKind.TEXT || kind == StageKind.POLL) "" else item.text,
    hashtags = reelHashtags(item),
    labels = disclosureLabels(item.disclosure),
    // No sound line in the caption (2026-10-19): the rail's sound disc says it already.
    soundLabel = null,
)

/**
 * The server's poll with this session's votes layered over it — one-step,
 * derived, never accumulated, the same rule as engagement taps.
 */
internal fun FeedPoll.toCardPoll(localPollVotes: Set<String>): PostCardPoll {
    val localOnly = localPollVotes - viewerVotedOptionIds.toSet()
    return PostCardPoll(
        options = options.map { option ->
            PostCardPollOption(
                id = option.id,
                label = option.label,
                voteCount = option.voteCount + if (option.id in localOnly) 1 else 0,
                percentage = option.percentage,
            )
        },
        totalVotes = totalVotes + localOnly.size,
        votedOptionIds = viewerVotedOptionIds.toSet() + localPollVotes,
        hasEnded = hasEnded,
    )
}

/**
 * Home's header over the stage: the More menu at the left, the three tabs
 * in the middle with Following's dot, then Messages (founder, 2026-10-19:
 * always visible), the bell with its unread count, and Search at the right.
 * White on the top scrim, under the status bar.
 */
@Composable
internal fun HomeStageHeader(
    selected: FeedTab,
    followingDot: Boolean,
    unreadCount: Int,
    onSelect: (FeedTab) -> Unit,
    onOpenMore: () -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    onOpenSearch: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .statusBarsPadding()
            .height(HEADER_HEIGHT)
            .padding(horizontal = UsTheme.spacing.xs)
            .testTag("home_header"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onOpenMore, modifier = Modifier.testTag("home_header:more")) {
            Icon(imageVector = UsIcons.Menu, contentDescription = "More", tint = UsTheme.extended.onMedia)
        }
        Row(
            modifier = Modifier.weight(1f).selectableGroup(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterHorizontally),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FeedTab.entries.forEach { tab ->
                HeaderTab(
                    tab = tab,
                    active = tab == selected,
                    dot = tab == FeedTab.FOLLOWING && followingDot,
                    onClick = { onSelect(tab) },
                )
            }
        }
        IconButton(onClick = onOpenMessages, modifier = Modifier.testTag("home_header:messages")) {
            Icon(imageVector = UsIcons.Comment, contentDescription = "Messages", tint = UsTheme.extended.onMedia)
        }
        IconButton(
            onClick = onOpenNotifications,
            modifier = Modifier
                .testTag("home_header:notifications")
                .semantics { contentDescription = usNotificationsDescription(unreadCount) },
        ) {
            UsBadgedIcon(icon = UsIcons.Notifications, count = unreadCount, tint = UsTheme.extended.onMedia)
        }
        IconButton(onClick = onOpenSearch, modifier = Modifier.testTag("home_header:search")) {
            Icon(imageVector = UsIcons.Search, contentDescription = "Search", tint = UsTheme.extended.onMedia)
        }
    }
}

@Composable
private fun HeaderTab(tab: FeedTab, active: Boolean, dot: Boolean, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .selectable(selected = active, role = Role.Tab, onClick = onClick)
            .semantics { if (dot) contentDescription = "${tab.label}, new posts" }
            .testTag("home_tab:${tab.name.lowercase()}"),
    ) {
        Column(modifier = Modifier.width(IntrinsicSize.Max), horizontalAlignment = Alignment.CenterHorizontally) {
            Text(
                text = tab.label,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = if (active) FontWeight.Bold else FontWeight.SemiBold,
                color = if (active) UsTheme.extended.onMedia else UsTheme.extended.onMediaMuted,
                maxLines = 1,
            )
            Box(
                modifier = Modifier
                    .padding(top = UsTheme.spacing.xs)
                    .fillMaxWidth(UNDERLINE_FRACTION)
                    .height(UNDERLINE_HEIGHT)
                    .clip(CircleShape)
                    .background(if (active) UsTheme.extended.onMedia else Color.Transparent),
            )
        }
        if (dot) {
            Box(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    // Clear of the word's last letter, ringed so it reads on a
                    // light photo (2026-10-19: 6dp on the "g" was easy to miss).
                    .offset(x = DOT_SIZE / 2 + DOT_GAP, y = UsTheme.spacing.xs)
                    .size(DOT_SIZE)
                    .clip(CircleShape)
                    .background(UsTheme.extended.stage)
                    .padding(DOT_RING)
                    .clip(CircleShape)
                    .background(UsTheme.extended.liveRed)
                    .testTag("home_tab_dot"),
            )
        }
    }
}

/** What the header covers below the status bar: its row. */
internal val HEADER_HEIGHT = 48.dp

/** The caption stops short of the rail so a long line never runs under it. */
private val RAIL_CLEARANCE = 80.dp
private val UNDERLINE_HEIGHT = 2.dp
private const val UNDERLINE_FRACTION = 0.6f
private val DOT_SIZE = 9.dp
private val DOT_RING = 1.5.dp
private val DOT_GAP = 2.dp
private const val VIDEO_CROP_HIGH = 1.35f
