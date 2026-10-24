// MatchingDeclarationName: the file is the trending list on screen, not its actions class.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.feed.ui.sound

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.RISING_LABEL
import com.us.android.core.feed.data.TrendingFilter
import com.us.android.core.feed.data.TrendingSound
import com.us.android.core.feed.data.reelsThisWeek
import com.us.android.core.feed.data.trendingRankLabel
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.model.ReelSound
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/*
 * Trending sounds on screen (2026-10-24): the shelf at the top of the sound
 * picker, the full list with its All / Rising tabs (in the picker, and as a
 * pushed screen from a sound's page), and the badges a sound page wears.
 * Every row: rank, cover, title, artist, "N reels this week", a
 * press-to-play preview and "Use this sound".
 */

/** Everything the shelf and the list can ask for. */
class TrendingSoundsActions(
    val onFilter: (TrendingFilter) -> Unit,
    val onRetry: () -> Unit,
    val onLoadMore: () -> Unit,
    val onTogglePreview: (TrendingSound) -> Unit,
    val onUse: (TrendingSound) -> Unit,
)

/** The actions of [viewModel], once. */
fun trendingActions(viewModel: TrendingSoundsViewModel) = TrendingSoundsActions(
    onFilter = viewModel::onFilter,
    onRetry = { viewModel.load(more = false) },
    onLoadMore = { viewModel.load(more = true) },
    onTogglePreview = viewModel::togglePreview,
    onUse = viewModel::use,
)

/**
 * The pushed screen (from a sound's page). [onChosen] runs once per "Use
 * this sound", after the sound is in `SoundEntry`: `:app` opens the reel
 * create flow, which takes it.
 */
@Composable
fun TrendingSoundsScreen(
    onBack: () -> Unit,
    onChosen: () -> Unit,
    viewModel: TrendingSoundsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val preview by viewModel.previewState.collectAsStateWithLifecycle()
    ChosenFollows(state.chosenSoundId, viewModel, onChosen)
    PreviewFollowsTrending(viewModel)

    UsScaffold(
        topBar = { UsTopBar(title = TRENDING_TITLE, onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        TrendingSoundsContent(
            state = state,
            preview = preview,
            actions = trendingActions(viewModel),
            modifier = Modifier.padding(padding),
        )
    }
}

/**
 * The full list inside the sound picker, under its own back row: the picker
 * shows it in place of the library, so "Use" closes the picker exactly as a
 * library row's does. The picker shares [viewModel] with its shelf and owns
 * the preview's lifecycle ([PreviewFollowsTrending]).
 */
@Composable
fun TrendingSoundsPanel(
    onBack: () -> Unit,
    onChosen: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: TrendingSoundsViewModel = hiltViewModel(),
    useLabel: String = USE_THIS_SOUND,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val preview by viewModel.previewState.collectAsStateWithLifecycle()
    ChosenFollows(state.chosenSoundId, viewModel, onChosen)

    Column(modifier = modifier.fillMaxSize().testTag("trending_sounds_panel")) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(
                onClick = {
                    viewModel.stopPreview()
                    onBack()
                },
                modifier = Modifier.testTag("trending_sounds_back"),
            ) {
                Icon(
                    imageVector = UsIcons.Back,
                    contentDescription = "Back to sounds",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            Text(
                text = TRENDING_TITLE,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.semantics { heading() },
            )
        }
        TrendingSoundsContent(
            state = state,
            preview = preview,
            actions = trendingActions(viewModel),
            useLabel = useLabel,
            modifier = Modifier.weight(1f),
        )
    }
}

/** The tabs and the rows, from the state alone, so it can be drawn without a ViewModel. */
@Composable
fun TrendingSoundsContent(
    state: TrendingSoundsUiState,
    preview: SoundPreview,
    actions: TrendingSoundsActions,
    modifier: Modifier = Modifier,
    useLabel: String = USE_THIS_SOUND,
    contentPadding: PaddingValues = PaddingValues(0.dp),
) {
    val tab = state.current
    Column(modifier = modifier.fillMaxSize().testTag("trending_sounds")) {
        TrendingTabs(selected = state.filter, onFilter = actions.onFilter)
        if (preview == SoundPreview.FAILED && state.previewingId != null) {
            Text(
                text = "That preview couldn't play. Press play to try again.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.statusDanger,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
                    .semantics { liveRegion = LiveRegionMode.Polite }
                    .testTag("trending_sound_preview_error"),
            )
        }
        Box(modifier = Modifier.weight(1f)) {
            when {
                tab.loading && tab.sounds.isEmpty() -> UsLoadingState(label = "Loading trending sounds")
                tab.error != null -> UsErrorState(message = tab.error, onRetry = actions.onRetry)
                tab.sounds.isEmpty() -> UsEmptyState(
                    title = "Nothing trending yet",
                    detail = if (state.filter == TrendingFilter.RISING) {
                        "No sound is rising this week. Check All for the top sounds."
                    } else {
                        "Sounds trend when people use them in reels. Check back soon."
                    },
                    modifier = Modifier.testTag("trending_sounds_empty"),
                )
                else -> LazyColumn(contentPadding = contentPadding, modifier = Modifier.fillMaxSize()) {
                    items(tab.sounds, key = { it.sound.id }) { sound ->
                        TrendingSoundRow(
                            sound = sound,
                            preview = if (state.previewingId == sound.sound.id) preview else SoundPreview.IDLE,
                            useLabel = useLabel,
                            onTogglePreview = { actions.onTogglePreview(sound) },
                            onUse = { actions.onUse(sound) },
                        )
                    }
                    if (tab.hasMore || tab.moreFailed) {
                        item(key = "more") {
                            Box(
                                contentAlignment = Alignment.Center,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = UsTheme.spacing.l),
                            ) {
                                UsPillButton(
                                    text = if (tab.moreFailed) "Try again" else "Load more",
                                    onClick = actions.onLoadMore,
                                    filled = false,
                                    busy = tab.loadingMore,
                                    modifier = Modifier.testTag("trending_sounds_more"),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

/** All / Rising. */
@Composable
private fun TrendingTabs(selected: TrendingFilter, onFilter: (TrendingFilter) -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs)
            .testTag("trending_sound_tabs"),
    ) {
        TrendingFilter.entries.forEach { filter ->
            UsPillButton(
                text = filter.label,
                onClick = { onFilter(filter) },
                filled = filter == selected,
                modifier = Modifier
                    .semantics {
                        role = Role.Tab
                        this.selected = filter == selected
                    }
                    .testTag("trending_sound_tab:${filter.wire}"),
            )
        }
    }
}

/** One ranked row: rank, cover, what it is and how often it was used this week, preview, and "Use". */
@Suppress("LongParameterList") // the row's facts and its two actions, each the caller's
@Composable
private fun TrendingSoundRow(
    sound: TrendingSound,
    preview: SoundPreview,
    useLabel: String,
    onTogglePreview: () -> Unit,
    onUse: () -> Unit,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("trending_sound:${sound.sound.id}"),
    ) {
        Text(
            text = trendingRankLabel(sound.rank),
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.widthIn(min = RANK_WIDTH).testTag("trending_sound_rank"),
        )
        SoundCover(coverUrl = sound.coverUrl, size = ROW_COVER)
        Column(modifier = Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = sound.sound.title,
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (sound.rising) {
                    RisingChip(modifier = Modifier.padding(start = UsTheme.spacing.s))
                }
            }
            if (sound.sound.artist.isNotBlank()) {
                Text(
                    text = sound.sound.artist,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            val uses = reelsThisWeek(sound.uses7d)
            if (uses.isNotEmpty()) {
                Text(
                    text = uses,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textDim,
                    modifier = Modifier.testTag("trending_sound_uses"),
                )
            }
        }
        SoundPreviewButton(preview = preview, onClick = onTogglePreview, size = ROW_PREVIEW)
        UsPillButton(
            text = useLabel,
            onClick = onUse,
            modifier = Modifier
                .semantics { contentDescription = "$useLabel: ${sound.sound.title}" }
                .testTag("trending_sound_use:${sound.sound.id}"),
        )
    }
}

/**
 * The shelf at the top of the sound picker: the top [TrendingSoundsUiState.shelf]
 * sounds, side by side, Rising ones chipped, and "See all" for the full list.
 * Nothing at all while it loads, fails or is empty — the library below is
 * the picker's main list and must not wait on it.
 */
@Composable
fun TrendingSoundsShelf(
    state: TrendingSoundsUiState,
    preview: SoundPreview,
    actions: TrendingSoundsActions,
    onSeeAll: () -> Unit,
    modifier: Modifier = Modifier,
    useLabel: String = "Use",
) {
    val shelf = state.shelf
    if (shelf.isEmpty()) return
    Column(modifier = modifier.fillMaxWidth().testTag("trending_shelf")) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = UsTheme.spacing.pageHorizontal, end = UsTheme.spacing.s),
        ) {
            Text(
                text = "Trending",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f).semantics { heading() },
            )
            TextButton(onClick = onSeeAll, modifier = Modifier.testTag("trending_shelf_see_all")) {
                Text(
                    text = "See all",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.accentStrong,
                )
            }
        }
        LazyRow(
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            modifier = Modifier.fillMaxWidth(),
        ) {
            items(shelf, key = { it.sound.id }) { sound ->
                ShelfCard(
                    sound = sound,
                    preview = if (state.previewingId == sound.sound.id) preview else SoundPreview.IDLE,
                    useLabel = useLabel,
                    onTogglePreview = { actions.onTogglePreview(sound) },
                    onUse = { actions.onUse(sound) },
                )
            }
        }
    }
}

@Suppress("LongParameterList") // the card's facts and its two actions, each the caller's
@Composable
private fun ShelfCard(
    sound: TrendingSound,
    preview: SoundPreview,
    useLabel: String,
    onTogglePreview: () -> Unit,
    onUse: () -> Unit,
) {
    Column(
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .width(SHELF_CARD)
            .testTag("trending_shelf_sound:${sound.sound.id}"),
    ) {
        Box {
            SoundCover(coverUrl = sound.coverUrl, size = SHELF_CARD)
            val rank = trendingRankLabel(sound.rank)
            if (rank.isNotEmpty()) {
                Text(
                    text = rank,
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier
                        .align(Alignment.TopStart)
                        .padding(UsTheme.spacing.s)
                        .clip(RoundedCornerShape(UsTheme.radii.pill))
                        .background(UsTheme.extended.bgSheet)
                        .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
                )
            }
            SoundPreviewButton(
                preview = preview,
                onClick = onTogglePreview,
                size = ROW_PREVIEW,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(UsTheme.spacing.s),
            )
        }
        Text(
            text = sound.sound.title,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = sound.sound.artist.ifBlank { reelsThisWeek(sound.uses7d) },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            if (sound.rising) RisingChip(modifier = Modifier.padding(start = UsTheme.spacing.xs))
        }
        UsPillButton(
            text = useLabel,
            onClick = onUse,
            filled = false,
            modifier = Modifier
                .semantics { contentDescription = "$useLabel: ${sound.sound.title}" }
                .testTag("trending_shelf_use:${sound.sound.id}"),
        )
    }
}

/** The sound's cover when the server sent one, else a music tile. */
@Composable
private fun SoundCover(coverUrl: String?, size: Dp) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(size)
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(UsTheme.extended.bgCard),
    ) {
        Icon(
            imageVector = UsIcons.Music,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(size * COVER_GLYPH_FRACTION),
        )
        if (coverUrl != null) {
            AsyncImage(
                model = coverUrl,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** "Rising", on a shelf card, a row, and a sound's page. */
@Composable
fun RisingChip(modifier: Modifier = Modifier) {
    TrendingBadge(text = RISING_LABEL, modifier = modifier.testTag("trending_rising"))
}

/** One badge: "#3 trending" or "Rising". */
@Composable
fun TrendingBadge(text: String, modifier: Modifier = Modifier) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.onPrimary,
        maxLines = 1,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.pill))
            .background(UsTheme.extended.accentSolid)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
    )
}

/** A "Use" has left the sound in `SoundEntry`: the screen acts on it once. */
@Composable
private fun ChosenFollows(chosenSoundId: String?, viewModel: TrendingSoundsViewModel, onChosen: () -> Unit) {
    LaunchedEffect(chosenSoundId) {
        if (chosenSoundId != null) {
            viewModel.onChosenHandled()
            onChosen()
        }
    }
}

/** The preview pauses when the surface stops, and is forgotten when the list leaves the composition. */
@Composable
fun PreviewFollowsTrending(viewModel: TrendingSoundsViewModel) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) viewModel.pausePreview()
        }
        owner.lifecycle.addObserver(observer)
        onDispose {
            owner.lifecycle.removeObserver(observer)
            viewModel.stopPreview()
        }
    }
}

@Suppress("MagicNumber") // preview data
@Preview
@Composable
private fun TrendingSoundsPreview() {
    val rows = listOf(
        previewRow(1, "Morning run", "Asha", 42L, rising = true),
        previewRow(2, "Original sound - Ravi", "Ravi", 1L, rising = false),
    )
    UsTheme {
        Column {
            TrendingSoundsShelf(
                state = TrendingSoundsUiState(all = TrendingTab(sounds = rows, loaded = true)),
                preview = SoundPreview.IDLE,
                actions = TrendingSoundsActions({}, {}, {}, {}, {}),
                onSeeAll = {},
            )
            TrendingSoundsContent(
                state = TrendingSoundsUiState(all = TrendingTab(sounds = rows, loaded = true, nextCursor = "c")),
                preview = SoundPreview.PLAYING,
                actions = TrendingSoundsActions({}, {}, {}, {}, {}),
            )
        }
    }
}

@Suppress("MagicNumber") // preview data
private fun previewRow(rank: Int, title: String, artist: String, uses: Long, rising: Boolean) = TrendingSound(
    rank = rank,
    sound = ReelSound("s$rank", title, artist, 0L, 28_000L, 3, null, null),
    coverUrl = null,
    uses7d = uses,
    usesPrev7d = 0L,
    rising = rising,
)

/** The list's title, on the pushed screen and in the picker. */
const val TRENDING_TITLE = "Trending sounds"

private const val USE_THIS_SOUND = "Use this sound"
private val RANK_WIDTH = 28.dp
private val ROW_COVER = 48.dp
private val ROW_PREVIEW = 36.dp
private val SHELF_CARD = 132.dp
private const val COVER_GLYPH_FRACTION = 0.4f
