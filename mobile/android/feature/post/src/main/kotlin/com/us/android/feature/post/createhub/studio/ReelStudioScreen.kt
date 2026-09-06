package com.us.android.feature.post.createhub.studio

import android.graphics.Bitmap
import android.view.TextureView
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.util.UnstableApi
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.ui.VideoLoadState
import com.us.android.core.media.ui.VideoLoadingOverlay
import com.us.android.core.media.ui.rememberVideoLoadState
import com.us.android.core.ui.UsErrorState
import com.us.android.feature.post.createhub.CreateTopBar
import com.us.android.feature.post.createhub.pressDim
import com.us.android.feature.post.createhub.studio.ReelStudioViewModel.StudioState
import kotlinx.coroutines.delay
import kotlin.math.roundToInt

/**
 * The Reel studio's screen (founder, 2026-09-05): a full-width 9:16
 * preview that loops the trimmed span with the look applied, a rail of
 * five tools under it — Frame, Trim, Speed, Looks, Text — and the chosen
 * tool's panel at the bottom. "Next" renders through the exporter behind
 * [ExportSheet]; the surface hands the file to the details step.
 *
 * The preview draws the whole source into a TextureView and frames it in
 * Compose: Fill scales the view to cover the box and slides it by the pan
 * (a drag on the video moves the crop window); Fit scales it to sit inside
 * the box over a blurred, dimmed still of the first frame — the export's
 * `GaussianBlurWithFrameOverlaid` does the real blur on every frame. The
 * text pill is drawn in Compose here so a drag places it at 60 fps; the
 * export bakes the same pill in as a bitmap overlay.
 */
@UnstableApi
@Composable
internal fun ReelStudioScreen(
    state: StudioState,
    actions: StudioActions,
    onClose: () -> Unit,
    onNext: () -> Unit,
    /** One muted line under the bar — the advanced editor was expected and is not usable. */
    notice: String? = null,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .statusBarsPadding()
            .navigationBarsPadding()
            .testTag("reel-studio"),
    ) {
        CreateTopBar(title = "Edit reel", onClose = onClose) {
            UsPillButton(
                text = "Next",
                onClick = onNext,
                enabled = state.canExport,
                modifier = Modifier.testTag("studio-next"),
            )
        }
        notice?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
        }
        val edit = state.edit
        when {
            state.unreadable -> UsErrorState(
                message = "That video can't be read. Pick another.",
                onRetry = onClose,
                modifier = Modifier.weight(1f),
            )
            edit == null -> Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(color = Color.White, strokeWidth = SPINNER_STROKE)
            }
            else -> StudioBody(edit = edit, state = state, actions = actions)
        }
    }
    state.export?.let { ExportSheet(percent = it.percent, onCancel = actions.cancelExport) }
    state.exportError?.let { ExportErrorSheet(message = it, onDismiss = actions.dismissExportError) }
}

/** What the studio's controls do — the ViewModel's verbs, bundled so the panels stay short. */
@Suppress("LongParameterList") // A bundle of callbacks, not a function that does eight things.
internal class StudioActions(
    val selectTool: (StudioTool) -> Unit,
    val togglePlaying: () -> Unit,
    val setPlaying: (Boolean) -> Unit,
    val setMode: (FrameMode) -> Unit,
    val pan: (dragPx: Float, previewPx: Float) -> Unit,
    val setTrimStart: (Long) -> Unit,
    val setTrimEnd: (Long) -> Unit,
    val setSpeed: (ReelSpeed) -> Unit,
    val setLook: (ReelLook) -> Unit,
    val setText: (String) -> Unit,
    val setTextStyle: (TextPillStyle) -> Unit,
    val moveText: (x: Float, y: Float) -> Unit,
    val removeText: () -> Unit,
    val cancelExport: () -> Unit,
    val dismissExportError: () -> Unit,
)

@UnstableApi
@Composable
private fun StudioBody(edit: ReelEdit, state: StudioState, actions: StudioActions) {
    val context = LocalContext.current
    val player = remember { ReelPreviewPlayer(context) }
    DisposableEffect(player) { onDispose { player.release() } }
    LaunchedEffect(edit.sourceUri, edit.look, edit.speed, state.playing) { player.apply(edit, state.playing) }
    val latest by rememberUpdatedState(edit)
    LaunchedEffect(Unit) {
        while (true) {
            player.keepInside(latest)
            delay(LOOP_TICK_MILLIS)
        }
    }
    Column(modifier = Modifier.fillMaxSize()) {
        StudioPreview(
            edit = edit,
            thumbnail = state.thumbnail,
            playing = state.playing,
            tool = state.tool,
            player = player,
            actions = actions,
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        )
        Spacer(Modifier.height(UsTheme.spacing.l))
        ToolRail(selected = state.tool, onSelect = actions.selectTool)
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(PANEL_HEIGHT)
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            when (state.tool) {
                StudioTool.FRAME -> FramePanel(edit = edit, onMode = actions.setMode)
                StudioTool.TRIM -> TrimPanel(
                    edit = edit,
                    frames = state.frames,
                    onStart = { timeUs ->
                        actions.setTrimStart(timeUs)
                        player.scrubTo(timeUs)
                    },
                    onEnd = { timeUs ->
                        actions.setTrimEnd(timeUs)
                        player.scrubTo(timeUs)
                    },
                    onDragEnd = { actions.setPlaying(true) },
                )
                StudioTool.SPEED -> SpeedPanel(selected = edit.speed, onSelect = actions.setSpeed)
                StudioTool.LOOKS -> LooksPanel(
                    thumbnail = state.thumbnail,
                    selected = edit.look,
                    onSelect = actions.setLook,
                )
                StudioTool.TEXT -> TextPanel(
                    pill = edit.text,
                    onText = actions.setText,
                    onStyle = actions.setTextStyle,
                    onRemove = actions.removeText,
                )
            }
        }
    }
}

// ── The preview ─────────────────────────────────────────────────────────

@UnstableApi
@Composable
private fun StudioPreview(
    edit: ReelEdit,
    thumbnail: Bitmap?,
    playing: Boolean,
    tool: StudioTool,
    player: ReelPreviewPlayer,
    actions: StudioActions,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(PREVIEW_RADIUS)
    Box(modifier = modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .aspectRatio(ReelFrame.ASPECT, matchHeightConstraintsFirst = true)
                .clip(shape)
                .background(Color.Black)
                .testTag("studio-preview"),
        ) {
            val density = LocalDensity.current
            val boxW = with(density) { maxWidth.toPx() }
            val boxH = with(density) { maxHeight.toPx() }
            val placement = remember(edit.width, edit.height, edit.mode, edit.pan, boxW, boxH) {
                previewPlacement(edit, boxW, boxH)
            }
            if (edit.mode == FrameMode.FIT && thumbnail != null) {
                Image(
                    bitmap = thumbnail.asImageBitmap(),
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier
                        .fillMaxSize()
                        .blur(FIT_BLUR),
                )
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .background(Color.Black.copy(alpha = FIT_DIM)),
                )
            }
            AndroidView(
                factory = { context -> TextureView(context).also(player::attach) },
                modifier = Modifier
                    .align(Alignment.Center)
                    .requiredSize(with(density) { placement.width.toDp() }, with(density) { placement.height.toDp() })
                    .offset { IntOffset(placement.dx.roundToInt(), placement.dy.roundToInt()) },
            )
            PreviewGestures(edit = edit, tool = tool, boxW = boxW, boxH = boxH, actions = actions)
            edit.text?.let { pill ->
                TextPillOverlay(
                    pill = pill,
                    boxW = boxW,
                    boxH = boxH,
                    draggable = tool == StudioTool.TEXT,
                    onMove = actions.moveText,
                )
            }
            // A look change re-prepares the player, and the first prepare
            // of a long source can take a moment on a mid-range phone — the
            // preview box is black in both gaps, which reads as an editor
            // that has crashed. Only ever while the preview is meant to be
            // running: a scrubbed trim handle pauses it, and a paused
            // preview shows the play glyph below instead.
            val loadState = rememberVideoLoadState(player.player)
            VideoLoadingOverlay(state = loadState, onRetry = player.player::prepare)
            if (!playing && loadState == VideoLoadState.NONE) {
                Icon(
                    imageVector = UsIcons.Play,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier
                        .align(Alignment.Center)
                        .size(PLAY_GLYPH),
                )
            }
        }
    }
}

/** Where the video view sits inside the 9:16 box: its size, and its shift for the pan. */
private data class Placement(val width: Float, val height: Float, val dx: Float, val dy: Float)

/**
 * Fill covers the box (the view is as tall as the box for a wide source,
 * as wide for a tall one) and slides by the pan along the free axis; Fit
 * sits inside it, centred. See [ReelFrame.cropWindow] for the pan's meaning.
 */
private fun previewPlacement(edit: ReelEdit, boxW: Float, boxH: Float): Placement {
    val aspect = edit.width.toFloat() / edit.height
    val wider = ReelFrame.isWiderThanReel(edit.width, edit.height)
    return when (edit.mode) {
        FrameMode.FILL -> if (wider) {
            val width = boxH * aspect
            Placement(width, boxH, dx = -edit.pan * (width - boxW) / 2f, dy = 0f)
        } else {
            val height = boxW / aspect
            Placement(boxW, height, dx = 0f, dy = edit.pan * (height - boxH) / 2f)
        }
        FrameMode.FIT -> if (wider) Placement(boxW, boxW / aspect, 0f, 0f) else Placement(boxH * aspect, boxH, 0f, 0f)
    }
}

/** A drag pans the crop window on the Frame tool; a tap pauses or plays. */
@Composable
private fun PreviewGestures(edit: ReelEdit, tool: StudioTool, boxW: Float, boxH: Float, actions: StudioActions) {
    val wider = ReelFrame.isWiderThanReel(edit.width, edit.height)
    val pans = tool == StudioTool.FRAME && edit.mode == FrameMode.FILL
    val latestPan by rememberUpdatedState(actions.pan)
    Box(
        modifier = Modifier
            .fillMaxSize()
            .pointerInput(pans, wider, boxW, boxH) {
                if (!pans) return@pointerInput
                detectDragGestures { change, drag ->
                    change.consume()
                    // NDC y is up, screen y is down: a drag downward reveals the top.
                    if (wider) latestPan(drag.x, boxW) else latestPan(-drag.y, boxH)
                }
            }
            .pointerInput(Unit) { detectTapGestures { actions.togglePlaying() } }
            .semantics {
                contentDescription = if (pans) "Preview. Drag to choose the crop" else "Preview. Tap to pause or play"
            },
    )
}

/**
 * The pill as Compose draws it over the preview: Outfit, sized from the
 * box width the way [TextPillRenderer] sizes it from the frame width, so
 * what is placed is what is exported. Draggable on the Text tool.
 */
@Composable
private fun TextPillOverlay(
    pill: TextPill,
    boxW: Float,
    boxH: Float,
    draggable: Boolean,
    onMove: (x: Float, y: Float) -> Unit,
) {
    val density = LocalDensity.current
    val textSize = with(density) { (boxW * PILL_TEXT_FRACTION).toSp() }
    val latestMove by rememberUpdatedState(onMove)
    val latestPill by rememberUpdatedState(pill)
    Box(modifier = Modifier.fillMaxSize()) {
        Box(
            modifier = Modifier
                .align(Alignment.TopStart)
                .offset { IntOffset((pill.x * boxW).roundToInt(), (pill.y * boxH).roundToInt()) }
                // Centre the pill on its point: the layout's own size is only known after measure.
                .then(CentreOnPoint)
                .clip(CircleShape)
                .background(if (pill.style == TextPillStyle.WHITE) Color.White else UsTheme.extended.brandNavy)
                .pointerInput(draggable, boxW, boxH) {
                    if (!draggable) return@pointerInput
                    detectDragGestures { change, drag ->
                        change.consume()
                        val current = latestPill
                        latestMove(current.x + drag.x / boxW, current.y + drag.y / boxH)
                    }
                }
                .padding(horizontal = PILL_PAD_H, vertical = PILL_PAD_V)
                .semantics { contentDescription = "Text: ${pill.text}. Drag to move" }
                .testTag("studio-text-pill"),
        ) {
            Text(
                text = pill.text,
                style = MaterialTheme.typography.titleMedium.copy(fontSize = textSize),
                fontWeight = FontWeight.Bold,
                color = if (pill.style == TextPillStyle.WHITE) UsTheme.extended.brandNavy else Color.White,
                maxLines = 1,
            )
        }
    }
}

/** Shifts a box back by half its own measured size, so its offset names its centre. */
private val CentreOnPoint = Modifier.layout { measurable, constraints ->
    val placeable = measurable.measure(constraints)
    layout(placeable.width, placeable.height) { placeable.place(-placeable.width / 2, -placeable.height / 2) }
}

// ── The rail ────────────────────────────────────────────────────────────

/** Five tools; the selected one is white, the rest muted — the app's selection rule. */
@Composable
private fun ToolRail(selected: StudioTool, onSelect: (StudioTool) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("studio-tools"),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        StudioTool.entries.forEach { tool ->
            ToolTab(tool = tool, selected = tool == selected, onClick = { onSelect(tool) })
        }
    }
}

@Composable
private fun ToolTab(tool: StudioTool, selected: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val tint = if (selected) Color.White else UsTheme.extended.textMuted
    Column(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics {
                role = Role.Tab
                this.selected = selected
                contentDescription = tool.label
            }
            .testTag("studio-tool-${tool.name.lowercase()}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = tool.icon, contentDescription = null, tint = tint, modifier = Modifier.size(TOOL_GLYPH))
        Text(
            text = tool.label,
            style = MaterialTheme.typography.labelSmall,
            fontSize = TOOL_LABEL_SIZE,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
            color = tint,
        )
    }
}

/** Lucide: crop · scissors · gauge · sliders · type. */
private val StudioTool.icon: ImageVector
    get() = when (this) {
        StudioTool.FRAME -> UsIcons.Crop
        StudioTool.TRIM -> UsIcons.Scissors
        StudioTool.SPEED -> UsIcons.Gauge
        StudioTool.LOOKS -> UsIcons.Sliders
        StudioTool.TEXT -> UsIcons.Type
    }

// ── Metrics ─────────────────────────────────────────────────────────────

private const val LOOP_TICK_MILLIS = 120L
private const val FIT_DIM = 0.35f
private const val PILL_TEXT_FRACTION = 0.052f
private val PREVIEW_RADIUS = 18.dp
private val PANEL_HEIGHT = 132.dp
private val FIT_BLUR = 24.dp
private val PLAY_GLYPH = 44.dp
private val SPINNER_STROKE = 2.dp
private val TOOL_GLYPH = 20.dp
private val TOOL_LABEL_SIZE = 11.sp
private val PILL_PAD_H = 14.dp
private val PILL_PAD_V = 7.dp
