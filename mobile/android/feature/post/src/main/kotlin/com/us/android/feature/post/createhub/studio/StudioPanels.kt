package com.us.android.feature.post.createhub.studio

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
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
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.createhub.CoverFrame
import com.us.android.feature.post.createhub.Filmstrip
import com.us.android.feature.post.createhub.ReelInputField
import com.us.android.feature.post.createhub.pressDim
import kotlin.math.abs
import kotlin.math.roundToInt

// ── Frame ───────────────────────────────────────────────────────────────

/** Fill or Fit, and one line saying what a drag does. */
@Composable
internal fun FramePanel(edit: ReelEdit, onMode: (FrameMode) -> Unit) {
    PanelColumn {
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            StudioChip(label = "Fill", selected = edit.mode == FrameMode.FILL, onClick = { onMode(FrameMode.FILL) })
            StudioChip(label = "Fit", selected = edit.mode == FrameMode.FIT, onClick = { onMode(FrameMode.FIT) })
        }
        Hint(
            text = when (edit.mode) {
                FrameMode.FILL -> if (ReelFrame.windowFraction(edit.width, edit.height) < 1f) {
                    "Drag the video to choose what stays in the frame."
                } else {
                    "Already 9:16 — nothing to crop."
                }
                FrameMode.FIT -> "The whole video, over a blurred copy of itself."
            },
        )
    }
}

// ── Trim ────────────────────────────────────────────────────────────────

/** The strip with two handles, the span's readout, and the cap's warning when the export would breach it. */
@Composable
internal fun TrimPanel(
    edit: ReelEdit,
    frames: List<CoverFrame>,
    onStart: (Long) -> Unit,
    onEnd: (Long) -> Unit,
    onDragEnd: () -> Unit,
) {
    PanelColumn {
        TrimStrip(
            frames = frames,
            durationUs = edit.durationUs,
            startUs = edit.trimStartUs,
            endUs = edit.trimEndUs,
            onStart = onStart,
            onEnd = onEnd,
            onDragEnd = onDragEnd,
        )
        Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "${Filmstrip.format(edit.trimStartUs)} – ${Filmstrip.format(edit.trimEndUs)}",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .weight(1f)
                    .testTag("studio-trim-readout"),
            )
            Text(
                text = if (edit.exceedsReelCap) {
                    "Over 5 minutes — trim it or speed it up."
                } else {
                    "${Filmstrip.format(edit.exportedUs)} at ${edit.speed.label}"
                },
                style = MaterialTheme.typography.labelMedium,
                color = if (edit.exceedsReelCap) MaterialTheme.colorScheme.error else UsTheme.extended.textMuted,
            )
        }
    }
}

/**
 * The trim strip: the source's thumbnails edge to edge, the kept span
 * between two white handles, the rest dimmed. A drag that starts nearer
 * the start handle moves it, otherwise the end handle; a handle never
 * crosses the other — [ReelEdit] pushes it instead.
 */
@Composable
private fun TrimStrip(
    frames: List<CoverFrame>,
    durationUs: Long,
    startUs: Long,
    endUs: Long,
    onStart: (Long) -> Unit,
    onEnd: (Long) -> Unit,
    onDragEnd: () -> Unit,
) {
    val latestStart by rememberUpdatedState(onStart)
    val latestEnd by rememberUpdatedState(onEnd)
    val latestDragEnd by rememberUpdatedState(onDragEnd)
    val latestDuration by rememberUpdatedState(durationUs)
    val latestStartUs by rememberUpdatedState(startUs)
    val latestEndUs by rememberUpdatedState(endUs)
    val density = LocalDensity.current
    val handlePx = with(density) { HANDLE_WIDTH.toPx() }
    val shape = RoundedCornerShape(UsTheme.radii.small)
    BoxWithConstraints(
        modifier = Modifier
            .fillMaxWidth()
            .height(STRIP_HEIGHT)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .pointerInput(Unit) {
                var movingStart = true
                detectDragGestures(
                    onDragStart = { offset ->
                        val startX = Filmstrip.fractionOf(latestStartUs, latestDuration) * size.width
                        val endX = Filmstrip.fractionOf(latestEndUs, latestDuration) * size.width
                        movingStart = abs(offset.x - startX) <= abs(offset.x - endX)
                    },
                    onDragEnd = { latestDragEnd() },
                    onDragCancel = { latestDragEnd() },
                ) { change, _ ->
                    change.consume()
                    val time = Filmstrip.timeAt(change.position.x / size.width, latestDuration)
                    if (movingStart) latestStart(time) else latestEnd(time)
                }
            }
            .semantics { contentDescription = "Trim, drag the handles" }
            .testTag("studio-trim-strip"),
    ) {
        val widthPx = with(density) { maxWidth.toPx() }
        Row(modifier = Modifier.fillMaxSize()) {
            frames.forEach { frame ->
                val image = remember(frame.bitmap) { frame.bitmap?.asImageBitmap() }
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxHeight()
                        .background(UsTheme.extended.bgCardHover),
                ) {
                    if (image != null) {
                        Image(
                            bitmap = image,
                            contentDescription = null,
                            contentScale = ContentScale.Crop,
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
        val startX = Filmstrip.fractionOf(startUs, durationUs) * widthPx
        val endX = Filmstrip.fractionOf(endUs, durationUs) * widthPx
        TrimShade(startX = startX, endX = endX, widthPx = widthPx)
        TrimHandle(x = startX - handlePx / 2, testTag = "studio-trim-start")
        TrimHandle(x = endX - handlePx / 2, testTag = "studio-trim-end")
    }
}

/** The dimmed parts outside the kept span, and the white frame around it. */
@Composable
private fun TrimShade(startX: Float, endX: Float, widthPx: Float) {
    val density = LocalDensity.current
    Box(
        modifier = Modifier
            .fillMaxHeight()
            .width(with(density) { startX.toDp() })
            .background(Color.Black.copy(alpha = OUTSIDE_DIM)),
    )
    Box(
        modifier = Modifier
            .fillMaxHeight()
            .offset { IntOffset(endX.roundToInt(), 0) }
            .width(with(density) { (widthPx - endX).coerceAtLeast(0f).toDp() })
            .background(Color.Black.copy(alpha = OUTSIDE_DIM)),
    )
    Box(
        modifier = Modifier
            .offset { IntOffset(startX.roundToInt(), 0) }
            .width(with(density) { (endX - startX).coerceAtLeast(0f).toDp() })
            .fillMaxHeight()
            .border(HANDLE_STROKE, Color.White, RoundedCornerShape(UsTheme.radii.small)),
    )
}

@Composable
private fun TrimHandle(x: Float, testTag: String) {
    Box(
        modifier = Modifier
            .offset { IntOffset(x.roundToInt(), 0) }
            .width(HANDLE_WIDTH)
            .fillMaxHeight()
            .padding(vertical = HANDLE_INSET)
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(Color.White)
            .testTag(testTag),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_GRIP_WIDTH, height = HANDLE_GRIP_HEIGHT)
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.brandNavy.copy(alpha = GRIP_ALPHA)),
        )
    }
}

// ── Speed ───────────────────────────────────────────────────────────────

@Composable
internal fun SpeedPanel(selected: ReelSpeed, onSelect: (ReelSpeed) -> Unit) {
    PanelColumn {
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            ReelSpeed.entries.forEach { speed ->
                StudioChip(label = speed.label, selected = speed == selected, onClick = { onSelect(speed) })
            }
        }
        Hint(text = "The sound keeps up with the picture.")
    }
}

// ── Looks ───────────────────────────────────────────────────────────────

/** Seven looks on the first frame, each through its own colour matrix — the export's matrix, on a still. */
@Composable
internal fun LooksPanel(thumbnail: Bitmap?, selected: ReelLook, onSelect: (ReelLook) -> Unit) {
    val image = remember(thumbnail) { thumbnail?.asImageBitmap() }
    LazyRow(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("studio-looks"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        contentPadding = PaddingValues(vertical = UsTheme.spacing.s),
    ) {
        items(ReelLook.entries.size, key = { ReelLook.entries[it].name }) { index ->
            val look = ReelLook.entries[index]
            LookTile(look = look, image = image, selected = look == selected, onClick = { onSelect(look) })
        }
    }
}

@Composable
private fun LookTile(
    look: ReelLook,
    image: androidx.compose.ui.graphics.ImageBitmap?,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val filter = remember(look) { ColorFilter.colorMatrix(ColorMatrix(look.colorMatrix())) }
    Column(
        modifier = Modifier
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .pressDim(interaction)
            .semantics {
                role = Role.RadioButton
                this.selected = selected
                contentDescription = "${look.label} look"
            }
            .testTag("studio-look-${look.name.lowercase()}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Box(
            modifier = Modifier
                .size(width = LOOK_WIDTH, height = LOOK_HEIGHT)
                .clip(shape)
                .background(UsTheme.extended.bgCard)
                .border(
                    width = if (selected) LOOK_STROKE else HAIRLINE,
                    color = if (selected) Color.White else UsTheme.extended.borderSubtle,
                    shape = shape,
                ),
        ) {
            if (image != null) {
                Image(
                    bitmap = image,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    colorFilter = filter,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
        Text(
            text = look.label,
            style = MaterialTheme.typography.labelSmall,
            fontSize = LOOK_LABEL_SIZE,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Medium,
            color = if (selected) Color.White else UsTheme.extended.textMuted,
        )
    }
}

// ── Text ────────────────────────────────────────────────────────────────

/** One line, two pill styles, and Remove once there is a pill to remove. */
@Composable
internal fun TextPanel(
    pill: TextPill?,
    onText: (String) -> Unit,
    onStyle: (TextPillStyle) -> Unit,
    onRemove: () -> Unit,
) {
    PanelColumn {
        ReelInputField(
            value = pill?.text.orEmpty(),
            onValueChange = onText,
            placeholder = "Say something on the video",
            icon = UsIcons.Type,
            modifier = Modifier.testTag("studio-text-input"),
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextPillStyle.entries.forEach { style ->
                StudioChip(
                    label = style.label,
                    selected = pill?.style == style,
                    onClick = { onStyle(style) },
                    enabled = pill != null,
                )
            }
            Spacer(Modifier.weight(1f))
            if (pill != null) {
                val interaction = remember { MutableInteractionSource() }
                Text(
                    text = "Remove",
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .clickable(interactionSource = interaction, indication = null, onClick = onRemove)
                        .pressDim(interaction)
                        .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
                        .semantics { role = Role.Button }
                        .testTag("studio-text-remove"),
                )
            }
        }
    }
}

// ── Shared pieces ───────────────────────────────────────────────────────

@Composable
private fun PanelColumn(content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        content = content,
    )
}

@Composable
private fun Hint(text: String) {
    Text(text = text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
}

/** A choice chip: white with navy text when selected, glass otherwise; no ripple, a dim on press. */
@Composable
internal fun StudioChip(label: String, selected: Boolean, onClick: () -> Unit, enabled: Boolean = true) {
    val interaction = remember { MutableInteractionSource() }
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Box(
        modifier = Modifier
            .clip(shape)
            .background(if (selected) Color.White else UsTheme.extended.glassBg)
            .border(HAIRLINE, if (selected) Color.White else UsTheme.extended.glassBorder, shape)
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = CHIP_PAD_H, vertical = CHIP_PAD_V)
            .semantics {
                role = Role.RadioButton
                this.selected = selected
            }
            .testTag("studio-chip-${label.lowercase()}"),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = when {
                selected -> UsTheme.extended.brandNavy
                enabled -> UsTheme.extended.textPrimary
                else -> UsTheme.extended.textGhost
            },
        )
    }
}

// ── Metrics ─────────────────────────────────────────────────────────────

private const val OUTSIDE_DIM = 0.55f
private const val GRIP_ALPHA = 0.5f
private val HAIRLINE = 1.dp
private val STRIP_HEIGHT = 56.dp
private val HANDLE_WIDTH = 16.dp
private val HANDLE_STROKE = 2.dp
private val HANDLE_INSET = 0.dp
private val HANDLE_GRIP_WIDTH = 3.dp
private val HANDLE_GRIP_HEIGHT = 18.dp
private val LOOK_WIDTH = 56.dp
private val LOOK_HEIGHT = 76.dp
private val LOOK_STROKE = 2.dp
private val LOOK_LABEL_SIZE = 11.sp
private val CHIP_PAD_H = 16.dp
private val CHIP_PAD_V = 8.dp
