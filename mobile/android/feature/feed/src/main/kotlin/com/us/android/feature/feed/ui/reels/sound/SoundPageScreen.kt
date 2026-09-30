package com.us.android.feature.feed.ui.reels.sound

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.SOUND_GONE_MESSAGE
import com.us.android.core.feed.data.SoundReelTile
import com.us.android.core.feed.data.soundReelCount
import com.us.android.core.feed.ui.sound.SoundPreviewButton
import com.us.android.core.feed.ui.sound.SoundPreviewViewModel
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.model.ReelSound
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * A sound's page (original sounds, 2026-09-30): the sound's name and its
 * creator, how many reels play it, a button that plays the sound alone, "Use
 * this sound", and then the reels that play it — the one it was taken from
 * first and marked "Original", the rest newest first, more as the grid is
 * scrolled.
 *
 * A tile opens that reel in Reels; "Use this sound" opens the reel create
 * flow with the sound chosen. Both leave through `:app`: this feature knows
 * neither screen.
 */
@Composable
fun SoundPageScreen(
    onBack: () -> Unit,
    /** A tile was tapped; the reel's id is already in `ReelsEntry`. `:app` switches to the Reels tab. */
    onOpenReels: () -> Unit,
    /** "Use this sound"; the sound is already in `SoundEntry`. `:app` opens the reel create flow. */
    onCreateWithSound: () -> Unit,
    viewModel: SoundPageViewModel = hiltViewModel(),
    preview: SoundPreviewViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val playing by preview.state.collectAsStateWithLifecycle()

    PausePreviewOnStop(preview)

    UsScaffold(
        topBar = { UsTopBar(title = "Sound", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        SoundPageContent(
            state = state,
            preview = playing,
            actions = SoundPageActions(
                onTogglePreview = { preview.player.toggle(viewModel.soundId) },
                onUseSound = { if (viewModel.chooseSound()) onCreateWithSound() },
                onOpenReel = { postId ->
                    preview.player.pause()
                    viewModel.openReel(postId)
                    onOpenReels()
                },
                onLoadMore = viewModel::loadMore,
                onRetry = viewModel::load,
            ),
            posterFor = { tile -> viewModel.posterUrl(tile.reel) },
            modifier = Modifier.padding(padding),
        )
    }
}

/** Everything the page can ask for, hoisted once. */
internal class SoundPageActions(
    val onTogglePreview: () -> Unit,
    val onUseSound: () -> Unit,
    val onOpenReel: (postId: String) -> Unit,
    val onLoadMore: () -> Unit,
    val onRetry: () -> Unit,
)

/** The preview stops when the page leaves the foreground; the player itself is the holder's to release. */
@Composable
private fun PausePreviewOnStop(preview: SoundPreviewViewModel) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, preview) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) preview.player.pause()
        }
        owner.lifecycle.addObserver(observer)
        onDispose {
            owner.lifecycle.removeObserver(observer)
            preview.player.pause()
        }
    }
}

/** The page by its phase: the house's loading, empty and error views, or the sound and its reels. */
@Composable
internal fun SoundPageContent(
    state: SoundPageState,
    preview: SoundPreview,
    actions: SoundPageActions,
    posterFor: (SoundReelTile) -> String?,
    modifier: Modifier = Modifier,
) {
    when (val phase = state.phase) {
        SoundPagePhase.Loading -> UsLoadingState(modifier = modifier, label = "Loading sound")
        SoundPagePhase.Gone -> UsEmptyState(title = SOUND_GONE_MESSAGE, modifier = modifier.testTag("sound_gone"))
        is SoundPagePhase.Failed ->
            UsErrorState(message = phase.message, modifier = modifier, onRetry = actions.onRetry)
        SoundPagePhase.Ready -> SoundReels(
            state = state,
            preview = preview,
            actions = actions,
            posterFor = posterFor,
            modifier = modifier,
        )
    }
}

/**
 * The sound above a three-column grid of the reels that play it. The header
 * is the grid's first row, so it scrolls away with the tiles; the grid asks
 * for the next page when its last rows come into view.
 */
@Composable
private fun SoundReels(
    state: SoundPageState,
    preview: SoundPreview,
    actions: SoundPageActions,
    posterFor: (SoundReelTile) -> String?,
    modifier: Modifier = Modifier,
) {
    val grid = rememberLazyGridState()
    val nearEnd by remember {
        derivedStateOf {
            val last = grid.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            last >= grid.layoutInfo.totalItemsCount - LOAD_MORE_WITHIN
        }
    }
    LaunchedEffect(nearEnd, state.canLoadMore, state.tiles.size) {
        if (nearEnd && state.wantsMore) actions.onLoadMore()
    }

    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        state = grid,
        modifier = modifier
            .fillMaxSize()
            .testTag("sound_page"),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.l,
        ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        item(key = "header", span = { GridItemSpan(maxLineSpan) }) {
            SoundHeader(
                sound = state.sound,
                preview = preview,
                onTogglePreview = actions.onTogglePreview,
                onUseSound = actions.onUseSound,
            )
        }
        if (state.isEmpty) {
            item(key = "empty", span = { GridItemSpan(maxLineSpan) }) {
                UsEmptyState(
                    title = "No reels yet",
                    detail = "Be the first to use this sound.",
                    modifier = Modifier.testTag("sound_empty"),
                )
            }
        }
        items(items = state.tiles, key = { it.reel.id }) { tile ->
            SoundReelTileView(
                tile = tile,
                posterUrl = posterFor(tile),
                onClick = { actions.onOpenReel(tile.reel.id) },
            )
        }
        if (state.loadingMore || state.moreFailed) {
            item(key = "more", span = { GridItemSpan(maxLineSpan) }) {
                MoreRow(failed = state.moreFailed, onRetry = actions.onLoadMore)
            }
        }
    }
}

/** The play button, the sound's name, its creator and how many reels play it; then "Use this sound". */
@Composable
private fun SoundHeader(
    sound: ReelSound?,
    preview: SoundPreview,
    onTogglePreview: () -> Unit,
    onUseSound: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            SoundPreviewButton(preview = preview, onClick = onTogglePreview)
            Column(
                modifier = Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    text = sound?.title.orEmpty(),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = TITLE_LINES,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.testTag("sound_title"),
                )
                if (!sound?.artist.isNullOrBlank()) {
                    Text(
                        text = sound?.artist.orEmpty(),
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textSecondary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.testTag("sound_creator"),
                    )
                }
                Text(
                    text = soundReelCount(sound?.useCount ?: 0),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.testTag("sound_count"),
                )
            }
        }
        if (preview == SoundPreview.FAILED) {
            Text(
                text = "This sound couldn't be played. Tap play to try again.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusDanger,
                modifier = Modifier.testTag("sound_preview_failed"),
            )
        }
        UsButton(
            text = "Use this sound",
            onClick = onUseSound,
            enabled = sound != null,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("sound_use"),
        )
    }
}

/** One reel that plays the sound: its still, 9:16, and "Original" on the one the sound was taken from. */
@Composable
private fun SoundReelTileView(
    tile: SoundReelTile,
    posterUrl: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    val name = tile.reel.title.ifBlank { tile.reel.text }.ifBlank { "reel" }
    Box(
        modifier = modifier
            .aspectRatio(PORTRAIT_TILE)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = if (tile.isOrigin) "Open $name. Original" else "Open $name" }
            .testTag("sound_tile:${tile.reel.id}"),
    ) {
        if (posterUrl != null) {
            AsyncImage(
                model = posterUrl,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = UsIcons.Film,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(TILE_GLYPH),
            )
        }
        if (tile.isOrigin) OriginalBadge(modifier = Modifier.align(Alignment.TopStart))
    }
}

/** "Original", on the reel the sound was taken from. */
@Composable
private fun OriginalBadge(modifier: Modifier = Modifier) {
    Text(
        text = "Original",
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.onPrimary,
        modifier = modifier
            .padding(UsTheme.spacing.s)
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.accentSolid)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs)
            .testTag("sound_original"),
    )
}

/** Under the grid while a later page is on its way, or after it failed. */
@Composable
private fun MoreRow(failed: Boolean, onRetry: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l),
        contentAlignment = Alignment.Center,
    ) {
        if (failed) {
            UsSecondaryButton(text = "Load more", onClick = onRetry, modifier = Modifier.testTag("sound_more_retry"))
        } else {
            CircularProgressIndicator(
                color = MaterialTheme.colorScheme.primary,
                strokeWidth = MORE_RING_STROKE,
                modifier = Modifier.size(MORE_RING),
            )
        }
    }
}

@Preview
@Composable
private fun SoundPagePreview() {
    UsTheme {
        SoundPageContent(
            state = SoundPageState(
                phase = SoundPagePhase.Ready,
                sound = ReelSound(
                    id = "s1",
                    title = "Original sound - Asha",
                    artist = "Asha",
                    startMs = 0L,
                    durationMs = 28_400L,
                    useCount = 3,
                    sourcePostId = "p0",
                    creatorUserId = "u1",
                ),
            ),
            preview = SoundPreview.PLAYING,
            actions = SoundPageActions({}, {}, {}, {}, {}),
            posterFor = { null },
        )
    }
}

@Preview
@Composable
private fun SoundGonePreview() {
    UsTheme {
        SoundPageContent(
            state = SoundPageState(phase = SoundPagePhase.Gone),
            preview = SoundPreview.IDLE,
            actions = SoundPageActions({}, {}, {}, {}, {}),
            posterFor = { null },
        )
    }
}

private const val GRID_COLUMNS = 3

/** Ask for the next page when the last two rows are in view. */
private const val LOAD_MORE_WITHIN = GRID_COLUMNS * 2

private const val PORTRAIT_TILE = 9f / 16f
private const val TITLE_LINES = 2

private val MORE_RING = 24.dp
private val MORE_RING_STROKE = 2.dp
private val TILE_GLYPH = 22.dp
