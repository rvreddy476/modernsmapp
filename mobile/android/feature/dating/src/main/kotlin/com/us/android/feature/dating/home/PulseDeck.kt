package com.us.android.feature.dating.home

import android.provider.Settings
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.AnimationVector2D
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.VectorConverter
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.ProfileOptionsUi
import com.us.android.feature.dating.profile.rememberProfileOptions
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportSheet
import com.us.android.feature.dating.travel.TripBanner
import com.us.android.feature.dating.travel.VisitingMark
import com.us.android.feature.dating.ui.ConfirmDialog
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.LabelChips
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId

/*
 * The Pulse deck (mechanic M1): a stack of cards, the top one draggable.
 *
 *   right past the threshold   spark
 *   left past the threshold    pass
 *   up past the threshold      Super Spark — only while `GET /allowances`
 *                              names the mechanic; otherwise an upward drag
 *                              is resisted and springs back
 *   Undo pass (above the card) takes back the last pass, while that pass is
 *                              still the last action and the mechanic is on
 *   tap the photo's halves     previous / next photo
 *   tap the name panel         the full profile (bio, prompts, languages)
 *
 * The drag belongs to the CARD and the gallery is paged by taps, so the two
 * never compete for the same horizontal movement. Pass, Save for later and
 * Spark stay as buttons, and the card carries the same actions for TalkBack.
 *
 * The card is taken off the deck by the SERVER, not by the gesture: a release
 * past the threshold asks the view model, which marks the card as leaving; the
 * card flies out, and comes back if the server refuses.
 */

/**
 * The Pulse tab. [onOpenPremium] is where a spent undo or Super Spark allowance
 * leads; [onOpenTravel] is where the trip banner (mechanic M8) leads.
 */
@Composable
internal fun PulseDeck(
    viewModel: PulseViewModel,
    onOpenPerson: (userId: String) -> Unit,
    onOpenPremium: () -> Unit,
    onOpenTravel: () -> Unit = {},
) {
    val deck by viewModel.deck.collectAsStateWithLifecycle()
    Column(Modifier.fillMaxSize()) {
        // While travelling, the deck says whose city it is showing.
        deck.trip?.let { TripBanner(it, onClick = onOpenTravel, modifier = Modifier.padding(top = UsTheme.spacing.m)) }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            DeckBody(viewModel, onOpenPerson, onOpenPremium)
        }
    }
}

@Suppress("LongMethod")
@Composable
private fun DeckBody(viewModel: PulseViewModel, onOpenPerson: (userId: String) -> Unit, onOpenPremium: () -> Unit) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val deck by viewModel.deck.collectAsStateWithLifecycle()
    val busy by viewModel.busy.collectAsStateWithLifecycle()
    var reporting by remember { mutableStateOf<CardUi?>(null) }
    var blocking by remember { mutableStateOf<CardUi?>(null) }
    val options = rememberProfileOptions()

    // Shown again — back from Premium, say, where a pack or a pass may have
    // landed: the allowances are read afresh.
    LaunchedEffect(Unit) { viewModel.refreshAllowances() }

    // Undo stays reachable when the pass emptied the stack.
    val undo: @Composable ColumnScope.() -> Unit = {
        if (deck.canRewind) {
            UndoControl(deck, enabled = busy == null, onUndo = { viewModel.rewind() }, modifier = Modifier.padding(top = UsTheme.spacing.l))
        }
    }

    when (val s = state) {
        ListState.Loading -> LoadingPane()
        is ListState.Failed -> MessagePane(title = "Pulse didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::refresh)
        is ListState.Items -> {
            val sparkLimit = deck.sparkLimit
            val superSparkLimit = deck.superSparkLimit
            val rewindLimit = deck.rewindLimit
            when {
                sparkLimit != null -> OutOfSparksPane(sparkLimit, onDismiss = viewModel::dismissSparkLimit)
                superSparkLimit != null -> OutOfSuperSparksPane(
                    limit = superSparkLimit,
                    packs = deck.superSparkBalance,
                    onGetMore = {
                        viewModel.dismissSuperSparkLimit()
                        onOpenPremium()
                    },
                    onDismiss = viewModel::dismissSuperSparkLimit,
                )
                rewindLimit != null -> OutOfUndosPane(
                    limit = rewindLimit,
                    onPremium = {
                        viewModel.dismissRewindLimit()
                        onOpenPremium()
                    },
                    onDismiss = viewModel::dismissRewindLimit,
                )
                s.items.isNotEmpty() -> DeckStack(
                    items = s.items,
                    deck = deck,
                    busy = busy,
                    options = options,
                    viewModel = viewModel,
                    onOpenPerson = onOpenPerson,
                    onReport = { reporting = it },
                    onBlock = { blocking = it },
                )
                s.gated -> MessagePane(
                    title = "Pulse isn't ready for you yet",
                    body = "We're opening Pulse gradually. Check back soon.",
                    icon = UsIcons.Compass,
                    secondaryLabel = "Refresh",
                    onSecondary = viewModel::refresh,
                )
                deck.refilling -> LoadingPane(label = "Finding more people")
                deck.outOfCards -> OutOfCardsPane(deck, onRefresh = viewModel::refresh, extra = undo)
                else -> MessagePane(
                    title = "You're all caught up",
                    body = "New people show up every day.",
                    icon = UsIcons.Compass,
                    secondaryLabel = "Refresh",
                    onSecondary = viewModel::refresh,
                    extra = undo,
                )
            }
        }
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
            body = "You won't see each other in Pulse, sparks or matches again.",
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

/** The daily allowance is spent. The time is the viewer's own clock; a time already behind us is not shown. */
@Composable
private fun OutOfCardsPane(deck: DeckUi, onRefresh: () -> Unit, extra: @Composable ColumnScope.() -> Unit) {
    val body = remember(deck) { DeckCopy.outOfCardsBody(deck, Instant.now(), ZoneId.systemDefault()) }
    MessagePane(
        title = DeckCopy.OUT_OF_CARDS_TITLE,
        body = body,
        icon = UsIcons.Clock,
        secondaryLabel = "Check again",
        onSecondary = onRefresh,
        extra = extra,
    )
}

/** `SUPER_SPARK_LIMIT_REACHED`: the daily allowance and every pack Super Spark are used. A pack is bought in Premium. */
@Composable
private fun OutOfSuperSparksPane(limit: SparkLimitUi, packs: Int, onGetMore: () -> Unit, onDismiss: () -> Unit) {
    val body = remember(limit, packs) { DeckCopy.outOfSuperSparksBody(limit, packs, Instant.now(), ZoneId.systemDefault()) }
    MessagePane(
        title = DeckCopy.OUT_OF_SUPER_SPARKS_TITLE,
        body = body,
        icon = UsIcons.Star,
        iconTint = UsTheme.extended.statusWarning,
        primaryLabel = "Get Super Sparks",
        onPrimary = onGetMore,
        secondaryLabel = "Keep browsing",
        onSecondary = onDismiss,
    )
}

/** `REWIND_LIMIT_REACHED`. A pass lifts the limit, so Premium is offered; the deck still works. */
@Composable
private fun OutOfUndosPane(limit: SparkLimitUi, onPremium: () -> Unit, onDismiss: () -> Unit) {
    val body = remember(limit) { DeckCopy.outOfUndosBody(limit, Instant.now(), ZoneId.systemDefault()) }
    MessagePane(
        title = DeckCopy.OUT_OF_UNDOS_TITLE,
        body = body,
        icon = UsIcons.RotateCcw,
        primaryLabel = "See Premium",
        onPrimary = onPremium,
        secondaryLabel = "Keep browsing",
        onSecondary = onDismiss,
    )
}

/**
 * Undo the last pass: shown only while [DeckUi.canRewind], with what is left
 * of the allowance beside it in a quieter voice.
 */
@Composable
private fun UndoControl(deck: DeckUi, enabled: Boolean, onUndo: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val left = DeckCopy.undosLeft(deck)
    Row(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.bgRaised, shape)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .clickable(enabled = enabled, onClickLabel = DeckCopy.UNDO, role = Role.Button, onClick = onUndo)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics(mergeDescendants = true) { },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(UsIcons.RotateCcw, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(16.dp))
        Text(DeckCopy.UNDO, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary)
        left?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted) }
    }
}

/** `SPARK_RATE_LIMITED`. Passing and saving still work, so the way out is back to the deck. */
@Composable
private fun OutOfSparksPane(limit: SparkLimitUi, onDismiss: () -> Unit) {
    val body = remember(limit) { DeckCopy.outOfSparksBody(limit, Instant.now(), ZoneId.systemDefault()) }
    MessagePane(
        title = DeckCopy.OUT_OF_SPARKS_TITLE,
        body = body,
        icon = UsIcons.HeartOutline,
        primaryLabel = "Keep browsing",
        onPrimary = onDismiss,
    )
}

@Suppress("LongParameterList")
@Composable
private fun DeckStack(
    items: List<CardUi>,
    deck: DeckUi,
    busy: String?,
    options: ProfileOptionsUi?,
    viewModel: PulseViewModel,
    onOpenPerson: (String) -> Unit,
    onReport: (CardUi) -> Unit,
    onBlock: (CardUi) -> Unit,
) {
    val top = items.first()
    val next = items.getOrNull(1)
    val idle = busy == null
    val offset = remember(top.userId) { Animatable(Offset.Zero, Offset.VectorConverter) }
    var cardSize by remember { mutableStateOf(IntSize.Zero) }

    Column(
        // The scaffold already keeps the page gutter; the card sits inside it.
        modifier = Modifier.fillMaxSize().padding(vertical = UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        val cardsLeft = DeckCopy.cardsLeft(deck)
        if (cardsLeft != null || deck.canRewind) {
            Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                if (deck.canRewind) UndoControl(deck, enabled = idle, onUndo = { viewModel.rewind() })
                Text(
                    text = cardsLeft.orEmpty(),
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textMuted,
                    textAlign = if (deck.canRewind) TextAlign.End else TextAlign.Center,
                    modifier = Modifier.weight(1f),
                )
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth().onSizeChanged { cardSize = it }) {
            if (next != null) {
                // The card underneath: a still preview that grows into place as
                // the top one is dragged away. Not interactive, and silent.
                key(next.userId) {
                    CardFace(
                        card = next,
                        photoUrl = next.photos().first(),
                        interests = next.glanceInterests(options),
                        modifier = Modifier
                            .fillMaxSize()
                            .graphicsLayer {
                                val width = cardSize.width.toFloat()
                                val gone = if (width > 0f) (offset.value.getDistance() / width).coerceIn(0f, 1f) else 0f
                                val scale = PREVIEW_SCALE + (1f - PREVIEW_SCALE) * gone
                                scaleX = scale
                                scaleY = scale
                            }
                            .clearAndSetSemantics { },
                    )
                }
            }
            key(top.userId) {
                SwipeCard(
                    card = top,
                    interests = top.glanceInterests(options),
                    offset = offset,
                    size = cardSize,
                    leaving = deck.leaving?.takeIf { it.userId == top.userId }?.exit,
                    enabled = idle,
                    superSparkEnabled = deck.superSparkEnabled,
                    onSwipe = { direction ->
                        when (direction) {
                            SwipeDirection.RIGHT -> viewModel.spark(top.userId)
                            SwipeDirection.LEFT -> viewModel.pass(top.userId)
                            SwipeDirection.UP -> viewModel.superSpark(top.userId)
                        }
                    },
                    onStash = { viewModel.stash(top.userId) },
                    onOpen = { onOpenPerson(top.userId) },
                    onReport = { onReport(top) },
                    onBlock = { onBlock(top) },
                )
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m), verticalAlignment = Alignment.CenterVertically) {
            UsSecondaryButton(text = "Pass", enabled = idle, onClick = { viewModel.pass(top.userId) }, modifier = Modifier.weight(1f))
            IconButton(onClick = { viewModel.stash(top.userId) }, enabled = idle) {
                Icon(UsIcons.BookmarkOutline, contentDescription = "Save for later", tint = UsTheme.extended.textPrimary)
            }
            if (deck.superSparkEnabled) {
                IconButton(onClick = { viewModel.superSpark(top.userId) }, enabled = idle) {
                    Icon(UsIcons.Star, contentDescription = SUPER_SPARK, tint = UsTheme.extended.statusWarning)
                }
            }
            UsButton(
                text = "Spark",
                enabled = idle || busy == top.userId,
                loading = busy == top.userId && deck.leaving?.exit == DeckExit.SPARK,
                onClick = { viewModel.spark(top.userId) },
                modifier = Modifier.weight(1f),
            )
        }
        DeckCopy.superSparksLeft(deck)?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
                textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/**
 * The top card: draggable, and driven off or back by [leaving].
 *
 * The drag handler sits OUTSIDE the layer that moves the card, so the pointer
 * is measured against a node that stays put. [onSwipe] answers whether the
 * action was started; when it was not, the card returns on its own.
 */
@Suppress("LongParameterList", "LongMethod")
@Composable
private fun SwipeCard(
    card: CardUi,
    interests: List<String>,
    offset: Animatable<Offset, AnimationVector2D>,
    size: IntSize,
    leaving: DeckExit?,
    enabled: Boolean,
    superSparkEnabled: Boolean,
    onSwipe: (SwipeDirection) -> Boolean,
    onStash: () -> Unit,
    onOpen: () -> Unit,
    onReport: () -> Unit,
    onBlock: () -> Unit,
) {
    val scope = rememberCoroutineScope()
    val reducedMotion = rememberReducedMotion()
    val flingVelocity = with(LocalDensity.current) { FLING_VELOCITY.toPx() }
    val decider = remember(size, flingVelocity, superSparkEnabled) {
        SwipeDecider(size.width.toFloat(), size.height.toFloat(), flingVelocity, superSparkEnabled)
    }
    val photos = remember(card) { card.photos() }
    var photo by rememberSaveable { mutableIntStateOf(0) }
    val shown = photo.coerceIn(0, (photos.size - 1).coerceAtLeast(0))
    var menu by remember { mutableStateOf(false) }

    suspend fun settle(target: Offset, leaving: Boolean) {
        when {
            offset.value == target -> Unit
            reducedMotion -> offset.snapTo(target)
            leaving -> offset.animateTo(target, tween(EXIT_MILLIS))
            else -> offset.animateTo(target, spring(dampingRatio = Spring.DampingRatioLowBouncy, stiffness = Spring.StiffnessMediumLow))
        }
    }

    // The server's word: an action in flight sends the card out, a refusal
    // (leaving cleared with the card still here) brings it back.
    LaunchedEffect(leaving, size) {
        if (leaving == null) settle(Offset.Zero, leaving = false) else settle(exitTarget(leaving, offset.value, size), leaving = true)
    }

    val actions = buildList {
        add(CustomAccessibilityAction("Spark") { onSwipe(SwipeDirection.RIGHT) })
        add(CustomAccessibilityAction("Pass") { onSwipe(SwipeDirection.LEFT) })
        if (superSparkEnabled) add(CustomAccessibilityAction(SUPER_SPARK) { onSwipe(SwipeDirection.UP) })
        add(CustomAccessibilityAction("Save for later") { onStash(); true })
        add(CustomAccessibilityAction("View profile") { onOpen(); true })
    }
    val summary = listOfNotNull("${card.name}, ${card.age}", card.visiting, card.city.takeIf { it.isNotBlank() }, card.distance).joinToString(". ")

    Box(
        modifier = Modifier
            .fillMaxSize()
            .pointerInput(enabled, decider) {
                if (!enabled) return@pointerInput
                val tracker = VelocityTracker()
                detectDragGestures(
                    onDragStart = { tracker.resetTracking() },
                    onDrag = { change, drag ->
                        change.consume()
                        tracker.addPosition(change.uptimeMillis, change.position)
                        // Up is resisted while Super Spark is off: the card hints
                        // that it will not go that way.
                        val dy = if (superSparkEnabled || drag.y > 0f) drag.y else drag.y * UP_RESISTANCE
                        scope.launch { offset.snapTo(offset.value + Offset(drag.x, dy)) }
                    },
                    onDragEnd = {
                        val velocity = tracker.calculateVelocity()
                        val at = offset.value
                        val direction = decider.decide(at.x, at.y, velocity.x, velocity.y)
                        val started = direction != null && onSwipe(direction)
                        if (!started) scope.launch { settle(Offset.Zero, leaving = false) }
                    },
                    onDragCancel = { scope.launch { settle(Offset.Zero, leaving = false) } },
                )
            }
            .graphicsLayer {
                translationX = offset.value.x
                translationY = offset.value.y
                rotationZ = if (reducedMotion) 0f else decider.rotation(offset.value.x)
            }
            .semantics {
                contentDescription = summary
                customActions = actions
            },
    ) {
        CardFace(
            card = card,
            photoUrl = photos.getOrNull(shown),
            interests = interests,
            modifier = Modifier.fillMaxSize(),
            photoCount = photos.size,
            photoIndex = shown,
            onPrevious = { photo = (shown - 1).coerceAtLeast(0) },
            onNext = { photo = (shown + 1).coerceAtMost(photos.size - 1) },
            onOpen = onOpen,
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
        // One place for the hint, three labels: each fades in with its own
        // direction, read straight from the offset so a drag recomposes nothing.
        Box(Modifier.align(Alignment.TopCenter).padding(top = HINT_TOP)) {
            DragHint("Spark", UsIcons.HeartFilled, UsTheme.extended.accentSolid) { hintAlpha(decider, offset.value, SwipeDirection.RIGHT) }
            DragHint("Pass", UsIcons.Close, UsTheme.extended.textSecondary) { hintAlpha(decider, offset.value, SwipeDirection.LEFT) }
            if (superSparkEnabled) {
                DragHint(SUPER_SPARK, UsIcons.Star, UsTheme.extended.statusWarning) { hintAlpha(decider, offset.value, SwipeDirection.UP) }
            }
        }
    }
}

private fun hintAlpha(decider: SwipeDecider, offset: Offset, direction: SwipeDirection): Float =
    decider.hint(offset.x, offset.y)?.takeIf { it.direction == direction }?.progress ?: 0f

@Composable
private fun DragHint(text: String, icon: ImageVector, color: Color, alpha: () -> Float) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = Modifier
            .graphicsLayer { this.alpha = alpha() }
            .background(UsTheme.extended.bgCanvas.copy(alpha = 0.86f), shape)
            .border(2.dp, color, shape)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
            .clearAndSetSemantics { },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(icon, contentDescription = null, tint = color, modifier = Modifier.size(18.dp))
        Text(text, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = color)
    }
}

/**
 * What a card shows: one photo edge to edge, the gallery's place markers, and
 * the name panel. With [onOpen] null it is the still preview underneath.
 *
 * The bio, prompts and languages are NOT here: the card is for the glance, and
 * the name panel opens the full profile, where they are.
 */
@Suppress("LongParameterList", "LongMethod")
@Composable
internal fun CardFace(
    card: CardUi,
    photoUrl: String?,
    modifier: Modifier = Modifier,
    interests: List<String> = emptyList(),
    photoCount: Int = 0,
    photoIndex: Int = 0,
    onPrevious: () -> Unit = {},
    onNext: () -> Unit = {},
    onOpen: (() -> Unit)? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.card)
    Box(modifier.clip(shape).background(UsTheme.extended.bgCardSolid).border(1.dp, UsTheme.extended.borderSubtle, shape)) {
        DatingPhoto(
            url = photoUrl,
            contentDescription = if (photoCount > 1) "${card.name}'s photo, ${photoIndex + 1} of $photoCount" else "${card.name}'s photo",
            modifier = Modifier.fillMaxSize(),
        )
        if (onOpen != null) {
            if (photoCount > 1) {
                Row(Modifier.fillMaxSize()) {
                    TapZone("Previous photo", enabled = photoIndex > 0, onClick = onPrevious, modifier = Modifier.weight(1f).fillMaxHeight())
                    TapZone("Next photo", enabled = photoIndex < photoCount - 1, onClick = onNext, modifier = Modifier.weight(1f).fillMaxHeight())
                }
            } else {
                TapZone("View profile", enabled = true, onClick = onOpen, modifier = Modifier.fillMaxSize())
            }
        }
        if (photoCount > 1) {
            Row(
                modifier = Modifier.align(Alignment.TopCenter).padding(top = UsTheme.spacing.l).clearAndSetSemantics { },
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                repeat(photoCount) { index ->
                    val active = index == photoIndex
                    Box(
                        Modifier
                            .size(if (active) MARKER_ACTIVE else MARKER)
                            .clip(CircleShape)
                            .background(if (active) UsTheme.extended.accentSolid else UsTheme.extended.textDim.copy(alpha = 0.5f)),
                    )
                }
            }
        }
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .background(UsTheme.extended.bgCanvas.copy(alpha = 0.72f))
                .then(if (onOpen != null) Modifier.clickable(onClickLabel = "View profile", role = Role.Button, onClick = onOpen) else Modifier)
                .padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Text(
                    "${card.name}, ${card.age}",
                    style = MaterialTheme.typography.headlineSmall,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (card.verified) Pill("Verified", Tone.Positive)
            }
            // Mechanic M8: on a trip, so the city below is where they are visiting.
            card.visiting?.let { VisitingMark(it) }
            val place = listOfNotNull(card.city.takeIf { it.isNotBlank() }, card.distance).joinToString(" · ")
            if (place.isNotBlank()) Text(place, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
            card.lastActive?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted) }
            card.detail?.bio?.let {
                Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            // Mechanic M6: a few interests for the glance; the full list is on the profile.
            LabelChips(interests)
            card.reasons.take(MAX_REASONS).forEach {
                Text("• $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (onOpen != null) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                    Text("View profile", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.accentSolid)
                    Icon(UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(16.dp))
                }
            }
        }
    }
}

/** An invisible tap target over the photo. No ripple: the photo changing is the feedback. */
@Composable
private fun TapZone(label: String, enabled: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = enabled,
                onClickLabel = label,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = label },
    )
}

/** The first few interest LABELS for the card face; none until the option lists have loaded. */
internal fun CardUi.glanceInterests(options: ProfileOptionsUi?): List<String> {
    val basics = detail?.basics ?: return emptyList()
    return options?.basicsOf(basics)?.interests.orEmpty().take(MAX_GLANCE_INTERESTS)
}

/** The gallery, primary first, or the single card photo; a null entry is the placeholder. */
internal fun CardUi.photos(): List<String?> =
    detail?.gallery?.map { it.url }?.takeIf { it.isNotEmpty() } ?: listOf(photoUrl)

/** Off the screen on the side the action belongs to, keeping the other axis where the drag left it. */
private fun exitTarget(exit: DeckExit, from: Offset, size: IntSize): Offset = when (exit) {
    DeckExit.SPARK -> Offset(size.width * EXIT_FACTOR, from.y)
    DeckExit.PASS -> Offset(-size.width * EXIT_FACTOR, from.y)
    DeckExit.SUPER_SPARK -> Offset(from.x, -size.height * EXIT_FACTOR)
    DeckExit.STASH -> Offset(from.x, size.height * EXIT_FACTOR)
}

/**
 * The system's "remove animations" setting (animator duration scale 0). The
 * card then jumps to where it is going instead of flying or tilting; the drag
 * itself still follows the finger, because that is the user's own movement.
 */
@Composable
private fun rememberReducedMotion(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f
    }
}

private const val SUPER_SPARK = "Super Spark"
private const val MAX_REASONS = 2
private const val MAX_GLANCE_INTERESTS = 3
private const val EXIT_MILLIS = 220
private const val EXIT_FACTOR = 1.6f
private const val UP_RESISTANCE = 0.3f
private const val PREVIEW_SCALE = 0.94f
private val FLING_VELOCITY = 900.dp
private val HINT_TOP = 56.dp
private val MARKER = 6.dp
private val MARKER_ACTIVE = 8.dp
