package com.us.android.feature.post.createhub

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.ui.usSwitchColors
import com.us.android.feature.post.createhub.ReelPublishViewModel.Phase
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC

/**
 * REEL and VIDEO — pick a video, then the TikTok-shaped form.
 *
 * Cover preview beside the description, the cover strip under them, then
 * one card of rows (Tag people, Add location, Audience, Category) and one
 * card of switches. Everything scrolls; the keyboard is an inset the root
 * column pads for, so the description is never under it.
 *
 * Opened from the Video tile (Tube, 2026-09-05) the same form is a LONG
 * video: a required title on top, a 16:9 cover strip, no remix switch. The
 * ViewModel reads which from the route; a reel over five minutes can switch
 * to it in place ([GateNotice]).
 *
 * Post hands the publish to WorkManager and LEAVES — a reel closes back to
 * where the user was and the Reels tab's pending item shows the upload; a
 * long video opens Tube home, where its pending item is. There is no
 * published callback here because nothing is published while this surface
 * exists.
 */
@Composable
internal fun ReelSurface(
    onClose: () -> Unit,
    onOpenTube: () -> Unit,
    viewModel: ReelPublishViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var cameraTarget by remember { mutableStateOf<android.net.Uri?>(null) }

    val pickVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri -> uri?.let { viewModel.onVideoPicked(it.toString()) } }

    val captureVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.CaptureVideo(),
    ) { saved -> if (saved) cameraTarget?.let { viewModel.onVideoPicked(it.toString()) } }

    val openCamera = rememberCameraLaunch {
        val target = captureUri(context, "mp4")
        cameraTarget = target
        captureVideo.launch(target)
    }
    val openSystemPicker = {
        pickVideo.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.VideoOnly))
    }
    val long = state.kind == VideoKind.LONG
    if (state.videoUri == null) {
        MediaGallerySurface(
            kind = GalleryKind.Videos,
            title = if (long) "New video" else "New reel",
            subtitle = if (long) "Pick a video — it posts to Tube." else "Pick a video — it posts to Reels.",
            onClose = onClose,
            onCamera = openCamera,
            onPicked = { uris -> uris.firstOrNull()?.let { viewModel.onVideoPicked(it.toString()) } },
            onSystemPicker = openSystemPicker,
        )
        return
    }

    if (state.phase is Phase.Enqueued) {
        // Handed to the worker: leave, once. A reel lands the user back where
        // they were and the Reels tab's pending item takes over; a long video
        // opens Tube home, whose pending item takes over there.
        LaunchedEffect(Unit) { if (long) onOpenTube() else onClose() }
    }

    ReelForm(state = state, viewModel = viewModel, onClose = onClose, onChangeVideo = openSystemPicker)
}

private enum class ReelSheet { None, Audience, Category, Location, People }

@Suppress("LongMethod") // One surface, one composable: the form IS the list of its parts.
@Composable
private fun ReelForm(
    state: ReelPublishViewModel.ReelUiState,
    viewModel: ReelPublishViewModel,
    onClose: () -> Unit,
    onChangeVideo: () -> Unit,
) {
    var sheet by rememberSaveable { mutableStateOf(ReelSheet.None) }
    val editable = !state.isBusy
    val long = state.kind == VideoKind.LONG
    val noun = if (long) "video" else "reel"

    Box(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .imePadding(),
        ) {
            CreateTopBar(title = if (long) "New video" else "New reel", onClose = onClose) {
                PublishPill(
                    text = if (state.phase is Phase.Failure) "Try again" else "Post",
                    enabled = state.canPost,
                    busy = state.isBusy,
                    onClick = viewModel::onPost,
                    description = postDescription(state, noun),
                    testTag = "reel-post",
                )
            }
            Column(
                modifier = Modifier
                    .weight(1f)
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal),
            ) {
                Spacer(Modifier.height(UsTheme.spacing.s))
                if (long) {
                    TitleField(value = state.title, onValueChange = viewModel::onTitleChanged, enabled = editable)
                    Spacer(Modifier.height(UsTheme.spacing.l))
                }
                CoverAndDescription(
                    kind = state.kind,
                    cover = state.cover,
                    caption = state.caption,
                    onCaptionChanged = viewModel::onCaptionChanged,
                    enabled = editable,
                )
                Spacer(Modifier.height(UsTheme.spacing.l))
                CoverStrip(
                    kind = state.kind,
                    frames = state.frames,
                    loading = state.framesLoading,
                    coverIndex = state.coverIndex,
                    enabled = editable,
                    onSelect = viewModel::onCoverSelected,
                    onChangeVideo = onChangeVideo,
                )
                GateNotice(gate = state.gate, enabled = editable, onSwitchToLong = viewModel::switchToLong)
                Spacer(Modifier.height(UsTheme.spacing.xxl))
                DetailsCard(
                    state = state,
                    enabled = editable,
                    onTagPeople = { sheet = ReelSheet.People },
                    onUntag = viewModel::onUntagUser,
                    onLocation = { sheet = ReelSheet.Location },
                    onAudience = { sheet = ReelSheet.Audience },
                    onCategory = { sheet = ReelSheet.Category },
                )
                Spacer(Modifier.height(UsTheme.spacing.xxl))
                SwitchesCard(state = state, viewModel = viewModel, enabled = editable)
                PhaseStatus(phase = state.phase)
                Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            }
        }
        if (sheet == ReelSheet.People) {
            TagPeopleScreen(
                state = state,
                onQueryChanged = viewModel::onPeopleQueryChanged,
                onTag = viewModel::onTagUser,
                onUntag = viewModel::onUntagUser,
                onDone = { sheet = ReelSheet.None },
            )
        }
    }

    ReelPickerSheets(sheet = sheet, state = state, viewModel = viewModel, onClose = { sheet = ReelSheet.None })
}

/** What the Post pill says to a screen reader: what it will do, or why it cannot. */
private fun postDescription(state: ReelPublishViewModel.ReelUiState, noun: String): String = when {
    state.isBusy -> "Post $noun. In progress."
    state.canPost -> "Post $noun"
    !state.gate.allowsPost -> "Post $noun. Unavailable: ${gateMessage(state.gate)}"
    !state.hasRequiredText -> "Post video. Unavailable: add a title first."
    else -> "Post $noun. Unavailable: choose a video first."
}

/** The three picker sheets — audience, category, location — one at a time. */
@Composable
private fun ReelPickerSheets(
    sheet: ReelSheet,
    state: ReelPublishViewModel.ReelUiState,
    viewModel: ReelPublishViewModel,
    onClose: () -> Unit,
) {
    when (sheet) {
        ReelSheet.Audience -> ReelOptionSheet(
            title = "Audience",
            options = AudienceOptions,
            selected = state.visibility,
            onPick = {
                viewModel.onVisibilityChanged(it)
                onClose()
            },
            onDismiss = onClose,
        )
        ReelSheet.Category -> ReelOptionSheet(
            title = "Category",
            options = listOf(ReelOption("", "None")) + state.categories.map { ReelOption(it.id, it.label) },
            selected = state.category,
            onPick = {
                viewModel.onCategoryChanged(it)
                onClose()
            },
            onDismiss = onClose,
        )
        ReelSheet.Location -> ReelLocationSheet(
            initial = state.locationName,
            onDone = {
                viewModel.onLocationChanged(it)
                onClose()
            },
            onDismiss = onClose,
        )
        ReelSheet.None, ReelSheet.People -> Unit
    }
}

// ── Cover + description ─────────────────────────────────────────────────

/**
 * Instagram's arrangement for a reel: the 9:16 cover on the left, the text
 * beside it. A long video is landscape, so its 16:9 cover sits full-width
 * ABOVE the description instead — a 9:16 slot would crop most of the frame.
 */
@Composable
private fun CoverAndDescription(
    kind: VideoKind,
    cover: CoverFrame?,
    caption: String,
    onCaptionChanged: (String) -> Unit,
    enabled: Boolean,
) {
    when (kind) {
        VideoKind.REEL -> Row(
            modifier = Modifier
                .fillMaxWidth()
                .height(PREVIEW_HEIGHT),
        ) {
            CoverPreview(
                cover = cover,
                modifier = Modifier
                    .width(PREVIEW_WIDTH)
                    .fillMaxHeight(),
            )
            Spacer(Modifier.width(UsTheme.spacing.l))
            DescriptionField(
                value = caption,
                onValueChange = onCaptionChanged,
                enabled = enabled,
                placeholder = "Describe your reel… #hashtags @mentions",
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight(),
            )
        }
        VideoKind.LONG -> Column(modifier = Modifier.fillMaxWidth()) {
            CoverPreview(
                cover = cover,
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(LANDSCAPE),
            )
            Spacer(Modifier.height(UsTheme.spacing.l))
            DescriptionField(
                value = caption,
                onValueChange = onCaptionChanged,
                enabled = enabled,
                placeholder = "Describe your video… #hashtags @mentions",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(LONG_DESCRIPTION_HEIGHT),
            )
        }
    }
}

@Composable
private fun CoverPreview(cover: CoverFrame?, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val image = remember(cover?.bitmap) { cover?.bitmap?.asImageBitmap() }
    Box(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .semantics { contentDescription = "Cover preview" }
            .testTag("reel-cover-preview"),
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
    }
}

/**
 * The description, with `#tags` and `@people` lit in the accent as they
 * are typed. A bare `BasicTextField`: the composer's reasoning applies —
 * a box around the text makes it a form to fill in.
 */
@Composable
private fun DescriptionField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    placeholder: String,
    modifier: Modifier = Modifier,
) {
    val accent = UsTheme.extended.accentSolid
    val transformation = remember(accent) { HashtagMentionTransformation(accent) }
    Box(modifier = modifier) {
        if (value.isEmpty()) {
            Text(
                text = placeholder,
                style = MaterialTheme.typography.bodyLarge.copy(
                    fontSize = DESCRIPTION_SIZE,
                    lineHeight = DESCRIPTION_LINE_HEIGHT,
                ),
                color = UsTheme.extended.textDim,
            )
        }
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            visualTransformation = transformation,
            textStyle = MaterialTheme.typography.bodyLarge.copy(
                fontSize = DESCRIPTION_SIZE,
                lineHeight = DESCRIPTION_LINE_HEIGHT,
                color = UsTheme.extended.textPrimary,
            ),
            cursorBrush = SolidColor(accent),
            modifier = Modifier
                .fillMaxSize()
                .semantics { contentDescription = "Description" }
                .testTag("reel-caption"),
        )
    }
}

// ── Cover strip ─────────────────────────────────────────────────────────

/** Six frames, evenly spaced, in the kind's aspect; the chosen one carries the accent ring. */
@Composable
private fun CoverStrip(
    kind: VideoKind,
    frames: List<CoverFrame>,
    loading: Boolean,
    coverIndex: Int,
    enabled: Boolean,
    onSelect: (Int) -> Unit,
    onChangeVideo: () -> Unit,
) {
    Column {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = if (loading) "Finding cover frames…" else "Cover",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.weight(1f),
            )
            val interaction = remember { MutableInteractionSource() }
            Text(
                text = "Change video",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = if (enabled) UsTheme.extended.accentSolid else UsTheme.extended.textGhost,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        enabled = enabled,
                        onClick = onChangeVideo,
                    )
                    .pressDim(interaction)
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
                    .testTag("reel-change-video"),
            )
        }
        Spacer(Modifier.height(UsTheme.spacing.s))
        val aspect = if (kind == VideoKind.LONG) LANDSCAPE else PORTRAIT
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            if (frames.isEmpty()) {
                repeat(STRIP_SLOTS) {
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .aspectRatio(aspect)
                            .clip(RoundedCornerShape(UsTheme.radii.small))
                            .background(UsTheme.extended.bgCard),
                    )
                }
            } else {
                frames.forEach { frame ->
                    CoverFrameTile(
                        frame = frame,
                        aspect = aspect,
                        selected = frame.index == coverIndex,
                        enabled = enabled && frame.bitmap != null,
                        onClick = { onSelect(frame.index) },
                        modifier = Modifier.weight(1f),
                    )
                }
            }
        }
    }
}

@Composable
private fun CoverFrameTile(
    frame: CoverFrame,
    aspect: Float,
    selected: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    val image = remember(frame.bitmap) { frame.bitmap?.asImageBitmap() }
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(targetValue = if (pressed) PRESS_SCALE else 1f, label = "framePress")
    Box(
        modifier = modifier
            .aspectRatio(aspect)
            .scale(scale)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(
                width = if (selected) RING_WIDTH else HAIRLINE,
                color = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle,
                shape = shape,
            )
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .semantics {
                role = Role.RadioButton
                this.selected = selected
                contentDescription = "Cover frame ${frame.index + 1}"
            }
            .testTag("reel-cover-${frame.index}"),
    ) {
        if (image != null) {
            Image(
                bitmap = image,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier
                    .fillMaxSize()
                    .alpha(if (selected) 1f else UNSELECTED_ALPHA),
            )
        }
    }
}

// ── Details card ────────────────────────────────────────────────────────

@Composable
private fun DetailsCard(
    state: ReelPublishViewModel.ReelUiState,
    enabled: Boolean,
    onTagPeople: () -> Unit,
    onUntag: (String) -> Unit,
    onLocation: () -> Unit,
    onAudience: () -> Unit,
    onCategory: () -> Unit,
) {
    GlassCard {
        DetailRow(
            icon = UsIcons.UserPlus,
            title = "Tag people",
            value = state.taggedUsers.size.takeIf { it > 0 }?.let { "$it tagged" }.orEmpty(),
            enabled = enabled,
            onClick = onTagPeople,
            testTag = "reel-tag-people-row",
        )
        if (state.taggedUsers.isNotEmpty()) {
            TaggedChips(
                users = state.taggedUsers,
                onRemove = onUntag,
                modifier = Modifier.padding(start = ROW_PADDING_H, end = ROW_PADDING_H, bottom = UsTheme.spacing.l),
            )
        }
        RowDivider()
        DetailRow(
            icon = UsIcons.MapPin,
            title = "Add location",
            value = state.locationName,
            enabled = enabled,
            onClick = onLocation,
            testTag = "reel-location-row",
        )
        RowDivider()
        DetailRow(
            icon = UsIcons.Globe,
            title = "Audience",
            value = AudienceOptions.firstOrNull { it.value == state.visibility }?.label.orEmpty(),
            enabled = enabled,
            onClick = onAudience,
            testTag = "reel-audience-row",
        )
        RowDivider()
        DetailRow(
            icon = UsIcons.Tag,
            title = "Category",
            value = state.categories.firstOrNull { it.id == state.category }?.label ?: "None",
            enabled = enabled,
            onClick = onCategory,
            testTag = "reel-category-row",
        )
    }
}

/** Icon · title · value · chevron. Press dims the row; nothing draws a square. */
@Composable
private fun DetailRow(
    icon: ImageVector,
    title: String,
    value: String,
    enabled: Boolean,
    onClick: () -> Unit,
    testTag: String,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = ROW_PADDING_H, vertical = ROW_PADDING_V)
            .semantics {
                role = Role.Button
                contentDescription = if (value.isBlank()) title else "$title, $value"
            }
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(ROW_ICON),
        )
        Text(
            text = title,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        if (value.isNotBlank()) {
            Text(
                text = value,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.widthIn(max = ROW_VALUE_MAX),
            )
        }
        Icon(
            imageVector = UsIcons.ChevronRight,
            contentDescription = null,
            tint = UsTheme.extended.textDim,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

// ── Switches card ───────────────────────────────────────────────────────

/**
 * Exactly these four, in this order — the founder's list. A long video has
 * no remix: the server keeps no `remix_setting` for long form, so the row
 * is absent rather than a switch that records nothing.
 */
@Composable
private fun SwitchesCard(
    state: ReelPublishViewModel.ReelUiState,
    viewModel: ReelPublishViewModel,
    enabled: Boolean,
) {
    GlassCard {
        ReelSwitchRow(
            title = "Allow comments",
            checked = state.allowComments,
            onCheckedChange = viewModel::onAllowCommentsChanged,
            enabled = enabled,
            testTag = "reel-allow-comments",
        )
        RowDivider()
        ReelSwitchRow(
            title = "Hide share button",
            checked = state.hideShare,
            onCheckedChange = viewModel::onHideShareChanged,
            enabled = enabled,
            testTag = "reel-hide-share",
        )
        RowDivider()
        ReelSwitchRow(
            title = "Allow download",
            checked = state.allowDownload,
            onCheckedChange = viewModel::onAllowDownloadChanged,
            enabled = enabled,
            testTag = "reel-allow-download",
        )
        if (state.kind == VideoKind.REEL) {
            RowDivider()
            ReelSwitchRow(
                title = "Allow remix",
                checked = state.allowRemix,
                onCheckedChange = viewModel::onAllowRemixChanged,
                enabled = enabled,
                testTag = "reel-allow-remix",
            )
        }
    }
}

/** A settings switch row, compacted: the whole row toggles, no ripple. */
@Composable
private fun ReelSwitchRow(
    title: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    enabled: Boolean,
    testTag: String,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(
                interactionSource = interaction,
                indication = null,
                enabled = enabled,
                role = Role.Switch,
                onClick = { onCheckedChange(!checked) },
            )
            .pressDim(interaction)
            .padding(horizontal = ROW_PADDING_H, vertical = SWITCH_PADDING_V)
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            enabled = enabled,
            colors = usSwitchColors(),
            modifier = Modifier.scale(SWITCH_SCALE),
        )
    }
}

// ── Title (long video) ──────────────────────────────────────────────────

/**
 * The long video's title: required, 100 characters, the counter at the
 * right. A bare field on the same reasoning as the description — a box
 * makes it a form — with a hairline under it so it reads as its own line
 * above the cover rather than a first line of the description.
 */
@Composable
private fun TitleField(
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
) {
    val accent = UsTheme.extended.accentSolid
    Column(modifier = Modifier.fillMaxWidth()) {
        Box(modifier = Modifier.fillMaxWidth()) {
            if (value.isEmpty()) {
                Text(
                    text = "Title",
                    style = MaterialTheme.typography.titleMedium.copy(fontSize = TITLE_SIZE),
                    color = UsTheme.extended.textDim,
                )
            }
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                enabled = enabled,
                singleLine = true,
                textStyle = MaterialTheme.typography.titleMedium.copy(
                    fontSize = TITLE_SIZE,
                    color = UsTheme.extended.textPrimary,
                ),
                cursorBrush = SolidColor(accent),
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Title" }
                    .testTag("video-title"),
            )
        }
        Spacer(Modifier.height(UsTheme.spacing.s))
        HorizontalDivider(color = UsTheme.extended.borderSubtle, thickness = HAIRLINE)
        Spacer(Modifier.height(UsTheme.spacing.xs))
        Text(
            text = "${value.length}/${ReelPublishViewModel.MAX_TITLE_LENGTH}",
            style = MaterialTheme.typography.labelMedium,
            color = if (value.isBlank()) UsTheme.extended.textDim else UsTheme.extended.textMuted,
            modifier = Modifier
                .align(Alignment.End)
                .testTag("video-title-count"),
        )
    }
}

// ── The gate ────────────────────────────────────────────────────────────

/**
 * What stops a post, said inline under the cover strip (founder,
 * 2026-09-05): a reel over five minutes offers "Post as a Video" — the
 * same video, cover and fields, as a long video — and a file over 500 MB
 * says so and offers nothing, because no kind can take it. Nothing is
 * drawn while the gate is open.
 */
@Composable
private fun GateNotice(
    gate: VideoGate,
    enabled: Boolean,
    onSwitchToLong: () -> Unit,
) {
    if (gate is VideoGate.Ok) return
    Spacer(Modifier.height(UsTheme.spacing.l))
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, RoundedCornerShape(UsTheme.radii.medium))
            .padding(horizontal = ROW_PADDING_H, vertical = UsTheme.spacing.l)
            .testTag("video-gate"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = gateMessage(gate),
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        if (gate is VideoGate.TooLongForReel) {
            UsPillButton(
                text = "Post as a Video",
                onClick = onSwitchToLong,
                enabled = enabled,
                modifier = Modifier.testTag("video-gate-switch"),
            )
        }
    }
}

/** The gate's one sentence, shared by the notice and the Post pill's description. */
internal fun gateMessage(gate: VideoGate): String = when (gate) {
    VideoGate.Ok -> ""
    is VideoGate.TooLongForReel -> "Reels are up to 5 minutes. Post it as a Video instead"
    is VideoGate.TooLarge -> "Videos are up to 500 MB. Pick a smaller file."
}

// ── Status ──────────────────────────────────────────────────────────────

/**
 * Only a failure has anything to say here now: the upload
 * and the post itself happen after this surface has closed, on the Reels
 * tab's pending item. The pill's spinner covers the moment before hand-off.
 */
@Composable
private fun PhaseStatus(phase: Phase) {
    if (phase !is Phase.Failure) return
    Spacer(Modifier.height(UsTheme.spacing.xxl))
    Text(
        text = phase.message,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier.testTag("reel-failure"),
    )
}

// ── Shared pieces ───────────────────────────────────────────────────────

/** The Create sheet's tile surface as a grouped card. */
@Composable
private fun GlassCard(content: @Composable () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape),
    ) {
        content()
    }
}

@Composable
private fun RowDivider() {
    HorizontalDivider(
        color = UsTheme.extended.borderSubtle,
        thickness = HAIRLINE,
        modifier = Modifier.padding(start = ROW_PADDING_H + ROW_ICON + UsTheme.spacing.l),
    )
}

/**
 * The press feedback this form uses instead of a ripple: the pressed thing
 * dims to 60% and comes back. Rows, links and options all share it, so a
 * finger anywhere on the form gets the same answer.
 */
@Composable
internal fun Modifier.pressDim(interaction: MutableInteractionSource): Modifier {
    val pressed by interaction.collectIsPressedAsState()
    val alpha by animateFloatAsState(targetValue = if (pressed) PRESS_ALPHA else 1f, label = "pressDim")
    return graphicsLayer { this.alpha = alpha }
}

private val AudienceOptions = listOf(
    ReelOption(VISIBILITY_PUBLIC, "Public", "Anyone on Momentum"),
    ReelOption(VISIBILITY_FOLLOWERS, "Followers", "People who follow you"),
    ReelOption(VISIBILITY_PRIVATE, "Private", "Only you"),
)

// ── Metrics ─────────────────────────────────────────────────────────────

private const val STRIP_SLOTS = 6
private const val PORTRAIT = 9f / 16f
private const val LANDSCAPE = 16f / 9f
private const val PRESS_SCALE = 0.94f
private const val PRESS_ALPHA = 0.6f
private const val UNSELECTED_ALPHA = 0.55f
private const val SWITCH_SCALE = 0.8f

private val HAIRLINE = 1.dp
private val RING_WIDTH = 2.dp
private val PREVIEW_WIDTH = 96.dp
private val PREVIEW_HEIGHT = 170.dp
private val PREVIEW_GLYPH = 22.dp
private val LONG_DESCRIPTION_HEIGHT = 96.dp
private val TITLE_SIZE = 17.sp
private val ROW_PADDING_H = 14.dp
private val ROW_PADDING_V = 13.dp
private val SWITCH_PADDING_V = 4.dp
private val ROW_ICON = 18.dp
private val ROW_VALUE_MAX = 150.dp
private val CHEVRON = 16.dp
private val DESCRIPTION_SIZE = 15.sp
private val DESCRIPTION_LINE_HEIGHT = 21.sp
