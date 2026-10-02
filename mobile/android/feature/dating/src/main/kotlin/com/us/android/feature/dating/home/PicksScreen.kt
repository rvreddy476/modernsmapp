package com.us.android.feature.dating.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.ProfileOptionsUi
import com.us.android.feature.dating.profile.rememberProfileOptions
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.travel.VisitingMark
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import kotlinx.coroutines.delay
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/*
 * The Picks tab (mechanic M7): today's few, as a scrolling list of deck-style
 * cards with Pass and Spark under each.
 *
 * A pick's full profile opens in a sheet built from the card itself: the card
 * already carries the whole pre-match block, and `GET /people/:id` does not
 * serve picks (they are kept apart from the deck), so the profile route would
 * only say "not available".
 */

@Composable
internal fun PicksTab(viewModel: PicksViewModel) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val busy by viewModel.busy.collectAsStateWithLifecycle()
    val options = rememberProfileOptions()
    var openId by remember { mutableStateOf<String?>(null) }
    var reporting by remember { mutableStateOf<CardUi?>(null) }
    var blocking by remember { mutableStateOf<CardUi?>(null) }

    // Shown again after midnight: the day has turned and a new set is waiting.
    LaunchedEffect(Unit) { viewModel.refreshIfStale() }

    when (val s = state) {
        PicksState.Loading, PicksState.Hidden -> LoadingPane()
        is PicksState.Failed -> MessagePane(title = PicksCopy.LOAD_FAILED, body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is PicksState.Loaded -> {
            // Still open at midnight: fetch the new set when it is due.
            LaunchedEffect(s.resetsAt) {
                val wait = s.resetsAt?.let { Duration.between(Instant.now(), it).toMillis() } ?: return@LaunchedEffect
                if (wait > 0) {
                    delay(wait + MIDNIGHT_GRACE_MILLIS)
                    viewModel.refresh()
                }
            }
            val zone = remember { ZoneId.systemDefault() }
            if (s.cards.isEmpty()) {
                MessagePane(
                    title = PicksCopy.EMPTY_TITLE,
                    body = remember(s.resetsAt) { PicksCopy.emptyBody(s.resetsAt, Instant.now(), zone) },
                    icon = UsIcons.Star,
                    secondaryLabel = "Refresh",
                    onSecondary = viewModel::refresh,
                )
            } else {
                PicksList(
                    state = s,
                    zone = zone,
                    busy = busy,
                    options = options,
                    onOpen = { openId = it.userId },
                    onPass = { viewModel.pass(it.userId) },
                    onSpark = { viewModel.spark(it.userId) },
                )
            }
        }
    }

    // The sheet follows the pick by id: once it is acted on, it closes.
    val open = (state as? PicksState.Loaded)?.cards?.firstOrNull { it.userId == openId }
    if (open != null) {
        PickSheet(
            card = open,
            busy = busy != null,
            options = options,
            onPass = { viewModel.pass(open.userId) },
            onSpark = { viewModel.spark(open.userId) },
            onReport = {
                openId = null
                reporting = open
            },
            onBlock = {
                openId = null
                blocking = open
            },
            onDismiss = { openId = null },
        )
    }
    reporting?.let { card ->
        ReportSheet(
            initial = ReportDraft(targetId = card.userId, photoIds = listOfNotNull(card.photoId)),
            name = card.name,
            onSubmit = {
                viewModel.report(it)
                reporting = null
            },
            onDismiss = { reporting = null },
        )
    }
    blocking?.let { card ->
        ConfirmDialog(
            title = "Block ${card.name}?",
            body = "You won't see each other in Pulse, picks, sparks or matches again.",
            confirmLabel = "Block",
            destructive = true,
            onConfirm = {
                viewModel.block(card.userId)
                blocking = null
            },
            onDismiss = { blocking = null },
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun PicksList(
    state: PicksState.Loaded,
    zone: ZoneId,
    busy: String?,
    options: ProfileOptionsUi?,
    onOpen: (CardUi) -> Unit,
    onPass: (CardUi) -> Unit,
    onSpark: (CardUi) -> Unit,
) {
    LazyColumn(
        contentPadding = PaddingValues(vertical = UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        item(key = "header") {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(PicksCopy.TITLE, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
                Text(
                    remember(state.resetsAt) { PicksCopy.resetLine(state.resetsAt, Instant.now(), zone) },
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.accentSolid,
                )
                Text(PicksCopy.BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        items(state.cards, key = { it.userId }) { card ->
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                CardFace(
                    card = card,
                    photoUrl = card.photos().first(),
                    interests = card.glanceInterests(options),
                    onOpen = { onOpen(card) },
                    modifier = Modifier.fillMaxWidth().aspectRatio(CARD_RATIO),
                )
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    UsSecondaryButton(text = "Pass", enabled = busy == null, onClick = { onPass(card) }, modifier = Modifier.weight(1f))
                    UsButton(
                        text = "Spark",
                        enabled = busy == null || busy == card.userId,
                        loading = busy == card.userId,
                        onClick = { onSpark(card) },
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

/** A pick in full, from its own card: the gallery, the readable half, and the same two choices. */
@OptIn(ExperimentalMaterial3Api::class)
@Suppress("LongParameterList", "LongMethod")
@Composable
private fun PickSheet(
    card: CardUi,
    busy: Boolean,
    options: ProfileOptionsUi?,
    onPass: () -> Unit,
    onSpark: () -> Unit,
    onReport: () -> Unit,
    onBlock: () -> Unit,
    onDismiss: () -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
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
            Box {
                PersonGallery(
                    photos = card.detail?.gallery.orEmpty(),
                    fallbackUrl = card.photoUrl,
                    name = card.name,
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(PHOTO_RATIO)
                        .clip(RoundedCornerShape(UsTheme.radii.card)),
                )
                Box(Modifier.align(Alignment.TopEnd)) {
                    IconButton(
                        onClick = { menu = true },
                        modifier = Modifier.padding(UsTheme.spacing.s).background(UsTheme.extended.bgCanvas.copy(alpha = 0.7f), CircleShape),
                    ) {
                        Icon(UsIcons.More, contentDescription = "More", tint = UsTheme.extended.textPrimary)
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text("Report") }, onClick = { menu = false; onReport() })
                        DropdownMenuItem(text = { Text("Block") }, onClick = { menu = false; onBlock() })
                    }
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text(
                    personLine(card.name, card.age) ?: "Today's pick",
                    style = MaterialTheme.typography.headlineSmall,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (card.verified) Pill("Verified", Tone.Positive)
            }
            card.visiting?.let { VisitingMark(it) }
            val place = listOfNotNull(card.city.takeIf { it.isNotBlank() }, card.distance).joinToString(" · ")
            if (place.isNotBlank()) Text(place, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
            card.lastActive?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
            PersonDetailBody(card.detail, options)
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), modifier = Modifier.padding(top = UsTheme.spacing.m)) {
                UsSecondaryButton(text = "Pass", enabled = !busy, onClick = onPass, modifier = Modifier.weight(1f))
                UsButton(text = "Spark", enabled = !busy, loading = busy, onClick = onSpark, modifier = Modifier.weight(1f))
            }
        }
    }
}

private const val CARD_RATIO = 0.8f
private const val MIDNIGHT_GRACE_MILLIS = 2_000L
