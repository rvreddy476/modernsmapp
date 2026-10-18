package com.us.android.feature.post.studio

import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Slider
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.creator.model.Adjustments
import com.us.android.core.creator.model.AdjustmentsMath
import com.us.android.core.creator.model.Crop
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.creator.CreatorFonts
import com.us.android.core.ui.LightSystemBarGlyphs
import com.us.android.core.ui.photoeditor.rememberPhotoEditor
import sh.calvin.reorderable.ReorderableItem
import sh.calvin.reorderable.rememberReorderableLazyListState
import java.io.File

/**
 * The Post Studio — the Instagram-pattern two-step flow.
 *
 * ## STEP 1: EDIT
 *
 * The photo IS the screen: black canvas, media full-bleed, tools as sheets
 * over it (Filter / Edit / Text / Ratio / Alt — nothing that isn't functional).
 * Bottom-left: draggable page thumbnails plus an add tile. Bottom-right: Next.
 *
 * ## STEP 2: SHARE
 *
 * A quiet settings list: preview, caption, language, the accessibility gate's
 * status, and one full-width Share. Next is ALWAYS available; Share is what the
 * alt gate blocks — deciding descriptions can happen on either step.
 *
 * ## WHAT THE PREVIEW CLAIMS
 *
 * The preview approximates with the same math the exporter uses (same crop
 * rect, same color matrix formula, same fonts); the exported pixels are
 * produced by the render exporter behind the engine port, and the
 * device-golden tolerance remains the closure proof that the two agree.
 */
@Composable
fun StudioScreen(
    onClose: () -> Unit,
    /**
     * The post is the worker's now: leave the studio for the viewer's own
     * profile, where the grid draws the upload's progress (founder,
     * 2026-09-06). There is no published callback because nothing is published
     * while this screen exists — the same shape the reel surface uses.
     */
    onOpenOwnProfile: () -> Unit,
    /** The post was saved as a server draft (2026-10-18): the host opens the Drafts list. */
    onDraftSaved: () -> Unit,
    viewModel: StudioViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }

    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PICK),
    ) { uris -> if (uris.isNotEmpty()) viewModel.onImagesPicked(uris) }
    val launchPicker = {
        pickImages.launch(
            PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
        )
    }
    // The advanced (licensed) photo editor over the selected page: null, and
    // so no Edit pill, unless its licence is Ready.
    val editPhoto = rememberPhotoEditor(
        editor = viewModel.advancedEditor,
        onEdited = viewModel::onSelectedPageEdited,
        onFailed = viewModel::onPhotoEditFailed,
    )

    LaunchedEffect(state.notice) {
        state.notice?.let {
            snackbar.showSnackbar(it)
            viewModel.onNoticeShown()
        }
    }
    // Once, on hand-off. `handedOff` never goes back to false, so keying the
    // effect on it means the studio leaves exactly once however often the rest
    // of the state changes on the way out.
    LaunchedEffect(state.handedOff) {
        if (state.handedOff) onOpenOwnProfile()
    }
    LaunchedEffect(state.draftSaved) {
        if (state.draftSaved) onDraftSaved()
    }

    // System Back walks the flow backwards before it leaves it.
    BackHandler(enabled = state.step == StudioViewModel.Step.Share) {
        viewModel.onBackToEdit()
    }

    when (state.step) {
        // 2026-10-02: the edit canvas is a stage (media first), dark in BOTH themes, so its
        // chrome and its Material controls take the dark theme whatever the device says.
        StudioViewModel.Step.Edit -> UsTheme(darkTheme = true) {
            LightSystemBarGlyphs()
            EditStep(
                state = state,
                viewModel = viewModel,
                onClose = onClose,
                onAddPhotos = launchPicker,
                onEditPhoto = editPhoto,
            )
        }
        StudioViewModel.Step.Share -> ShareStep(state = state, viewModel = viewModel, onAddPhotos = launchPicker)
    }
    SnackbarHost(hostState = snackbar)
}

// ════════════════════════════════════════════════════════════════════════
// STEP 1 — EDIT
// ════════════════════════════════════════════════════════════════════════

private enum class StudioTool(val label: String) {
    Filter("Filter"),
    Edit("Edit"),
    Text("Text"),
    Ratio("Ratio"),
    Alt("Alt"),
}

// LongMethod: one screen, one composable — the same trade PostCard documents.
@Suppress("LongMethod")
@Composable
private fun EditStep(
    state: StudioViewModel.StudioUiState,
    viewModel: StudioViewModel,
    onClose: () -> Unit,
    onAddPhotos: () -> Unit,
    /** Opens the advanced editor on an image; null when it is not licensed, and then there is no pill. */
    onEditPhoto: ((Uri) -> Unit)?,
) {
    var tool by remember { mutableStateOf<StudioTool?>(null) }
    // While a look tool is open the preview follows this DRAFT; Done commits it
    // as one reducer command, Cancel simply drops it. The document never holds
    // values the user walked away from.
    var draft by remember { mutableStateOf<Adjustments?>(null) }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.stage)
            .testTag("studio"),
    ) {
        // The shell leaves the studio both bars (2026-10-19); the stage runs
        // under them and the controls pad themselves clear.
        Column(modifier = Modifier.fillMaxSize().systemBarsPadding()) {
            // Which editing tools this studio offers.
            //
            // With the advanced editor available, the studio's own Filter,
            // Edit and Text duplicate it — two things labelled "Edit" a
            // centimetre apart, doing different jobs, and neither one obviously
            // the real one. The advanced editor is the editor; the studio
            // frames the picture, describes it and posts it.
            //
            // Ratio and Alt stay either way: the crop is the studio's own
            // (the editor exports a picture, not a frame) and alt text is
            // accessibility, which no image editor gives back.
            val advanced = onEditPhoto != null
            val availableTools = remember(advanced) {
                if (advanced) listOf(StudioTool.Ratio, StudioTool.Alt) else StudioTool.entries.toList()
            }
            EditTopBar(
                canUndo = state.canUndo,
                canRedo = state.canRedo,
                onUndo = viewModel::onUndo,
                onRedo = viewModel::onRedo,
                onReset = viewModel::onReset,
                onClose = onClose,
                onEditPhoto = editSelected(onEditPhoto, state.selectedPage),
            )

            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth(),
                contentAlignment = Alignment.Center,
            ) {
                state.selectedPage?.let { page ->
                    PageCanvas(page = page, adjustmentsOverride = draft)
                } ?: EmptyStudio(onAddPhotos = onAddPhotos)
            }

            val page = state.selectedPage
            if (page != null) {
                when (tool) {
                    null -> ToolChipsRow(tools = availableTools, onOpen = { tool = it })
                    StudioTool.Filter -> FilterSheet(
                        page = page,
                        draft = draft ?: page.adjustments,
                        onDraft = { draft = it },
                        onCancel = {
                            draft = null
                            tool = null
                        },
                        onDone = {
                            draft?.let(viewModel::onAdjust)
                            draft = null
                            tool = null
                        },
                    )
                    StudioTool.Edit -> AdjustSheet(
                        draft = draft ?: page.adjustments,
                        onDraft = { draft = it },
                        onCancel = {
                            draft = null
                            tool = null
                        },
                        onDone = {
                            draft?.let(viewModel::onAdjust)
                            draft = null
                            tool = null
                        },
                    )
                    StudioTool.Ratio -> RatioSheet(
                        page = page,
                        viewModel = viewModel,
                        onDone = { tool = null },
                    )
                    StudioTool.Alt -> AltSheet(
                        page = page,
                        viewModel = viewModel,
                        onDone = { tool = null },
                    )
                    // Rendered as a full-screen overlay below, not a sheet.
                    StudioTool.Text -> Unit
                }
            }

            ThumbStripRow(
                state = state,
                viewModel = viewModel,
                onAddPhotos = onAddPhotos,
            )
        }

        if (tool == StudioTool.Text) {
            state.selectedPage?.let { page ->
                TextOverlayEditor(
                    page = page,
                    viewModel = viewModel,
                    onClose = { tool = null },
                )
            }
        }
    }
}

@Suppress("LongParameterList")
@Composable
private fun EditTopBar(
    canUndo: Boolean,
    canRedo: Boolean,
    onUndo: () -> Unit,
    onRedo: () -> Unit,
    onReset: () -> Unit,
    onClose: () -> Unit,
    onEditPhoto: (() -> Unit)?,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onClose) {
            Icon(UsIcons.Close, contentDescription = "Close the studio", tint = UsTheme.extended.onMedia)
        }
        if (onEditPhoto != null) {
            AdvancedEditPill(onClick = onEditPhoto)
        }
        Spacer(Modifier.weight(1f))
        TextButton(onClick = onUndo, enabled = canUndo) {
            Text("Undo", color = if (canUndo) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim)
        }
        TextButton(onClick = onRedo, enabled = canRedo) {
            Text("Redo", color = if (canRedo) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim)
        }
        TextButton(onClick = onReset, enabled = canUndo) {
            Text("Reset", color = if (canUndo) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim)
        }
    }
}

/** The pill's action over the selected page — null with no editor or no page, and then no pill. */
private fun editSelected(editPhoto: ((Uri) -> Unit)?, page: StudioViewModel.PageUi?): (() -> Unit)? =
    if (editPhoto != null && page != null) {
        { editPhoto(Uri.fromFile(File(page.sourcePath))) }
    } else {
        null
    }

/**
 * The advanced editor's entry — a pill on the canvas chrome with the sliders
 * glyph. It is composed only while the editor is licensed; there is never a
 * disabled one.
 */
@Composable
private fun AdvancedEditPill(onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.mediaRim)
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .semantics { contentDescription = "Edit this photo in the advanced editor" }
            .testTag("studio-advanced-edit"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(
            UsIcons.Sliders,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
            modifier = Modifier.size(PILL_ICON),
        )
        Text("Edit", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.onMedia)
    }
}

/** The full-bleed live preview: crop, rotation, adjustments and text — the exporter's math. */
@Composable
private fun PageCanvas(
    page: StudioViewModel.PageUi,
    adjustmentsOverride: Adjustments?,
) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(CANVAS_ASPECT)
            .background(UsTheme.extended.stage)
            .clipToBounds()
            .testTag("studio-preview"),
    ) {
        val crop = page.crop
        val scaleX = MICROS_F / crop.wMicros
        val scaleY = MICROS_F / crop.hMicros
        AsyncImage(
            model = File(page.sourcePath),
            contentDescription = page.altText.takeIf { it.isNotBlank() },
            contentScale = ContentScale.FillBounds,
            colorFilter = ColorFilter.colorMatrix(
                adjustmentsMatrix(adjustmentsOverride ?: page.adjustments),
            ),
            modifier = Modifier
                .fillMaxSize()
                .graphicsLayer {
                    rotationZ = page.rotationDegMicros / MICROS_F
                    this.scaleX = scaleX
                    this.scaleY = scaleY
                    translationX =
                        size.width * scaleX * (HALF_MICROS - crop.xMicros - crop.wMicros / 2) / MICROS_F
                    translationY =
                        size.height * scaleY * (HALF_MICROS - crop.yMicros - crop.hMicros / 2) / MICROS_F
                },
        )
        page.textLayers.forEach { layer ->
            val typeface = CreatorFonts.typeface(LocalContext.current, layer.fontAssetId)
            Text(
                text = layer.value,
                color = parseArgb(layer.colorArgb),
                fontSize = (layer.sizeCanvasMicros / TEXT_SIZE_DIVISOR).sp,
                fontFamily = typeface?.let { FontFamily(it) },
                modifier = Modifier.align(Alignment.Center),
            )
        }
    }
}

/**
 * The exporter's exact adjustment math — [AdjustmentsMath] is the ONE
 * definition both sides share, so preview and export agree by construction.
 */
private fun adjustmentsMatrix(adjustments: Adjustments): ColorMatrix =
    ColorMatrix(AdjustmentsMath.matrix(adjustments))

private fun parseArgb(argb: String): Color =
    runCatching { Color(android.graphics.Color.parseColor(argb)) }.getOrDefault(Color.Unspecified)

// ── Tool chips ──────────────────────────────────────────────────────────

@Composable
private fun ToolChipsRow(tools: List<StudioTool>, onOpen: (StudioTool) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        tools.forEach { tool ->
            Box(
                modifier = Modifier
                    .weight(1f)
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.mediaRim)
                    .clickable { onOpen(tool) }
                    .padding(vertical = UsTheme.spacing.m)
                    .testTag("studio-tool-${tool.label.lowercase()}"),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    tool.label,
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.onMedia,
                )
            }
        }
    }
}

/** The Cancel / title / Done header every sheet shares. */
@Composable
private fun SheetHeader(title: String, onCancel: (() -> Unit)?, onDone: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (onCancel != null) {
            TextButton(onClick = onCancel) { Text("Cancel", color = UsTheme.extended.onMedia) }
        }
        Spacer(Modifier.weight(1f))
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMedia,
        )
        Spacer(Modifier.weight(1f))
        TextButton(onClick = onDone, modifier = Modifier.testTag("studio-sheet-done")) {
            Text("Done", color = UsTheme.extended.onMedia, fontWeight = FontWeight.Bold)
        }
    }
}

// ── Filter sheet ────────────────────────────────────────────────────────

/**
 * Named looks with LIVE thumbnails — each is the page's own photo behind the
 * preset's color matrix. Filters remain adjustment presets (exposure/contrast
 * pairs); a thumbnail that showed anything the sliders can't do would be a lie.
 */
@Composable
private fun FilterSheet(
    page: StudioViewModel.PageUi,
    draft: Adjustments,
    onDraft: (Adjustments) -> Unit,
    onCancel: () -> Unit,
    onDone: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        SheetHeader(title = "Filter", onCancel = onCancel, onDone = onDone)
        LazyRow(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = UsTheme.spacing.s),
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            items(FILTER_PRESETS, key = { it.first }) { (name, preset) ->
                val selected = preset == draft
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    modifier = Modifier
                        .clickable { onDraft(preset) }
                        .semantics { contentDescription = "$name filter" },
                ) {
                    Text(
                        name,
                        style = MaterialTheme.typography.labelMedium,
                        color = if (selected) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim,
                        fontWeight = if (selected) FontWeight.Bold else FontWeight.Normal,
                    )
                    Spacer(Modifier.height(UsTheme.spacing.xs))
                    Box(
                        modifier = Modifier
                            .size(FILTER_THUMB_W, FILTER_THUMB_H)
                            .clip(RoundedCornerShape(UsTheme.radii.small))
                            .then(
                                if (selected) {
                                    Modifier.border(
                                        2.dp,
                                        UsTheme.extended.onMedia,
                                        RoundedCornerShape(UsTheme.radii.small),
                                    )
                                } else {
                                    Modifier
                                },
                            ),
                    ) {
                        AsyncImage(
                            model = File(page.sourcePath),
                            contentDescription = null,
                            contentScale = ContentScale.Crop,
                            colorFilter = ColorFilter.colorMatrix(adjustmentsMatrix(preset)),
                            modifier = Modifier.fillMaxSize(),
                        )
                    }
                }
            }
        }
    }
}

// ── Edit (adjust) sheet ─────────────────────────────────────────────────

private enum class AdjustTool(val label: String) {
    Brightness("Brightness"),
    Contrast("Contrast"),
    Saturation("Saturation"),
    Warmth("Warmth"),
}

@Composable
private fun AdjustSheet(
    draft: Adjustments,
    onDraft: (Adjustments) -> Unit,
    onCancel: () -> Unit,
    onDone: () -> Unit,
) {
    var selected by remember { mutableStateOf(AdjustTool.Brightness) }

    Column(modifier = Modifier.fillMaxWidth()) {
        SheetHeader(title = "Edit", onCancel = onCancel, onDone = onDone)
        Slider(
            value = when (selected) {
                AdjustTool.Brightness -> draft.exposureMicros / MICROS_F
                AdjustTool.Contrast -> draft.contrastMicros / MICROS_F
                AdjustTool.Saturation -> draft.saturationMicros / MICROS_F
                AdjustTool.Warmth -> draft.warmthMicros / MICROS_F
            },
            onValueChange = { value ->
                onDraft(
                    when (selected) {
                        AdjustTool.Brightness ->
                            draft.copy(exposureMicros = (value * MICROS).toInt())
                        AdjustTool.Contrast ->
                            draft.copy(contrastMicros = (value * MICROS).toInt())
                        AdjustTool.Saturation ->
                            draft.copy(saturationMicros = (value * MICROS).toInt())
                        AdjustTool.Warmth ->
                            draft.copy(warmthMicros = (value * MICROS).toInt())
                    },
                )
            },
            valueRange = -1f..1f,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.l)
                .testTag("studio-adjust-slider"),
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = UsTheme.spacing.s),
            horizontalArrangement = Arrangement.Center,
        ) {
            AdjustTool.entries.forEach { candidate ->
                TextButton(onClick = { selected = candidate }) {
                    Text(
                        candidate.label,
                        color = if (selected == candidate) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim,
                        fontWeight = if (selected == candidate) FontWeight.Bold else FontWeight.Normal,
                    )
                }
            }
        }
    }
}

// ── Ratio sheet ─────────────────────────────────────────────────────────

/**
 * Ratio applies IMMEDIATELY — a crop is a discrete decision undo already
 * covers, and previewing it any other way would need a second crop model.
 */
@Composable
private fun RatioSheet(
    page: StudioViewModel.PageUi,
    viewModel: StudioViewModel,
    onDone: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        SheetHeader(title = "Ratio", onCancel = null, onDone = onDone)
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            RatioOption("Original") { viewModel.onCrop(Crop(0, 0, MICROS, MICROS)) }
            RatioOption("Square") { viewModel.onCropRatio(1f) }
            RatioOption("Portrait") { viewModel.onCropRatio(PORTRAIT_ASPECT) }
            RatioOption("Rotate ${page.rotationDegMicros / DEG_MICROS}°") {
                viewModel.onRotateQuarter()
            }
        }
    }
}

@Composable
private fun RowScope.RatioOption(
    label: String,
    onClick: () -> Unit,
) {
    Box(
        modifier = Modifier
            .weight(1f)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.mediaRim)
            .clickable(onClick = onClick)
            .padding(vertical = UsTheme.spacing.m),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.onMedia)
    }
}

// ── Alt sheet ───────────────────────────────────────────────────────────

/** The per-page accessibility decision — the SHARE gate the badges track. */
@Composable
private fun AltSheet(
    page: StudioViewModel.PageUi,
    viewModel: StudioViewModel,
    onDone: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        SheetHeader(title = "Alt text", onCancel = null, onDone = onDone)
        OutlinedTextField(
            value = page.altText,
            onValueChange = { viewModel.onAccessibility(it, false) },
            label = { Text("Describe this photo") },
            enabled = !page.decorative,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m)
                .testTag("studio-alt-input"),
        )
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(
                horizontal = UsTheme.spacing.m,
                vertical = UsTheme.spacing.xs,
            ),
        ) {
            Switch(
                checked = page.decorative,
                onCheckedChange = { viewModel.onAccessibility("", it) },
                modifier = Modifier.testTag("studio-decorative"),
            )
            Spacer(Modifier.width(UsTheme.spacing.s))
            Text("This photo is decorative", color = UsTheme.extended.onMedia)
        }
    }
}

// ── Text overlay editor (full-screen) ───────────────────────────────────

// LongMethod: a full-screen tool with header, entry, fonts, colors and size —
// splitting it would scatter one gesture across five functions.
@Suppress("LongMethod")
@Composable
private fun TextOverlayEditor(
    page: StudioViewModel.PageUi,
    viewModel: StudioViewModel,
    onClose: () -> Unit,
) {
    var draftText by remember { mutableStateOf("") }
    var fontId by remember { mutableStateOf(CreatorFonts.ALL.first().fontAssetId) }
    var color by remember { mutableStateOf(TEXT_COLORS.first()) }
    var sizeMicros by remember { mutableStateOf(DEFAULT_TEXT_SIZE) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.stage.copy(alpha = TEXT_EDITOR_SCRIM_ALPHA))
            .testTag("studio-text-editor"),
    ) {
        SheetHeader(
            title = "Text",
            onCancel = onClose,
            onDone = {
                if (draftText.isNotBlank()) {
                    viewModel.onAddTextLayer(draftText.trim(), fontId, color.second, sizeMicros)
                }
                onClose()
            },
        )

        Box(modifier = Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
            val typeface = CreatorFonts.typeface(LocalContext.current, fontId)
            OutlinedTextField(
                value = draftText,
                onValueChange = { draftText = it },
                placeholder = { Text("Type something…", color = UsTheme.extended.onMediaDim) },
                textStyle = MaterialTheme.typography.headlineSmall.copy(
                    color = parseArgb(color.second),
                    fontSize = (sizeMicros / TEXT_SIZE_DIVISOR).sp,
                    fontFamily = typeface?.let { FontFamily(it) },
                ),
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.l)
                    .testTag("studio-text-input"),
            )
        }

        // Fonts — the three PINNED faces, nothing else renders.
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            CreatorFonts.ALL.forEach { font ->
                FilterChip(
                    selected = fontId == font.fontAssetId,
                    onClick = { fontId = font.fontAssetId },
                    label = { Text(font.fontAssetId.removePrefix("noto-sans-")) },
                )
            }
        }

        // Colors — a fixed palette stored as exact ARGB in the document.
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.m),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            TEXT_COLORS.forEach { candidate ->
                Box(
                    modifier = Modifier
                        .size(COLOR_SWATCH)
                        .clip(CircleShape)
                        .background(parseArgb(candidate.second))
                        .border(
                            width = if (color == candidate) 3.dp else 1.dp,
                            color = if (color == candidate) {
                                MaterialTheme.colorScheme.primary
                            } else {
                                UsTheme.extended.onMedia
                            },
                            shape = CircleShape,
                        )
                        .clickable { color = candidate }
                        .semantics { contentDescription = "${candidate.first} text" },
                )
            }
        }

        Slider(
            value = sizeMicros.toFloat(),
            onValueChange = { sizeMicros = it.toInt() },
            valueRange = TEXT_SIZE_MIN..TEXT_SIZE_MAX,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.l),
        )

        // Existing layers on this page, removable in place.
        page.textLayers.forEach { layer ->
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.m),
            ) {
                Text(layer.value, color = UsTheme.extended.onMedia, modifier = Modifier.weight(1f))
                TextButton(onClick = { viewModel.onRemoveTextLayer(layer.layerId) }) {
                    Text("Remove")
                }
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.m))
    }
}

// ── Thumbnail strip + Next ──────────────────────────────────────────────

/**
 * Bottom-left: the pages, drag-to-reorder (long press) with the strip's ONLY
 * library dependency; every swap the drag produces is a real MovePage command.
 * Bottom-right: Next.
 */
@Composable
private fun ThumbStripRow(
    state: StudioViewModel.StudioUiState,
    viewModel: StudioViewModel,
    onAddPhotos: () -> Unit,
) {
    val listState = rememberLazyListState()
    val reorderState = rememberReorderableLazyListState(listState) { from, to ->
        viewModel.onDragMove(from.index, to.index)
    }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        LazyRow(
            state = listState,
            modifier = Modifier.weight(1f),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            items(state.pages, key = { it.pageId }) { page ->
                ReorderableItem(reorderState, key = page.pageId) { dragging ->
                    ThumbTile(
                        page = page,
                        position = state.pages.indexOfFirst { it.pageId == page.pageId },
                        count = state.pages.size,
                        selected = page.pageId == state.selectedPageId,
                        dragging = dragging,
                        dragHandle = Modifier.longPressDraggableHandle(),
                        onSelect = { viewModel.onSelectPage(page.pageId) },
                        onRemove = { viewModel.onRemovePage(page.pageId) },
                    )
                }
            }
            if (state.pages.size < MAX_PICK) {
                item(key = "studio-add") {
                    Box(
                        modifier = Modifier
                            .size(THUMB_SIZE)
                            .clip(RoundedCornerShape(UsTheme.radii.medium))
                            .background(UsTheme.extended.mediaRim)
                            .clickable(onClick = onAddPhotos)
                            .semantics { contentDescription = "Add photos" }
                            .testTag("studio-add-photos"),
                        contentAlignment = Alignment.Center,
                    ) {
                        Icon(UsIcons.Create, contentDescription = null, tint = UsTheme.extended.onMedia)
                    }
                }
            }
        }

        Spacer(Modifier.width(UsTheme.spacing.m))

        Button(
            onClick = viewModel::onNext,
            enabled = state.pages.isNotEmpty(),
            modifier = Modifier.testTag("studio-next"),
        ) { Text("Next") }
    }
}

@Suppress("LongParameterList")
@Composable
private fun ThumbTile(
    page: StudioViewModel.PageUi,
    position: Int,
    count: Int,
    selected: Boolean,
    dragging: Boolean,
    dragHandle: Modifier,
    onSelect: () -> Unit,
    onRemove: () -> Unit,
) {
    Box(
        modifier = dragHandle
            .size(THUMB_SIZE)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .then(
                if (selected || dragging) {
                    Modifier.border(2.dp, UsTheme.extended.onMedia, RoundedCornerShape(UsTheme.radii.medium))
                } else {
                    Modifier
                },
            )
            .clickable(onClick = onSelect)
            .semantics {
                contentDescription = "Page ${position + 1} of $count" +
                    (if (selected) ", selected" else "") +
                    ". Long press and drag to reorder."
            },
    ) {
        AsyncImage(
            model = File(page.sourcePath),
            contentDescription = null,
            contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize(),
        )
        // The unmissable "this page still needs a decision" marker.
        if (!page.altDecided) {
            Text(
                "ALT",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onError,
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .background(MaterialTheme.colorScheme.error)
                    .padding(horizontal = 4.dp),
            )
        }
        // Remove lives ON the selected thumb only: visible exactly when the
        // page it deletes is the one on the canvas.
        if (selected) {
            Box(
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(2.dp)
                    .size(REMOVE_BADGE)
                    .clip(CircleShape)
                    .background(UsTheme.extended.mediaPlate)
                    .clickable(onClick = onRemove)
                    .semantics { contentDescription = "Remove page ${position + 1}" },
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    UsIcons.Close,
                    contentDescription = null,
                    tint = UsTheme.extended.onMedia,
                    modifier = Modifier.size(REMOVE_ICON),
                )
            }
        }
    }
}

@Composable
private fun EmptyStudio(onAddPhotos: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            "Up to ten photos, in your order.",
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.onMedia,
        )
        Spacer(Modifier.height(UsTheme.spacing.m))
        Button(onClick = onAddPhotos, modifier = Modifier.testTag("studio-empty-add")) {
            Text("Choose photos")
        }
    }
}

// ════════════════════════════════════════════════════════════════════════
// STEP 2 — THE POST DETAILS (2026-10-18)
// ════════════════════════════════════════════════════════════════════════

/**
 * The details screen: [PostDetailsForm] over the studio's pages — the strip
 * shows them as edited, a tap makes one the cover, "+" adds more (ten at
 * most) — with the recovery and failed-publish cards above it, Preview over
 * it, and Drafts and Post at its foot.
 */
@Composable
private fun ShareStep(
    state: StudioViewModel.StudioUiState,
    viewModel: StudioViewModel,
    onAddPhotos: () -> Unit,
) {
    var previewing by remember { mutableStateOf(false) }
    val tiles = state.pages.map { page ->
        DetailsTile(
            key = page.pageId,
            model = File(page.sourcePath),
            altText = page.altText,
            decorative = page.decorative,
            colorMatrix = adjustmentsMatrix(page.adjustments),
        )
    }
    PostDetailsForm(
        state = PostDetailsFormState(
            tiles = tiles,
            description = state.postText,
            details = state.details,
            recentLocations = state.recentLocations,
            mentionQuery = state.mentionQuery,
            mentionResults = state.mentionResults,
            posting = state.publish is StudioViewModel.PublishUi.Publishing,
            savingDraft = state.savingDraft,
            canPost = state.canPublish,
            language = state.postLanguage,
        ),
        actions = PostDetailsFormActions(
            onBack = viewModel::onBackToEdit,
            onPreview = { previewing = true },
            onDescriptionChanged = viewModel::onPostTextChanged,
            onInsertHashtag = viewModel::onInsertHashtag,
            onMentionQueryChanged = viewModel::onMentionQueryChanged,
            onMentionPicked = viewModel::onMentionPicked,
            onDetails = viewModel::onDetailsChanged,
            onLocationChosen = viewModel::onLocationChosen,
            onAltText = { index, alt, decorative ->
                state.pages.getOrNull(index)?.let { viewModel.onAccessibilityFor(it.pageId, alt, decorative) }
            },
            onAddPhotos = onAddPhotos,
            onDrafts = viewModel::onSaveDraft,
            onPost = viewModel::onPublish,
            onLanguageChanged = viewModel::onLanguageChanged,
        ),
        modifier = Modifier.testTag("studio-share"),
    ) {
        state.recoveries.forEach { recovery ->
            RecoveryCard(recovery, onRetry = { viewModel.onRetryRecovery(recovery.recoveryId) })
        }
        (state.publish as? StudioViewModel.PublishUi.PermanentFailure)?.let { publish ->
            PublishFailureCard(publish.reason, onRetry = viewModel::onRetryPublish)
        }
    }
    if (previewing) {
        StudioPreview(state = state, onClose = { previewing = false })
    }
}

/**
 * Preview: the post as the full-screen feed will show it — the photos as a
 * sideways carousel on the stage, the dots and the caption block — before
 * it goes. Pictures are the edited sources with their colour look; the
 * exported pixels are the render exporter's, as everywhere in the studio.
 */
@Composable
private fun StudioPreview(state: StudioViewModel.StudioUiState, onClose: () -> Unit) {
    androidx.compose.ui.window.Dialog(
        onDismissRequest = onClose,
        properties = androidx.compose.ui.window.DialogProperties(usePlatformDefaultWidth = false),
    ) {
        val pages = state.pages
        val pager = androidx.compose.foundation.pager.rememberPagerState(
            initialPage = state.details.coverIndex.coerceIn(0, (pages.size - 1).coerceAtLeast(0)),
            pageCount = { pages.size },
        )
        Box(modifier = Modifier.fillMaxSize().background(UsTheme.extended.stage).testTag("studio-preview")) {
            androidx.compose.foundation.pager.HorizontalPager(state = pager, modifier = Modifier.fillMaxSize()) { index ->
                val page = pages[index]
                AsyncImage(
                    model = File(page.sourcePath),
                    contentDescription = page.altText.ifBlank { null },
                    contentScale = ContentScale.Fit,
                    colorFilter = ColorFilter.colorMatrix(adjustmentsMatrix(page.adjustments)),
                    modifier = Modifier.fillMaxSize(),
                )
            }
            com.us.android.core.ui.StageBottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
            com.us.android.core.ui.StageCaption(
                state = com.us.android.core.ui.StageCaptionState(
                    postId = state.projectId,
                    handle = "You",
                    date = "now",
                    title = state.details.title.ifBlank { null },
                    text = state.postText,
                    hashtags = descriptionTags(state.postText).first,
                    labels = previewLabels(state.details),
                ),
                onOpenAuthor = {},
                above = {
                    if (pages.size > 1) com.us.android.core.ui.StagePageDots(pageCount = pages.size, currentPage = pager.currentPage)
                },
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .navigationBarsPadding()
                    .padding(UsTheme.spacing.xxl),
            )
            IconButton(
                onClick = onClose,
                modifier = Modifier.align(Alignment.TopStart).statusBarsPadding().padding(UsTheme.spacing.s),
            ) {
                Icon(UsIcons.Close, contentDescription = "Close preview", tint = UsTheme.extended.onMedia)
            }
        }
    }
}

/** The labels the post will wear, as the feed words them. */
private fun previewLabels(details: PostDetailsUi): List<String> = buildList {
    if (details.paidPromotion) add("Paid partnership")
    if (details.isBranded) add("Brand")
    if (details.aiGenerated) add("AI-generated")
}

// ── Recovery and failure cards ──────────────────────────────────────────

/** A publish that already left the device once — finish it, exactly as it was. */
@Composable
private fun RecoveryCard(recovery: StudioViewModel.RecoveryUi, onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(MaterialTheme.colorScheme.secondaryContainer)
            .padding(UsTheme.spacing.m)
            .testTag("studio-recovery"),
    ) {
        Text("A post didn't finish publishing", style = MaterialTheme.typography.titleSmall)
        Text(
            recovery.text.take(RECOVERY_PREVIEW_CHARS),
            style = MaterialTheme.typography.bodySmall,
        )
        recovery.message?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
        Row {
            Button(onClick = onRetry, enabled = !recovery.busy) {
                Text(if (recovery.busy) "Finishing…" else "Finish publishing")
            }
        }
    }
}

@Composable
private fun PublishFailureCard(reason: String, onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(MaterialTheme.colorScheme.errorContainer)
            .padding(UsTheme.spacing.m)
            .testTag("studio-publish-failure"),
    ) {
        Text("Publishing didn't finish", style = MaterialTheme.typography.titleSmall)
        Text(reason, style = MaterialTheme.typography.bodySmall)
        Button(onClick = onRetry) { Text("Try again") }
    }
}

// ── Constants ───────────────────────────────────────────────────────────

private const val MAX_PICK = 10
private const val MICROS = 1_000_000
private const val MICROS_F = 1_000_000f
private const val HALF_MICROS = 500_000
private const val DEG_MICROS = 1_000_000
private const val CANVAS_ASPECT = 1080f / 1350f
private const val PORTRAIT_ASPECT = 1080f / 1350f
private const val TEXT_SIZE_DIVISOR = 2_000
private const val RECOVERY_PREVIEW_CHARS = 80
private const val DEFAULT_TEXT_SIZE = 52_000
private const val TEXT_SIZE_MIN = 24_000f
private const val TEXT_SIZE_MAX = 120_000f

private val THUMB_SIZE = 56.dp
private val PILL_ICON = 16.dp
private val FILTER_THUMB_W = 72.dp
private val FILTER_THUMB_H = 90.dp
private val REMOVE_BADGE = 18.dp
private val REMOVE_ICON = 12.dp
private val COLOR_SWATCH = 32.dp

// The edit canvas is the STAGE in both themes (media first): EditStep runs inside the dark theme.
private const val TEXT_EDITOR_SCRIM_ALPHA = 0.88f

// The preset values ARE the definition of each look — a named constant per
// number would just restate the label sitting beside it. Every look is a
// tuple of the four adjustment channels; there is no hidden LUT engine.
@Suppress("MagicNumber")
private val FILTER_PRESETS = listOf(
    "None" to Adjustments(0, 0),
    "Soft" to Adjustments(80_000, -120_000, -100_000, 0),
    "Bright" to Adjustments(250_000, 60_000),
    "Punchy" to Adjustments(60_000, 350_000, 200_000, 0),
    "Faded" to Adjustments(150_000, -300_000, -250_000, 0),
    "Warm" to Adjustments(80_000, 0, 100_000, 350_000),
    "Cool" to Adjustments(80_000, 0, 50_000, -350_000),
    "Mono" to Adjustments(0, 100_000, -1_000_000, 0),
)

// Named ARGB values are the document's exact stored form.
@Suppress("MagicNumber")
private val TEXT_COLORS = listOf(
    "White" to "#FFFFFFFF",
    "Black" to "#FF000000",
    "Yellow" to "#FFFFD400",
    "Red" to "#FFE53935",
    "Blue" to "#FF2196F3",
    "Green" to "#FF43A047",
)
