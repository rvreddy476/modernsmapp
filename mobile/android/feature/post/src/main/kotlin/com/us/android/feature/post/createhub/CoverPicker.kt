package com.us.android.feature.post.createhub

import android.graphics.Bitmap
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
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
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.publish.VideoKind
import com.us.android.feature.post.createhub.ReelPublishViewModel.CoverPicker
import kotlin.math.roundToInt

/**
 * The exact-frame cover picker (founder, 2026-09-05): the frame under the
 * handle, large, in the kind's aspect; the filmstrip beneath with a
 * draggable handle and the readout ("0:42.6"); "Upload" for a gallery
 * image; "Use this frame" to confirm. Drawn over the form, full screen,
 * so the preview has the width it needs.
 *
 * Dragging moves the handle and the readout at once and asks the ViewModel
 * for the frame at that instant; the preview swaps when the seek answers,
 * with a small spinner in the corner while it is in flight.
 */
@Composable
internal fun CoverPickerScreen(
    kind: VideoKind,
    picker: CoverPicker,
    frames: List<CoverFrame>,
    durationUs: Long,
    actions: CoverPickerActions,
) {
    val onScrub = actions.onScrub
    val onConfirm = actions.onConfirm
    val onClose = actions.onClose
    val pickImage = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        uri?.let { actions.onUpload(it.toString()) }
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .statusBarsPadding()
            .navigationBarsPadding()
            .testTag("cover-picker"),
    ) {
        CreateTopBar(title = "Cover", onClose = onClose) {
            UsPillButton(
                text = "Use this frame",
                onClick = onConfirm,
                enabled = picker.preview != null && !picker.seeking,
                modifier = Modifier.testTag("cover-confirm"),
            )
        }
        Column(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(UsTheme.spacing.l))
            FramePreview(kind = kind, bitmap = picker.preview, seeking = picker.seeking)
            Spacer(Modifier.height(UsTheme.spacing.xl))
            Text(
                text = Filmstrip.format(picker.timeUs),
                style = MaterialTheme.typography.titleMedium,
                fontSize = READOUT_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .semantics { contentDescription = "At ${Filmstrip.format(picker.timeUs)}" }
                    .testTag("cover-time"),
            )
            Spacer(Modifier.height(UsTheme.spacing.l))
            FilmstripTimeline(
                kind = kind,
                frames = frames,
                durationUs = durationUs,
                timeUs = picker.timeUs,
                onScrub = onScrub,
            )
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            UploadRow(
                onPick = {
                    pickImage.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                },
            )
        }
    }
}

/** The frame at the handle, in the kind's aspect; the spinner sits in the corner while a seek runs. */
@Composable
private fun FramePreview(kind: VideoKind, bitmap: Bitmap?, seeking: Boolean) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val image = remember(bitmap) { bitmap?.asImageBitmap() }
    val aspect = if (kind == VideoKind.LONG) LANDSCAPE else PORTRAIT
    BoxWithConstraints(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
        // A portrait preview is capped so the strip stays on screen under it.
        val width = if (kind == VideoKind.LONG) maxWidth else minOf(maxWidth, PORTRAIT_PREVIEW_WIDTH)
        Box(
            modifier = Modifier
                .width(width)
                .aspectRatio(aspect)
                .clip(shape)
                .background(UsTheme.extended.bgCard)
                .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
                .semantics { contentDescription = "Cover preview" }
                .testTag("cover-preview"),
            contentAlignment = Alignment.Center,
        ) {
            if (image != null) {
                Image(
                    bitmap = image,
                    contentDescription = null,
                    contentScale = ContentScale.Crop,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                Icon(
                    imageVector = UsIcons.Film,
                    contentDescription = null,
                    tint = UsTheme.extended.textDim,
                    modifier = Modifier.size(PREVIEW_GLYPH),
                )
            }
            if (seeking) {
                CircularProgressIndicator(
                    color = Color.White,
                    strokeWidth = SPINNER_STROKE,
                    modifier = Modifier
                        .align(Alignment.TopEnd)
                        .padding(UsTheme.spacing.m)
                        .size(SPINNER_SIZE),
                )
            }
        }
    }
}

/**
 * The filmstrip: the thumbnails edge to edge in one strip, a white handle
 * over the instant under the finger. A drag anywhere on the strip moves
 * the handle; a tap jumps to it. The handle's place is the instant as a
 * fraction of the video, so it lands exactly where the seek looks.
 */
@Composable
private fun FilmstripTimeline(
    kind: VideoKind,
    frames: List<CoverFrame>,
    durationUs: Long,
    timeUs: Long,
    onScrub: (Long) -> Unit,
) {
    val latestScrub by rememberUpdatedState(onScrub)
    val latestDuration by rememberUpdatedState(durationUs)
    val density = LocalDensity.current
    val handleWidthPx = with(density) { HANDLE_WIDTH.toPx() }
    val shape = RoundedCornerShape(UsTheme.radii.small)
    BoxWithConstraints(
        modifier = Modifier
            .fillMaxWidth()
            .height(if (kind == VideoKind.LONG) STRIP_HEIGHT_LANDSCAPE else STRIP_HEIGHT_PORTRAIT)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .pointerInput(Unit) {
                detectDragGestures { change, _ ->
                    change.consume()
                    latestScrub(Filmstrip.timeAt(change.position.x / size.width, latestDuration))
                }
            }
            .pointerInput(Unit) {
                detectTapGestures { offset -> latestScrub(Filmstrip.timeAt(offset.x / size.width, latestDuration)) }
            }
            .semantics {
                contentDescription = "Filmstrip, drag to choose a frame"
                role = Role.Image
            }
            .testTag("cover-filmstrip"),
    ) {
        val stripWidthPx = with(density) { maxWidth.toPx() }
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
        val fraction = Filmstrip.fractionOf(timeUs, durationUs)
        val handleX = (fraction * stripWidthPx - handleWidthPx / 2)
            .coerceIn(0f, (stripWidthPx - handleWidthPx).coerceAtLeast(0f))
        Box(
            modifier = Modifier
                .offset { IntOffset(handleX.roundToInt(), 0) }
                .width(HANDLE_WIDTH)
                .fillMaxHeight()
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .border(HANDLE_STROKE, Color.White, RoundedCornerShape(UsTheme.radii.small))
                .testTag("cover-handle"),
        )
    }
}

/** "Upload" — a gallery image as the cover instead of a frame. */
@Composable
private fun UploadRow(onPick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, RoundedCornerShape(UsTheme.radii.full))
            .clickable(interactionSource = interaction, indication = null, onClick = onPick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
            .semantics {
                role = Role.Button
                contentDescription = "Upload a cover from your gallery"
            }
            .testTag("cover-upload"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Icon(
            imageVector = UsIcons.ImagePlus,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(UPLOAD_GLYPH),
        )
        Text(
            text = "Upload",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** What the picker's controls do: scrub, confirm the frame, take a gallery image, leave. */
internal class CoverPickerActions(
    val onScrub: (timeUs: Long) -> Unit,
    val onConfirm: () -> Unit,
    val onUpload: (imageUri: String) -> Unit,
    val onClose: () -> Unit,
)

private const val LANDSCAPE = 16f / 9f
private const val PORTRAIT = 9f / 16f
private val HAIRLINE = 1.dp
private val HANDLE_WIDTH = 22.dp
private val HANDLE_STROKE = 3.dp
private val STRIP_HEIGHT_LANDSCAPE = 48.dp
private val STRIP_HEIGHT_PORTRAIT = 64.dp
private val PORTRAIT_PREVIEW_WIDTH = 240.dp
private val PREVIEW_GLYPH = 28.dp
private val SPINNER_SIZE = 18.dp
private val SPINNER_STROKE = 2.dp
private val UPLOAD_GLYPH = 18.dp
private val READOUT_SIZE = 18.sp
