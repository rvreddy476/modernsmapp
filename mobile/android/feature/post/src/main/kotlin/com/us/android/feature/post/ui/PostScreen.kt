package com.us.android.feature.post.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.media3.ui.compose.modifiers.resizeWithContentScale
import androidx.media3.ui.compose.state.rememberPresentationState
import coil3.compose.AsyncImage
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.engagement.data.saveCountOr
import com.us.android.core.media.Playback
import com.us.android.core.media.ui.VideoLoadingIndicator
import com.us.android.core.model.Post
import com.us.android.core.ui.EngagementFailureBar
import com.us.android.core.ui.LightSystemBarGlyphs
import com.us.android.core.ui.PORTRAIT_SCREEN_ASPECT
import com.us.android.core.ui.PostCardMediaPage
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
import com.us.android.core.ui.StageRailControl
import com.us.android.core.ui.StageTextCard
import com.us.android.core.ui.StageTopScrim
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.VIDEO_POST
import com.us.android.core.ui.rememberPostSharer
import com.us.android.core.ui.stageCountLabel

/**
 * One post, full screen (2026-10-19) — what opens from a profile grid, a
 * notification, a link or a published draft. The same stage Home draws: the
 * media edge to edge, the caption bottom-left, the shared rail bottom-right,
 * and an "Add comment…" bar at the foot. It replaced a light card page that
 * showed neither the title nor the counts the rail now carries.
 */
@Composable
fun PostScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: PostViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val failures by viewModel.failures.collectAsStateWithLifecycle()
    // Comments open OVER the post, the same sheet the feed and reels use:
    // Back closes the sheet rather than the post.
    var commentsOpen by rememberSaveable { mutableStateOf(false) }
    PostContent(
        state = state,
        failures = failures,
        onRetryFailure = viewModel::retryFailure,
        onDismissFailure = viewModel::dismissFailure,
        onBack = onBack,
        onRetry = viewModel::load,
        onReact = viewModel::onReactToggle,
        onBookmark = viewModel::onBookmarkToggle,
        onDismissActionError = viewModel::dismissActionError,
        onOpenAuthor = onOpenAuthor,
        onOpenComments = { commentsOpen = true },
        onPageSettled = viewModel::onPageSettled,
        videoPlayer = viewModel::player,
    )
    if (commentsOpen) {
        PostCommentsSheet(
            onDismiss = { commentsOpen = false },
            postAuthorId = (state as? PostUiState.Content)?.post?.authorId.orEmpty(),
        )
    }
}

/** Stateless renderer. Immutable state in, callbacks out; fetches nothing. */
@Suppress("LongParameterList")
@Composable
internal fun PostContent(
    state: PostUiState,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onDismissActionError: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    modifier: Modifier = Modifier,
    failures: List<EngagementFailure> = emptyList(),
    onRetryFailure: (String, EngagementAction) -> Unit = { _, _ -> },
    onDismissFailure: (String, EngagementAction) -> Unit = { _, _ -> },
    /** Fired when the carousel settles, so only the visible page is resolved. */
    onPageSettled: (Int) -> Unit = {},
    /** The player a video post plays in; null draws its poster only. */
    videoPlayer: ((Playback) -> Player)? = null,
) {
    // A stage in both themes, like Home: dark ground, light glyphs.
    UsTheme(darkTheme = true) {
        LightSystemBarGlyphs()
        Box(
            modifier = modifier
                .fillMaxSize()
                .background(UsTheme.extended.stage)
                .testTag("post_stage"),
        ) {
            when (state) {
                is PostUiState.Loading -> UsLoadingState(
                    label = "Loading post",
                    modifier = Modifier.align(Alignment.Center),
                )
                is PostUiState.Error -> UsErrorState(
                    message = state.message,
                    onRetry = if (state.retryable) onRetry else null,
                    modifier = Modifier.align(Alignment.Center),
                )
                is PostUiState.Content -> PostStage(
                    state = state,
                    onReact = onReact,
                    onBookmark = onBookmark,
                    onOpenAuthor = onOpenAuthor,
                    onOpenComments = onOpenComments,
                    onPageSettled = onPageSettled,
                    videoPlayer = videoPlayer,
                )
            }

            StageTopScrim(modifier = Modifier.align(Alignment.TopCenter))
            IconButton(
                onClick = onBack,
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .statusBarsPadding()
                    .padding(UsTheme.spacing.xs)
                    .testTag("post_back"),
            ) {
                Icon(UsIcons.Back, contentDescription = "Back", tint = UsTheme.extended.onMedia)
            }

            // Under the header, over the media: a failed like is about this
            // session, not about what the author wrote.
            Column(
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .statusBarsPadding()
                    .padding(top = HEADER_HEIGHT)
                    .fillMaxWidth(),
            ) {
                EngagementFailureBar(failures = failures, onRetry = onRetryFailure, onDismiss = onDismissFailure)
                (state as? PostUiState.Content)?.actionError?.let { error ->
                    ActionErrorPlate(error = error, onDismiss = onDismissActionError)
                }
            }
        }
    }
}

/** What a post page draws: its photos, its video, or its words. Pure. */
internal enum class PostStageKind { PHOTO, VIDEO, TEXT }

internal fun postStageKind(post: Post): PostStageKind = when {
    post.media.isEmpty() -> PostStageKind.TEXT
    post.media.first().kind == VIDEO_POST -> PostStageKind.VIDEO
    else -> PostStageKind.PHOTO
}

/** The caption block for a post. A text page already shows its words, so the caption does not repeat them. Pure. */
internal fun postStageCaption(post: Post, handle: String, kind: PostStageKind = postStageKind(post)) = StageCaptionState(
    postId = post.id,
    handle = handle,
    date = formatRelativeTime(post.createdAt).takeIf { it.isNotBlank() },
    title = post.title.takeIf { it.isNotBlank() && kind != PostStageKind.TEXT },
    text = if (kind == PostStageKind.TEXT) "" else post.text,
    hashtags = post.hashtags,
    labels = post.disclosure.labels,
)

@Suppress("LongMethod", "LongParameterList")
@Composable
private fun PostStage(
    state: PostUiState.Content,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    onPageSettled: (Int) -> Unit,
    videoPlayer: ((Playback) -> Player)?,
) {
    val post = state.post
    val author = state.author
    val share = rememberPostSharer()
    val kind = postStageKind(post)
    val pages = post.media.map { ref ->
        val delivery = state.media[ref.mediaId]
        val page = PostCardMediaPage(mediaId = ref.mediaId, url = delivery?.posterUrl, contentDescription = ref.contentDescription)
        delivery?.aspectRatio?.let { page.copy(aspectRatio = it) } ?: page
    }
    val carousel = rememberPagerState(pageCount = { pages.size })
    // Resolve the page the reader has landed on, not every page.
    LaunchedEffect(carousel.settledPage, pages.size) { onPageSettled(carousel.settledPage) }
    val authorName = author?.nameForDisplay ?: author?.username.orEmpty()
    val handle = author?.username?.takeIf { it.isNotBlank() }?.let { "@$it" } ?: authorName

    Column(modifier = Modifier.fillMaxSize()) {
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            when (kind) {
                PostStageKind.PHOTO -> StagePhotoCarousel(pages = pages, pagerState = carousel)
                PostStageKind.VIDEO -> PostVideo(
                    page = pages.first(),
                    hlsUrl = state.media[post.media.first().mediaId]?.takeIf { it.isReady }?.hlsUrl,
                    videoPlayer = videoPlayer,
                )
                PostStageKind.TEXT -> StageTextCard(
                    seed = post.id,
                    text = post.text.ifBlank { post.title },
                    contentPadding = PaddingValues(top = HEADER_HEIGHT),
                )
            }
            if (kind == PostStageKind.PHOTO && pages.size > 1) {
                StagePageCounter(
                    current = carousel.currentPage,
                    count = pages.size,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .statusBarsPadding()
                        .padding(top = UsTheme.spacing.m, end = UsTheme.spacing.l),
                )
            }
            StageBottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
            StageCaption(
                state = postStageCaption(post, handle, kind),
                onOpenAuthor = { onOpenAuthor(post.authorId) },
                above = {
                    if (kind == PostStageKind.PHOTO && pages.size > 1) {
                        StagePageDots(pageCount = pages.size, currentPage = carousel.currentPage)
                    }
                },
                tagPrefix = "post_caption",
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .padding(start = UsTheme.spacing.xxl, end = RAIL_CLEARANCE, bottom = UsTheme.spacing.l),
            )
            StageRail(
                author = StageRailAuthor(name = authorName, seed = post.authorId, onOpen = { onOpenAuthor(post.authorId) }),
                controls = postRailControls(
                    post = post,
                    state = state,
                    onReact = onReact,
                    onComment = onOpenComments,
                    onBookmark = onBookmark,
                    onShare = { share(post.id, post.text.ifBlank { post.title }, authorName.ifBlank { null }) },
                ),
                sound = null,
                tagPrefix = "post_rail",
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(end = UsTheme.spacing.m, bottom = UsTheme.spacing.l),
            )
        }
        CommentBar(enabled = post.allowsComments, onClick = onOpenComments)
    }
}

/** The rail for a post: like, comment, save and share, each with its count. */
@Suppress("LongParameterList")
@Composable
private fun postRailControls(
    post: Post,
    state: PostUiState.Content,
    onReact: () -> Unit,
    onComment: () -> Unit,
    onBookmark: () -> Unit,
    onShare: () -> Unit,
): List<StageRailControl> {
    val overlay = state.overlay
    val reacted = overlay.reactedOr(post.viewer.hasReacted)
    val saved = overlay.bookmarkedOr(post.viewer.isBookmarked)
    return buildList {
        add(
            StageRailControl(
                key = "like",
                icon = if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
                description = if (reacted) "Liked" else "Like",
                label = stageCountLabel(overlay.likeCountOr(post.counts.likes, post.viewer.hasReacted), "Like"),
                tint = if (reacted) UsTheme.extended.liveRed else null,
                onClick = onReact,
            ),
        )
        if (post.allowsComments) {
            add(
                StageRailControl(
                    key = "comments",
                    icon = UsIcons.Comment,
                    description = "Comments",
                    label = stageCountLabel(post.counts.comments, "Comment"),
                    onClick = onComment,
                ),
            )
        }
        add(
            StageRailControl(
                key = if (saved) "saved" else "save",
                icon = if (saved) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
                description = if (saved) "Saved" else "Save",
                label = stageCountLabel(overlay.saveCountOr(post.counts.saves, post.viewer.isBookmarked), if (saved) "Saved" else "Save"),
                tint = if (saved) UsTheme.extended.accent else null,
                onClick = onBookmark,
            ),
        )
        if (!post.hideShare) {
            add(
                StageRailControl(
                    key = "share",
                    icon = UsIcons.Share,
                    description = "Share",
                    label = stageCountLabel(post.counts.shares, "Share"),
                    onClick = onShare,
                ),
            )
        }
    }
}

/**
 * A video post: it plays looping as soon as it is ready, a tap holds it, and
 * it holds while the screen is in the background. Its poster until then.
 */
@OptIn(UnstableApi::class)
@Composable
private fun PostVideo(page: PostCardMediaPage, hlsUrl: String?, videoPlayer: ((Playback) -> Player)?) {
    if (hlsUrl == null || videoPlayer == null) {
        StagePhoto(page = page)
        return
    }
    val player = remember(hlsUrl) { videoPlayer(Playback.hls(hlsUrl)) }
    var paused by remember { mutableStateOf(false) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(player, lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_PAUSE -> player.playWhenReady = false
                Lifecycle.Event.ON_RESUME -> player.playWhenReady = !paused
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    val presentation = rememberPresentationState(player)
    val scale = postVideoScale(page.aspectRatio)
    Box(
        modifier = Modifier
            .fillMaxSize()
            .pointerInput(player) {
                detectTapGestures(onTap = {
                    paused = !paused
                    player.playWhenReady = !paused
                })
            }
            .testTag("post_video"),
        contentAlignment = Alignment.Center,
    ) {
        PlayerSurface(
            player = player,
            surfaceType = SURFACE_TYPE_SURFACE_VIEW,
            modifier = Modifier.resizeWithContentScale(scale, presentation.videoSizeDp),
        )
        if (presentation.coverSurface && page.url != null) {
            AsyncImage(model = page.url, contentDescription = null, contentScale = scale, modifier = Modifier.fillMaxSize())
        }
        VideoLoadingIndicator(player = player, onRetry = player::prepare)
        if (paused) StagePausedGlyph(modifier = Modifier.align(Alignment.Center))
    }
}

/** Crop a video close to the screen's shape, fit anything else. Pure. */
internal fun postVideoScale(mediaAspect: Float, screenAspect: Float = PORTRAIT_SCREEN_ASPECT): ContentScale {
    if (mediaAspect <= 0f) return ContentScale.Fit
    val ratio = mediaAspect / screenAspect
    return if (ratio in 1f..VIDEO_CROP_HIGH) ContentScale.Crop else ContentScale.Fit
}

/** The "Add comment…" bar at the foot; says so when the author turned comments off. */
@Composable
private fun CommentBar(enabled: Boolean, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.stage)
            .navigationBarsPadding()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
    ) {
        Text(
            text = if (enabled) "Add comment…" else "Comments are turned off",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMediaDim,
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.mediaPlate)
                .then(if (enabled) Modifier.clickable(onClick = onClick) else Modifier)
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
                .testTag("post_comment_bar"),
        )
    }
}

@Composable
private fun ActionErrorPlate(error: String, onDismiss: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.mediaPlate)
            .padding(start = UsTheme.spacing.l),
    ) {
        Text(
            text = error,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMedia,
            modifier = Modifier.align(Alignment.CenterStart).padding(end = DISMISS_WIDTH),
        )
        TextButton(onClick = onDismiss, modifier = Modifier.align(Alignment.CenterEnd)) {
            Text("Dismiss", color = UsTheme.extended.onMedia)
        }
    }
}

/** The back button's row, so what sits under the header clears it. */
private val HEADER_HEIGHT = 56.dp

/** The caption stops short of the rail. */
private val RAIL_CLEARANCE = 80.dp

private val DISMISS_WIDTH = 88.dp

/** A video up to this much wider than the screen is cropped; wider is fitted. */
private const val VIDEO_CROP_HIGH = 1.35f
