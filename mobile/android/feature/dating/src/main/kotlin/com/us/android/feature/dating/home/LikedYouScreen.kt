package com.us.android.feature.dating.home

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.rememberProfileOptions
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.travel.VisitingMark
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone

/**
 * The Sparks tab: who sparked you, as a grid (mechanic M4).
 *
 * Unlocked, a tile shows the person and opens a sheet with their card, Spark
 * back and Decline — the same decision the incoming list offered. Locked, a
 * tile shows only the server-blurred image, a Super Spark star and a lock, and
 * every tap leads to the Premium upsell; there is no decline, because there is
 * no one to decide about.
 */
@Composable
internal fun LikedYouGrid(
    viewModel: LikedYouViewModel,
    onOpenPerson: (userId: String) -> Unit,
    onOpenPremium: () -> Unit,
) {
    // Shown again — back from Premium, say, where a pass may have landed.
    LaunchedEffect(Unit) { viewModel.refresh() }
    val state by viewModel.state.collectAsStateWithLifecycle()
    val upsell by viewModel.upsell.collectAsStateWithLifecycle()
    val busy by viewModel.busy.collectAsStateWithLifecycle()
    var selectedId by remember { mutableStateOf<String?>(null) }
    var reporting by remember { mutableStateOf<IncomingSparkUi?>(null) }

    when (val s = state) {
        LikedYouState.Loading -> LoadingPane()
        is LikedYouState.Failed -> MessagePane(title = "Sparks didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is LikedYouState.Loaded -> if (s.tiles.isEmpty() && s.total == 0) {
            MessagePane(title = LikedYouCopy.EMPTY_TITLE, body = LikedYouCopy.EMPTY_BODY, icon = UsIcons.HeartOutline)
        } else {
            Grid(
                state = s,
                onTile = { tile ->
                    when (tile) {
                        is LikedYouTile.Locked -> viewModel.showUpsell()
                        is LikedYouTile.Open -> selectedId = tile.sparkId
                    }
                },
                onUnlock = viewModel::showUpsell,
                onEndReached = viewModel::loadMore,
            )
        }
    }

    // The sheet follows the tile by id: once it is accepted, declined or locked, the sheet closes.
    val selected = (state as? LikedYouState.Loaded)?.tiles
        ?.firstOrNull { it.sparkId == selectedId } as? LikedYouTile.Open
    if (selected != null) {
        SparkSheet(
            tile = selected,
            busy = busy != null,
            onAccept = { viewModel.accept(selected) },
            onDecline = { viewModel.decline(selected) },
            onOpenProfile = {
                selectedId = null
                onOpenPerson(selected.spark.fromUserId)
            },
            onReport = {
                selectedId = null
                reporting = selected.spark
            },
            onDismiss = { selectedId = null },
        )
    }
    if (upsell) {
        ConfirmDialog(
            title = LikedYouCopy.UPSELL_TITLE,
            body = LikedYouCopy.UPSELL_BODY,
            confirmLabel = LikedYouCopy.LOCKED_CTA_ACTION,
            dismissLabel = "Not now",
            onConfirm = {
                viewModel.dismissUpsell()
                onOpenPremium()
            },
            onDismiss = viewModel::dismissUpsell,
        )
    }
    reporting?.let { spark ->
        ReportSheet(
            initial = ReportDraft(targetId = spark.fromUserId, sparkIds = listOf(spark.sparkId)),
            name = spark.name,
            onSubmit = {
                viewModel.report(it)
                reporting = null
            },
            onDismiss = { reporting = null },
        )
    }
}

@Composable
private fun Grid(
    state: LikedYouState.Loaded,
    onTile: (LikedYouTile) -> Unit,
    onUnlock: () -> Unit,
    onEndReached: () -> Unit,
) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val columns = if (maxWidth >= WIDE_SCREEN) WIDE_COLUMNS else COLUMNS
        val gap = UsTheme.spacing.m
        LazyVerticalGrid(
            columns = GridCells.Fixed(columns),
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
            horizontalArrangement = Arrangement.spacedBy(gap),
            verticalArrangement = Arrangement.spacedBy(gap),
            modifier = Modifier.fillMaxSize(),
        ) {
            item(key = "header", span = { GridItemSpan(maxLineSpan) }) {
                Header(state, onUnlock)
            }
            itemsIndexed(state.tiles, key = { _, tile -> tile.sparkId }) { index, tile ->
                if (index == state.tiles.lastIndex && state.canLoadMore) {
                    LaunchedEffect(tile.sparkId) { onEndReached() }
                }
                when (tile) {
                    is LikedYouTile.Locked -> LockedTile(tile, onClick = { onTile(tile) })
                    is LikedYouTile.Open -> OpenTile(tile, onClick = { onTile(tile) })
                }
            }
            if (state.loadingMore) {
                item(key = "more", span = { GridItemSpan(maxLineSpan) }) {
                    Box(Modifier.fillMaxWidth().padding(UsTheme.spacing.l), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator(color = UsTheme.extended.accentSolid, strokeWidth = 3.dp, modifier = Modifier.size(24.dp))
                    }
                }
            }
        }
    }
}

/** The count, and — while locked — the call to action that leads to Premium. */
@Composable
private fun Header(state: LikedYouState.Loaded, onUnlock: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), modifier = Modifier.padding(bottom = UsTheme.spacing.xs)) {
        LikedYouCopy.header(state.total)?.let {
            Text(it, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
        }
        if (!state.unlocked) {
            val shape = RoundedCornerShape(UsTheme.radii.large)
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(shape)
                    .background(UsTheme.extended.bgCardSolid)
                    .border(1.dp, UsTheme.extended.borderSubtle, shape)
                    .padding(UsTheme.spacing.xl),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    Icon(UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(20.dp))
                    Text(
                        LikedYouCopy.LOCKED_CTA_TITLE,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = UsTheme.extended.textPrimary,
                    )
                }
                Text(LikedYouCopy.LOCKED_CTA_BODY, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                UsButton(text = LikedYouCopy.LOCKED_CTA_ACTION, onClick = onUnlock, modifier = Modifier.fillMaxWidth())
            }
        }
    }
}

/** A tile's frame: the photo fills it, and the overlays sit on top. */
@Composable
private fun TileFrame(
    photoUrl: String?,
    description: String,
    onClick: () -> Unit,
    overlay: @Composable () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(TILE_RATIO)
            .clip(shape)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .clickable(onClick = onClick)
            .semantics(mergeDescendants = true) { contentDescription = description },
    ) {
        DatingPhoto(url = photoUrl, contentDescription = null, modifier = Modifier.fillMaxSize())
        overlay()
    }
}

/** A blurred image the SERVER blurred, a Super Spark star when there is one, and a lock. Nothing else. */
@Composable
private fun LockedTile(tile: LikedYouTile.Locked, onClick: () -> Unit) {
    val description = if (tile.superSpark) "Someone sent you a Super Spark. Locked." else "Someone sparked you. Locked."
    TileFrame(photoUrl = tile.photoUrl, description = description, onClick = onClick) {
        Box(Modifier.fillMaxSize().padding(UsTheme.spacing.m)) {
            if (tile.superSpark) SuperStar(Modifier.align(Alignment.TopStart))
            Row(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .background(UsTheme.extended.bgCanvas.copy(alpha = SCRIM_ALPHA), RoundedCornerShape(UsTheme.radii.full))
                    .padding(horizontal = 10.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Icon(UsIcons.Lock, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(12.dp))
                Text(
                    LikedYouCopy.LOCKED_PILL,
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

/** The person's photo, with their name and age along the bottom. */
@Composable
private fun OpenTile(tile: LikedYouTile.Open, onClick: () -> Unit) {
    val spark = tile.spark
    val line = personLine(spark.name, spark.age) ?: "Someone sparked you"
    val description = listOfNotNull(line, SUPER_SPARK_MARK.takeIf { spark.superSpark }, spark.visiting).joinToString(". ")
    TileFrame(photoUrl = spark.photoUrl, description = description, onClick = onClick) {
        Box(Modifier.fillMaxSize()) {
            if (spark.superSpark) SuperStar(Modifier.align(Alignment.TopStart).padding(UsTheme.spacing.m))
            // Mechanic M8: on a trip. Kept clear of the star in the other corner.
            spark.visiting?.let {
                VisitingMark(
                    it,
                    onPhoto = true,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(top = UsTheme.spacing.m, end = UsTheme.spacing.m, start = VISITING_START),
                )
            }
            Row(
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .background(UsTheme.extended.bgCanvas.copy(alpha = SCRIM_ALPHA))
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(
                    line,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
            }
        }
    }
}

/** The Super Spark star on a tile, in the warm status colour the incoming row uses. */
@Composable
private fun SuperStar(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .size(28.dp)
            .background(UsTheme.extended.bgCanvas.copy(alpha = SCRIM_ALPHA), CircleShape),
        contentAlignment = Alignment.Center,
    ) {
        Icon(UsIcons.Star, contentDescription = null, tint = UsTheme.extended.statusWarning, modifier = Modifier.size(16.dp))
    }
}

/** An open tile's card: the same pre-match detail the deck shows, then Decline or Spark back. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SparkSheet(
    tile: LikedYouTile.Open,
    busy: Boolean,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
    onOpenProfile: () -> Unit,
    onReport: () -> Unit,
    onDismiss: () -> Unit,
) {
    val spark = tile.spark
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgRaised,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding()
                .padding(bottom = UsTheme.spacing.l),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            PersonGallery(
                photos = spark.detail?.gallery.orEmpty(),
                fallbackUrl = spark.photoUrl,
                name = spark.name,
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(PHOTO_RATIO)
                    .clip(RoundedCornerShape(UsTheme.radii.card)),
            )
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text(
                    personLine(spark.name, spark.age) ?: "Someone sparked you",
                    style = MaterialTheme.typography.headlineSmall,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (spark.verified) Pill("Verified", Tone.Positive)
                IconButton(onClick = onReport) { Icon(UsIcons.Flag, contentDescription = "Report", tint = UsTheme.extended.textMuted) }
            }
            if (spark.superSpark) SuperSparkMark()
            spark.visiting?.let { VisitingMark(it) }
            val about = listOfNotNull(spark.city, spark.intent, spark.distance).joinToString(" · ")
            if (about.isNotBlank()) {
                Text(about, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
            }
            spark.note?.let { SparkNote(sparkId = spark.sparkId, note = it, hidden = spark.noteHidden) }
            PersonDetailBody(spark.detail, rememberProfileOptions())
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), modifier = Modifier.padding(top = UsTheme.spacing.m)) {
                UsSecondaryButton(text = "Decline", enabled = !busy, onClick = onDecline, modifier = Modifier.weight(1f))
                UsButton(text = "Spark back", enabled = !busy, loading = busy, onClick = onAccept, modifier = Modifier.weight(1f))
            }
            TextButton(onClick = onOpenProfile, modifier = Modifier.align(Alignment.CenterHorizontally)) {
                Text("View full profile", color = UsTheme.extended.accentSolid)
            }
        }
    }
}

/** The spark note words (mechanic M13). Our own. */
object SparkNoteCopy {
    const val FOLDED = "Their note is folded away"
    const val TAP_TO_READ = "Tap to read"
    const val FOLD_AGAIN = "Fold away"
}

/**
 * A spark's note. One the viewer's comment filter hides arrives with a reason
 * and shows folded — the reason and "Tap to read" — until tapped; it can be
 * folded again. An ordinary note shows as it always has.
 */
@Composable
internal fun SparkNote(sparkId: String, note: String, hidden: NoteHidden?) {
    var open by rememberSaveable(sparkId) { mutableStateOf(false) }
    if (hidden == null) {
        Text("“$note”", style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textSecondary)
        return
    }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.card))
            .background(UsTheme.extended.bgCardSolid)
            .clickable { open = !open }
            .padding(UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        if (open) {
            Text("“$note”", style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textSecondary)
            Text(SparkNoteCopy.FOLD_AGAIN, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.accentSolid)
        } else {
            Text(SparkNoteCopy.FOLDED, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
            Text(hidden.reason, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            Text(SparkNoteCopy.TAP_TO_READ, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.accentSolid)
        }
    }
}

private val WIDE_SCREEN = 600.dp
private val VISITING_START = 44.dp
private const val COLUMNS = 2
private const val WIDE_COLUMNS = 3
private const val TILE_RATIO = 0.78f
private const val SCRIM_ALPHA = 0.72f
