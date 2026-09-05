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
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.util.UnstableApi
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelGate
import com.us.android.core.feed.data.channelGate
import com.us.android.core.feed.ui.channel.CreateChannelSheet
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.usSwitchColors
import com.us.android.feature.post.createhub.ReelPublishViewModel.Phase
import com.us.android.feature.post.createhub.studio.ReelStudioScreen
import com.us.android.feature.post.createhub.studio.ReelStudioViewModel
import com.us.android.feature.post.createhub.studio.StudioActions
import com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS
import com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC

/**
 * REEL and VIDEO — pick a video, then the TikTok-shaped form.
 *
 * Cover preview beside the description (a reel) or above it (a video), a
 * "Choose cover" pill that opens the exact-frame picker, then one card of
 * rows (Tag people, Add location, Audience, Category) and one card of
 * switches. Everything scrolls; the keyboard is an inset the root column
 * pads for, so the description is never under it.
 *
 * Opened from the Video tile (Tube, 2026-09-05) the same form is a LONG
 * video: a required title on top, a 16:9 cover, no remix switch — and a
 * channel first: without one the "Create your channel" sheet opens over
 * the picker and the form continues once it exists. The ViewModel reads
 * which kind from the route; a reel over five minutes can switch to a
 * video in place ([GateNotice]).
 *
 * A REEL goes through the studio first (2026-09-05): pick, edit — frame,
 * trim, speed, look, text — export, and only then this form, over the
 * exported file. Its caption is just the caption: hashtags are chips in a
 * field of their own, mentions are the people picked, and beside Post
 * sits Schedule.
 *
 * Post hands the publish to WorkManager and LEAVES onto the viewer's own
 * profile, whose grid shows the posting video first with its ring; several
 * may be pending at once. There is no published callback here because
 * nothing is published while this surface exists.
 */
@OptIn(UnstableApi::class)
@Composable
internal fun ReelSurface(
    onClose: () -> Unit,
    onOpenOwnProfile: () -> Unit,
    viewModel: ReelPublishViewModel = hiltViewModel(),
    studio: ReelStudioViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val studioState by studio.state.collectAsStateWithLifecycle()
    val long = state.kind == VideoKind.LONG

    // A reel goes through the studio first; a long video is picked as is.
    val sources = rememberVideoSources(onSource = if (long) viewModel::onVideoPicked else studio::setSource)

    // The studio rendered: the details step takes the file, and the studio forgets it.
    studioState.exportedPath?.let { path ->
        LaunchedEffect(path) {
            viewModel.onReelExported(path)
            studio.consumeExported()
        }
    }

    if (state.phase is Phase.Enqueued) {
        // Handed to the worker: leave, once, onto the viewer's own profile,
        // whose grid shows the pending tile with its ring (founder, 2026-09-05).
        LaunchedEffect(Unit) { onOpenOwnProfile() }
    }

    val gate = if (long) channelGate(state.channel) else ChannelGate.Proceed
    when {
        gate is ChannelGate.Blocked -> UsErrorState(message = gate.message, onRetry = viewModel::retryChannel)
        state.videoUri == null && !long && studioState.sourceUri != null -> ReelStudioScreen(
            state = studioState,
            actions = studioActions(studio),
            onClose = studio::clear,
            onNext = { studio.startExport(viewModel.exportTargetPath()) },
        )
        state.videoUri == null -> VideoSourceStep(long = long, sources = sources, onClose = onClose)
        else -> ReelForm(
            state = state,
            viewModel = viewModel,
            onClose = onClose,
            onChangeVideo = {
                viewModel.clearVideo()
                studio.clear()
                if (long) sources.openSystemPicker()
            },
        )
    }

    // Channel before video (founder, 2026-09-05): the sheet comes first, over
    // whichever step the form is on. Dismissing it without a channel leaves
    // the surface — a video cannot post without one.
    if (gate is ChannelGate.CreateFirst) {
        CreateChannelSheet(onCreated = {}, onDismiss = onClose)
    }
}

/** The three ways a video comes in: the in-app gallery's pick, the camera, the system picker. */
private class VideoSources(
    val onPicked: (String) -> Unit,
    val openCamera: () -> Unit,
    val openSystemPicker: () -> Unit,
)

@Composable
private fun rememberVideoSources(onSource: (String) -> Unit): VideoSources {
    val context = LocalContext.current
    val latest by rememberUpdatedState(onSource)
    var cameraTarget by remember { mutableStateOf<android.net.Uri?>(null) }
    val pickVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri -> uri?.let { latest(it.toString()) } }
    val captureVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.CaptureVideo(),
    ) { saved -> if (saved) cameraTarget?.let { latest(it.toString()) } }
    val openCamera = rememberCameraLaunch {
        val target = captureUri(context, "mp4")
        cameraTarget = target
        captureVideo.launch(target)
    }
    return remember(openCamera) {
        VideoSources(
            onPicked = { uri -> latest(uri) },
            openCamera = openCamera,
            openSystemPicker = {
                pickVideo.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.VideoOnly))
            },
        )
    }
}

/** The in-app gallery over videos: one tap picks; Camera and Browse lead to the other two sources. */
@Composable
private fun VideoSourceStep(long: Boolean, sources: VideoSources, onClose: () -> Unit) {
    MediaGallerySurface(
        kind = GalleryKind.Videos,
        title = if (long) "New video" else "New reel",
        subtitle = if (long) "Pick a video — it posts to Tube." else "Pick a video — the studio opens next.",
        onClose = onClose,
        onCamera = sources.openCamera,
        onPicked = { uris -> uris.firstOrNull()?.let { sources.onPicked(it.toString()) } },
        onSystemPicker = sources.openSystemPicker,
    )
}

/** The studio's verbs, bound to its ViewModel. */
private fun studioActions(studio: ReelStudioViewModel) = StudioActions(
    selectTool = studio::selectTool,
    togglePlaying = studio::togglePlaying,
    setPlaying = studio::setPlaying,
    setMode = studio::setMode,
    pan = studio::pan,
    setTrimStart = studio::setTrimStart,
    setTrimEnd = studio::setTrimEnd,
    setSpeed = studio::setSpeed,
    setLook = studio::setLook,
    setText = studio::setText,
    setTextStyle = studio::setTextStyle,
    moveText = studio::moveText,
    removeText = studio::removeText,
    cancelExport = studio::cancelExport,
    dismissExportError = studio::dismissExportError,
)

private enum class ReelSheet { None, Audience, Category, Location, People, Schedule }

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
            CreateTopBar(title = if (long) "New video" else "New reel", onClose = onClose)
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
                    onOpenPicker = viewModel::openCoverPicker,
                    enabled = editable,
                )
                Spacer(Modifier.height(UsTheme.spacing.l))
                CoverActions(
                    loading = state.framesLoading,
                    source = state.coverSource,
                    enabled = editable,
                    onChooseCover = viewModel::openCoverPicker,
                    onChangeVideo = onChangeVideo,
                )
                GateNotice(gate = state.gate, enabled = editable, onSwitchToLong = viewModel::switchToLong)
                Spacer(Modifier.height(UsTheme.spacing.xxl))
                HashtagsField(
                    hashtags = state.hashtags,
                    input = state.hashtagInput,
                    suggestions = state.hashtagSuggestions,
                    enabled = editable,
                    actions = HashtagActions(
                        onInputChanged = viewModel::onHashtagInputChanged,
                        onCommit = { viewModel.commitHashtags() },
                        onRemove = viewModel::removeHashtag,
                        onPickSuggestion = viewModel::onHashtagSuggestionPicked,
                    ),
                )
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
                Spacer(Modifier.height(UsTheme.spacing.xxl))
            }
            PostActions(
                publishAt = state.publishAt,
                canPost = state.canPost,
                busy = state.isBusy,
                retrying = state.phase is Phase.Failure,
                onSchedule = { sheet = ReelSheet.Schedule },
                onPost = viewModel::onPost,
                description = postDescription(state, noun),
            )
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
        state.picker?.let { picker ->
            CoverPickerScreen(
                kind = state.kind,
                picker = picker,
                frames = state.frames,
                durationUs = state.durationUs,
                actions = CoverPickerActions(
                    onScrub = viewModel::onScrub,
                    onConfirm = viewModel::confirmCover,
                    onUpload = viewModel::onCoverImagePicked,
                    onClose = viewModel::closeCoverPicker,
                ),
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
        ReelSheet.Schedule -> ScheduleSheet(
            initial = state.publishAt,
            onSchedule = {
                viewModel.onScheduleChanged(it)
                onClose()
            },
            onClear = {
                viewModel.onScheduleChanged(null)
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
 * The cover itself is a button: tapping it opens the picker.
 */
@Composable
private fun CoverAndDescription(
    kind: VideoKind,
    cover: CoverFrame?,
    caption: String,
    onCaptionChanged: (String) -> Unit,
    onOpenPicker: () -> Unit,
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
                onClick = onOpenPicker,
                enabled = enabled,
                modifier = Modifier
                    .width(PREVIEW_WIDTH)
                    .fillMaxHeight(),
            )
            Spacer(Modifier.width(UsTheme.spacing.l))
            DescriptionField(
                value = caption,
                onValueChange = onCaptionChanged,
                enabled = enabled,
                placeholder = "Describe your reel…",
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight(),
            )
        }
        VideoKind.LONG -> Column(modifier = Modifier.fillMaxWidth()) {
            CoverPreview(
                cover = cover,
                onClick = onOpenPicker,
                enabled = enabled,
                modifier = Modifier
                    .fillMaxWidth()
                    .aspectRatio(LANDSCAPE),
            )
            Spacer(Modifier.height(UsTheme.spacing.l))
            DescriptionField(
                value = caption,
                onValueChange = onCaptionChanged,
                enabled = enabled,
                placeholder = "Describe your video…",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(LONG_DESCRIPTION_HEIGHT),
            )
        }
    }
}

@Composable
private fun CoverPreview(cover: CoverFrame?, onClick: () -> Unit, enabled: Boolean, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val image = remember(cover?.bitmap) { cover?.bitmap?.asImageBitmap() }
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(targetValue = if (pressed) PRESS_SCALE else 1f, label = "coverPress")
    Box(
        modifier = modifier
            .scale(scale)
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .semantics {
                role = Role.Button
                contentDescription = "Cover preview. Choose a cover"
            }
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

// ── Cover actions ───────────────────────────────────────────────────────

/** "Cover · from the video / uploaded", then "Choose cover" and "Change video". */
@Composable
private fun CoverActions(
    loading: Boolean,
    source: ReelPublishViewModel.CoverSource,
    enabled: Boolean,
    onChooseCover: () -> Unit,
    onChangeVideo: () -> Unit,
) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = when {
                loading -> "Finding cover frames…"
                source == ReelPublishViewModel.CoverSource.Upload -> "Cover · uploaded"
                else -> "Cover · from the video"
            },
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        TextAction(label = "Choose cover", enabled = enabled, onClick = onChooseCover, testTag = "reel-choose-cover")
        TextAction(label = "Change video", enabled = enabled, onClick = onChangeVideo, testTag = "reel-change-video")
    }
}

@Composable
private fun TextAction(label: String, enabled: Boolean, onClick: () -> Unit, testTag: String) {
    val interaction = remember { MutableInteractionSource() }
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.SemiBold,
        color = if (enabled) UsTheme.extended.textPrimary else UsTheme.extended.textGhost,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s)
            .semantics { role = Role.Button }
            .testTag(testTag),
    )
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
            icon = UsIcons.AtSign,
            title = "Mention people",
            value = state.taggedUsers.size.takeIf { it > 0 }?.let { "$it mentioned" }.orEmpty(),
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
 * What stops a post, said inline under the cover row (founder,
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
 * and the post itself happen after this surface has closed, on the
 * pending tile. The pill's spinner covers the moment before hand-off.
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

private const val LANDSCAPE = 16f / 9f
private const val PRESS_SCALE = 0.97f
private const val PRESS_ALPHA = 0.6f
private const val SWITCH_SCALE = 0.8f

private val HAIRLINE = 1.dp
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
