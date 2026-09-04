package com.us.android.feature.feed.ui

import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.media3.ui.compose.modifiers.resizeWithContentScale
import androidx.media3.ui.compose.state.rememberPresentationState
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.modifier.usMediaScrim
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.PostActionBar
import com.us.android.core.ui.PostCardMediaPage
import com.us.android.core.ui.PostCardState

/**
 * The Home feed's in-place viewer: a card's media, full screen, in the same
 * screen the card is on.
 *
 * ## WHY NOT A DESTINATION
 *
 * Tapping a card used to push post detail. The founder's call (2026-09-04):
 * no navigation on tap — a photo or a video opens OVER the feed and closes
 * back onto the same scroll position, the way every gallery does. So this is
 * state in the feed screen, not a route: a full-window [Dialog] so it covers
 * the header and the bottom bar too, closed by Back, by the X, or by dragging
 * the media down past a third of the screen.
 *
 * Video plays with sound and loops through its own player — the viewer opened
 * it deliberately, which is the one case autoplaying audio is not hostile.
 * The player is released the moment the overlay closes.
 */
@Suppress("LongParameterList")
@Composable
internal fun FeedMediaViewer(
    item: FeedItem,
    state: PostCardState,
    onClose: () -> Unit,
    onAuthorClick: () -> Unit,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onRepost: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
    viewModel: FeedViewerViewModel = hiltViewModel(),
) {
    Dialog(
        onDismissRequest = onClose,
        properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) {
        BackHandler(onBack = onClose)
        var dragY by remember { mutableFloatStateOf(0f) }
        var dragHeight by remember { mutableFloatStateOf(1f) }
        Box(
            modifier = Modifier
                .fillMaxSize()
                .onSizeChanged { dragHeight = it.height.toFloat().coerceAtLeast(1f) }
                .graphicsLayer {
                    // The whole viewer follows the finger down and fades a
                    // little, so the gesture reads as "putting it back".
                    translationY = dragY
                    alpha = 1f - (dragY / dragHeight) * DRAG_FADE
                }
                .background(UsTheme.extended.bgCanvas)
                .pointerInput(Unit) {
                    detectVerticalDragGestures(
                        onDragEnd = {
                            if (dragY > dragHeight * CLOSE_FRACTION) onClose() else dragY = 0f
                        },
                        onDragCancel = { dragY = 0f },
                    ) { _, delta ->
                        dragY = (dragY + delta).coerceAtLeast(0f)
                    }
                }
                .testTag("feed_media_viewer"),
        ) {
            val playback = remember(item.id) { viewModel.playback(item) }
            if (playback != null) {
                val player = remember(playback) { viewModel.createPlayer(playback) }
                DisposableEffect(player) { onDispose { player.release() } }
                ViewerVideo(player = player, modifier = Modifier.fillMaxSize())
            } else {
                ViewerImages(pages = state.mediaPages, modifier = Modifier.fillMaxSize())
            }

            CloseButton(
                onClick = onClose,
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .statusBarsPadding()
                    .padding(UsTheme.spacing.l),
            )

            ViewerFooter(
                state = state,
                onAuthorClick = onAuthorClick,
                onReact = onReact,
                onComment = onComment,
                onRepost = onRepost,
                onBookmark = onBookmark,
                onShare = onShare,
                modifier = Modifier.align(Alignment.BottomStart),
            )
        }
    }
}

/**
 * The video, letterboxed to its own aspect on the navy ground rather than
 * stretched to the window: a landscape clip tapped from the feed should look
 * like the clip, not like a funhouse mirror. Tapping it pauses and resumes.
 */
@OptIn(UnstableApi::class)
@Composable
private fun ViewerVideo(player: ExoPlayer, modifier: Modifier = Modifier) {
    val presentation = rememberPresentationState(player)
    Box(
        modifier = modifier.clickable(
            interactionSource = remember { MutableInteractionSource() },
            indication = null,
        ) { player.playWhenReady = !player.playWhenReady },
        contentAlignment = Alignment.Center,
    ) {
        PlayerSurface(
            player = player,
            surfaceType = SURFACE_TYPE_SURFACE_VIEW,
            modifier = Modifier.resizeWithContentScale(ContentScale.Fit, presentation.videoSizeDp),
        )
    }
}

/** Every page of the row's carousel, swipeable, each fitted whole on screen. */
@Composable
private fun ViewerImages(pages: List<PostCardMediaPage>, modifier: Modifier = Modifier) {
    val pagerState = rememberPagerState(pageCount = { pages.size })
    Box(modifier = modifier) {
        HorizontalPager(state = pagerState, modifier = Modifier.fillMaxSize()) { index ->
            val page = pages[index]
            AsyncImage(
                model = page.url,
                contentDescription = page.contentDescription,
                contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (pages.size > 1) {
            Text(
                text = "${pagerState.currentPage + 1}/${pages.size}",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Medium,
                color = Color.White,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .statusBarsPadding()
                    .padding(UsTheme.spacing.l)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(Color.Black.copy(alpha = PLATE_ALPHA))
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
            )
        }
    }
}

@Composable
private fun CloseButton(onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .size(CLOSE_TARGET)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics { role = Role.Button },
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = UsIcons.Close,
            contentDescription = "Close",
            tint = Color.White,
            modifier = Modifier.size(CLOSE_GLYPH),
        )
    }
}

/**
 * Author, caption and the SAME action row the card has, over the media's
 * bottom edge. The row is the card's so a like here is the same like there.
 */
@Suppress("LongParameterList")
@Composable
private fun ViewerFooter(
    state: PostCardState,
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
            .usMediaScrim()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = FOOTER_SCRIM_RAMP)
            .navigationBarsPadding()
            .padding(bottom = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Text(
            text = state.authorName,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onAuthorClick,
                )
                .semantics { role = Role.Button },
        )
        if (state.text.isNotBlank()) {
            Text(
                text = state.text,
                style = MaterialTheme.typography.bodyMedium,
                color = Color.White,
                maxLines = CAPTION_MAX_LINES,
                overflow = TextOverflow.Ellipsis,
            )
        }
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

private const val CAPTION_MAX_LINES = 4

/** Past a third of the screen the drag is a decision, not a fidget. */
private const val CLOSE_FRACTION = 0.3f

/** How much the viewer fades over a full-height drag. Subtle: the media stays the subject. */
private const val DRAG_FADE = 0.5f
private const val PLATE_ALPHA = 0.55f
private val CLOSE_TARGET = 44.dp
private val CLOSE_GLYPH = 22.dp
private val FOOTER_SCRIM_RAMP = 72.dp
