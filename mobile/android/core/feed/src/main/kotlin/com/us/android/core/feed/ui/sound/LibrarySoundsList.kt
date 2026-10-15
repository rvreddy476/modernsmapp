package com.us.android.core.feed.ui.sound

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.LiveRegionMode
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
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.LibrarySound
import com.us.android.core.feed.data.genreLabel
import com.us.android.core.feed.data.librarySoundMeta
import com.us.android.core.feed.data.libraryUsageLabel
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/*
 * The curated sound library on screen (2026-10-15): one list, drawn in the
 * reel form's Library sheet and in the Creator Hub's Sounds tab, so the two
 * cannot drift. Genre pills, then the rows — a press-to-play preview, the
 * title and what is known of it, and "Use". Nothing plays until a preview
 * button is pressed; the preview stops when the list goes away.
 */

/**
 * The Library sheet over the reel form. [onChosen] runs once a "Use" has
 * left the sound in `SoundEntry`; the form takes it from there and the
 * sheet is closed by the caller.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibrarySoundSheet(
    onDismiss: () -> Unit,
    onChosen: () -> Unit,
    viewModel: LibrarySoundsViewModel = hiltViewModel(),
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        modifier = Modifier.testTag("library_sound_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .fillMaxHeight(SHEET_HEIGHT_FRACTION)
                .navigationBarsPadding(),
        ) {
            Text(
                text = "Sounds",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.pageHorizontal)
                    .semantics { heading() },
            )
            Text(
                text = "Preview a sound from the library and add it to your reel.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(
                    start = UsTheme.spacing.pageHorizontal,
                    end = UsTheme.spacing.pageHorizontal,
                    top = UsTheme.spacing.xs,
                    bottom = UsTheme.spacing.m,
                ),
            )
            LibrarySoundsList(onChosen = onChosen, viewModel = viewModel, useLabel = "Use")
        }
    }
}

/**
 * The list itself, with its genre filter. [onChosen] runs once per "Use"
 * that succeeded, after the sound is in `SoundEntry`. The preview pauses
 * when the surface stops and is forgotten when the list leaves.
 */
@Composable
fun LibrarySoundsList(
    onChosen: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: LibrarySoundsViewModel = hiltViewModel(),
    useLabel: String = "Use this sound",
    contentPadding: PaddingValues = PaddingValues(0.dp),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val preview by viewModel.previewState.collectAsStateWithLifecycle()

    LaunchedEffect(state.chosenSoundId) {
        if (state.chosenSoundId != null) {
            viewModel.onChosenHandled()
            onChosen()
        }
    }
    PreviewFollowsList(viewModel)

    LibrarySoundsContent(
        state = state,
        preview = preview,
        actions = LibrarySoundsActions(
            onGenre = viewModel::onGenre,
            onRetry = { viewModel.load(more = false) },
            onLoadMore = { viewModel.load(more = true) },
            onTogglePreview = viewModel::togglePreview,
            onUse = viewModel::use,
        ),
        useLabel = useLabel,
        contentPadding = contentPadding,
        modifier = modifier,
    )
}

/** Everything the list can ask for. */
class LibrarySoundsActions(
    val onGenre: (String) -> Unit,
    val onRetry: () -> Unit,
    val onLoadMore: () -> Unit,
    val onTogglePreview: (LibrarySound) -> Unit,
    val onUse: (LibrarySound) -> Unit,
)

/** The list from its state alone, so it can be drawn without a ViewModel. */
@Composable
fun LibrarySoundsContent(
    state: LibrarySoundsUiState,
    preview: SoundPreview,
    actions: LibrarySoundsActions,
    modifier: Modifier = Modifier,
    useLabel: String = "Use this sound",
    contentPadding: PaddingValues = PaddingValues(0.dp),
) {
    Column(modifier = modifier.fillMaxSize().testTag("library_sounds")) {
        if (state.genres.isNotEmpty()) {
            GenrePills(genres = state.genres, selected = state.genre, onGenre = actions.onGenre)
        }
        state.useMessage?.let { message ->
            Notice(text = message, testTag = "library_sound_use_error")
        }
        if (preview == SoundPreview.FAILED && state.previewingId != null) {
            Notice(text = "That preview couldn't play. Press play to try again.", testTag = "library_sound_preview_error")
        }
        Box(modifier = Modifier.weight(1f)) {
            when {
                state.loading && state.sounds.isEmpty() -> UsLoadingState(label = "Loading sounds")
                state.error != null -> UsErrorState(message = state.error, onRetry = actions.onRetry)
                state.sounds.isEmpty() -> UsEmptyState(
                    title = "No sounds yet",
                    detail = if (state.genre.isNotEmpty()) {
                        "Nothing in this genre. Choose All to see every sound."
                    } else {
                        "The sound library is empty for now."
                    },
                )
                else -> LazyColumn(
                    contentPadding = contentPadding,
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(state.sounds, key = { it.id }) { sound ->
                        LibrarySoundRow(
                            sound = sound,
                            preview = if (state.previewingId == sound.id) preview else SoundPreview.IDLE,
                            using = state.usingId == sound.id,
                            useEnabled = state.usingId == null,
                            useLabel = useLabel,
                            onTogglePreview = { actions.onTogglePreview(sound) },
                            onUse = { actions.onUse(sound) },
                        )
                    }
                    if (state.hasMore || state.moreFailed) {
                        item(key = "more") {
                            Box(
                                contentAlignment = Alignment.Center,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = UsTheme.spacing.l),
                            ) {
                                UsPillButton(
                                    text = if (state.moreFailed) "Try again" else "Load more",
                                    onClick = actions.onLoadMore,
                                    filled = false,
                                    busy = state.loadingMore,
                                    modifier = Modifier.testTag("library_sounds_more"),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun GenrePills(genres: List<String>, selected: String, onGenre: (String) -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xs)
            .testTag("library_sound_genres"),
    ) {
        (listOf("") + genres).forEach { genre ->
            val label = if (genre.isEmpty()) "All" else genreLabel(genre)
            UsPillButton(
                text = label,
                onClick = { onGenre(genre) },
                filled = genre.equals(selected, ignoreCase = true),
                modifier = Modifier.testTag("library_sound_genre:$label"),
            )
        }
    }
}

@Composable
private fun Notice(text: String, testTag: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.statusDanger,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .semantics { liveRegion = LiveRegionMode.Polite }
            .testTag(testTag),
    )
}

/** One library row: preview, what it is, how often it is used, and "Use". */
@Suppress("LongParameterList") // the row's facts and its two actions, each the caller's
@Composable
private fun LibrarySoundRow(
    sound: LibrarySound,
    preview: SoundPreview,
    using: Boolean,
    useEnabled: Boolean,
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
            .testTag("library_sound:${sound.id}"),
    ) {
        if (sound.playable) {
            SoundPreviewButton(preview = preview, onClick = onTogglePreview, size = PREVIEW_BUTTON)
        } else {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(PREVIEW_BUTTON)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(UsTheme.extended.bgCard)
                    .semantics { contentDescription = "No preview for this sound" },
            ) {
                Icon(
                    imageVector = UsIcons.Music,
                    contentDescription = null,
                    tint = UsTheme.extended.textMuted,
                    modifier = Modifier.size(GLYPH),
                )
            }
        }
        Column(modifier = Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = sound.title,
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (sound.isTrending) {
                    Spacer(Modifier.width(UsTheme.spacing.s))
                    Text(
                        text = "Popular",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.accentStrong,
                    )
                }
            }
            val meta = librarySoundMeta(sound)
            if (meta.isNotEmpty()) {
                Text(
                    text = meta,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            val usage = libraryUsageLabel(sound.usageCount)
            if (usage.isNotEmpty()) {
                Text(
                    text = usage,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textDim,
                )
            }
        }
        UsPillButton(
            text = useLabel,
            onClick = onUse,
            enabled = useEnabled,
            busy = using,
            modifier = Modifier
                .semantics { contentDescription = "$useLabel: ${sound.title}" }
                .testTag("library_sound_use:${sound.id}"),
        )
    }
}

/** The preview pauses when the surface stops, and is forgotten when the list leaves the composition. */
@Composable
private fun PreviewFollowsList(viewModel: LibrarySoundsViewModel) {
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

@Preview
@Composable
private fun LibrarySoundsPreview() {
    UsTheme {
        LibrarySoundsContent(
            state = LibrarySoundsUiState(
                loading = false,
                genres = listOf("pop", "lo-fi"),
                sounds = listOf(
                    LibrarySound("l1", "Morning run", "Studio", 28_000L, "pop", "upbeat", 1_200L, true, null, true),
                    LibrarySound("l2", "Quiet hours", "", 61_000L, "lo-fi", "", 0L, false, "t2", false),
                ),
                previewingId = "l1",
                nextOffset = 20,
            ),
            preview = SoundPreview.PLAYING,
            actions = LibrarySoundsActions({}, {}, {}, {}, {}),
        )
    }
}

private val SHEET_RADIUS = 28.dp
private val PREVIEW_BUTTON = 40.dp
private val GLYPH = 18.dp
private const val SHEET_HEIGHT_FRACTION = 0.85f
