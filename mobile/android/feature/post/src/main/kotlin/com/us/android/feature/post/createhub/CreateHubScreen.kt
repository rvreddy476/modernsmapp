package com.us.android.feature.post.createhub

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.composer.ComposerScreen
import java.io.File

/**
 * The Create hub — ONE entry, a footer rail, nothing else to hunt for.
 *
 * ## THE RAIL IS THE ONLY SWITCH
 *
 * Text · Image · Reel · Poll along the bottom, the pattern every social app
 * has taught. There is no "+" anywhere inside, and no dropdown: choosing what
 * to make IS the rail. Each rail item is fully functional — Live joins the
 * rail when a live-streaming stack exists to back it, not before.
 *
 * ## WHAT EACH SURFACE IS
 *
 *  - **Text** — the proven composer, exactly as it was, minus every media
 *    control (media has its own rail items now).
 *  - **Image** — the in-app gallery grid (camera as its first tile), then
 *    straight into the Post Studio with the picked photos already imported.
 *  - **Reel** — the same grid over videos; one tap picks, then title +
 *    caption + Post; upload/transcode/publish states shown honestly.
 *  - **Poll** — question + 2–6 options + multi-select, posted as a real
 *    `poll` post.
 */
@Composable
fun CreateHubScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    onOpenStudio: (uris: List<String>) -> Unit,
    onOpenLive: () -> Unit = {},
) {
    var surface by rememberSaveable { mutableStateOf(CreateSurface.Text) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .testTag("create-hub"),
    ) {
        Box(modifier = Modifier.weight(1f)) {
            when (surface) {
                CreateSurface.Text -> ComposerScreen(
                    onClose = onClose,
                    onPublished = onPublished,
                )
                CreateSurface.Image -> ImageSourceSurface(
                    onClose = onClose,
                    onOpenStudio = onOpenStudio,
                )
                CreateSurface.Reel -> ReelSurface(onClose = onClose, onPublished = onPublished)
                CreateSurface.Poll -> PollSurface(onClose = onClose, onPublished = onPublished)
                // Unreachable: LIVE never becomes the selected surface — the
                // rail routes it out below.
                CreateSurface.Live -> Unit
            }
        }
        CreateRail(
            selected = surface,
            onSelect = { picked ->
                if (picked == CreateSurface.Live) onOpenLive() else surface = picked
            },
        )
    }
}

/**
 * Each surface carries its own accent, per the Figma create-post toolbar:
 * every tool is a different colour so the row reads as a palette of things
 * to make, not four copies of one control.
 */
private enum class CreateSurface(val label: String, val accent: Color) {
    Text("TEXT", CREATE_TEXT_PURPLE),
    Image("IMAGE", CREATE_IMAGE_GREEN),
    Reel("REEL", CREATE_REEL_RED),
    Poll("POLL", CREATE_POLL_BLUE),

    /**
     * Not a surface: selecting it NAVIGATES to the live hub. It sits on the
     * rail because the redesign reserved this slot for LIVE, and the backend
     * (live-service-v2 + LiveKit) now exists to honour it.
     */
    Live("LIVE", CREATE_LIVE_RED),
}

private val CreateSurface.icon: ImageVector
    get() = when (this) {
        CreateSurface.Text -> UsIcons.Type
        CreateSurface.Image -> UsIcons.Photo
        CreateSurface.Reel -> UsIcons.Reels
        CreateSurface.Poll -> UsIcons.Poll
        CreateSurface.Live -> UsIcons.Live
    }

/**
 * The format switch — a FLOATING pill, per the create-post redesign (93:4).
 *
 * The reference pill spells POST · STORY · REEL · LIVE in text; the founder's
 * call (2026-09-01) is icons instead, so the pill carries the per-tool accent
 * glyphs. Only surfaces with a real backend earn a slot — STORY and LIVE join
 * when they exist. The selected tool sits on a soft chip because this pill
 * SWITCHES surfaces and the current choice has to be visible.
 */
@Composable
private fun CreateRail(selected: CreateSurface, onSelect: (CreateSurface) -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                top = UsTheme.spacing.m,
                bottom = UsTheme.spacing.l,
            ),
        contentAlignment = Alignment.Center,
    ) {
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.bgCardSolid)
                .padding(
                    horizontal = UsTheme.spacing.l,
                    vertical = UsTheme.spacing.s,
                ),
        ) {
            CreateSurface.entries.forEach { candidate ->
                val active = candidate == selected
                Box(
                    modifier = Modifier
                        .size(RAIL_TOOL)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(
                            if (active) UsTheme.extended.glassBg else Color.Transparent,
                        )
                        .clickable { onSelect(candidate) }
                        .semantics {
                            contentDescription = "Create ${candidate.label.lowercase()}" +
                                if (active) ", selected" else ""
                        }
                        .testTag("create-rail-${candidate.label.lowercase()}"),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        imageVector = candidate.icon,
                        contentDescription = null,
                        tint = candidate.accent,
                        modifier = Modifier.size(RAIL_GLYPH),
                    )
                }
            }
        }
    }
}

// ════════════════════════════════════════════════════════════════════════
// IMAGE — Camera or Gallery, then the Studio
// ════════════════════════════════════════════════════════════════════════

@Composable
private fun ImageSourceSurface(onClose: () -> Unit, onOpenStudio: (uris: List<String>) -> Unit) {
    val context = LocalContext.current
    var cameraTarget by remember { mutableStateOf<android.net.Uri?>(null) }

    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PICK),
    ) { uris -> if (uris.isNotEmpty()) onOpenStudio(uris.map { it.toString() }) }

    val takePicture = rememberLauncherForActivityResult(
        ActivityResultContracts.TakePicture(),
    ) { saved -> if (saved) cameraTarget?.let { onOpenStudio(listOf(it.toString())) } }

    MediaGallerySurface(
        kind = GalleryKind.Photos,
        title = "New photo post",
        subtitle = "Up to ten photos. Editing opens next.",
        onClose = onClose,
        onCamera = {
            val target = captureUri(context, "jpg")
            cameraTarget = target
            takePicture.launch(target)
        },
        onPicked = { uris -> onOpenStudio(uris.map { it.toString() }) },
        onSystemPicker = {
            pickImages.launch(
                PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
            )
        },
    )
}

/** A grantable cache URI for the system camera to write into. */
private fun captureUri(context: android.content.Context, extension: String): android.net.Uri {
    val dir = File(context.cacheDir, "create_capture").apply { mkdirs() }
    val file = File(dir, "capture_${System.currentTimeMillis()}.$extension")
    return FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
}

// ════════════════════════════════════════════════════════════════════════
// REEL — pick or record a video, then title + caption + Post
// ════════════════════════════════════════════════════════════════════════

// LongMethod/complexity: one surface, one composable — the branches ARE the
// upload/processing/posting state machine, rendered honestly.
@Suppress("LongMethod", "CyclomaticComplexMethod")
@Composable
private fun ReelSurface(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: ReelPublishViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var cameraTarget by remember { mutableStateOf<android.net.Uri?>(null) }

    val pickVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.PickVisualMedia(),
    ) { uri -> uri?.let(viewModel::onVideoPicked) }

    val captureVideo = rememberLauncherForActivityResult(
        ActivityResultContracts.CaptureVideo(),
    ) { saved -> if (saved) cameraTarget?.let(viewModel::onVideoPicked) }

    if (state.videoUri == null) {
        MediaGallerySurface(
            kind = GalleryKind.Videos,
            title = "New reel",
            subtitle = "Pick a video — it posts to Reels.",
            onClose = onClose,
            onCamera = {
                val target = captureUri(context, "mp4")
                cameraTarget = target
                captureVideo.launch(target)
            },
            onPicked = { uris -> uris.firstOrNull()?.let(viewModel::onVideoPicked) },
            onSystemPicker = {
                pickVideo.launch(
                    PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.VideoOnly),
                )
            },
        )
        return
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(UsTheme.spacing.pageHorizontal),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onClose) {
                Icon(
                    UsIcons.Close,
                    contentDescription = "Close",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            Text(
                "New reel",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
        }

        // The chosen video, stated as a fact with a way out.
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.medium))
                .background(UsTheme.extended.bgCard)
                .padding(UsTheme.spacing.m),
        ) {
            Icon(UsIcons.Reels, contentDescription = null, tint = UsTheme.extended.textMuted)
            Spacer(Modifier.width(UsTheme.spacing.m))
            Text(
                "Video selected",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            TextButton(
                onClick = {
                    pickVideo.launch(
                        PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.VideoOnly),
                    )
                },
                enabled = state.phase is ReelPublishViewModel.Phase.Editing ||
                    state.phase is ReelPublishViewModel.Phase.Failure,
            ) { Text("Change") }
        }

        Spacer(Modifier.height(UsTheme.spacing.m))
        OutlinedTextField(
            value = state.title,
            onValueChange = viewModel::onTitleChanged,
            label = { Text("Title") },
            singleLine = true,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("reel-title"),
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        OutlinedTextField(
            value = state.caption,
            onValueChange = viewModel::onCaptionChanged,
            label = { Text("Caption") },
            minLines = 2,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("reel-caption"),
        )

        Spacer(Modifier.height(UsTheme.spacing.m))

        when (val phase = state.phase) {
            is ReelPublishViewModel.Phase.Uploading -> {
                LinearProgressIndicator(
                    progress = { phase.fraction },
                    modifier = Modifier.fillMaxWidth(),
                )
                StatusLine("Uploading video… ${(phase.fraction * PERCENT).toInt()}%")
            }
            is ReelPublishViewModel.Phase.Processing ->
                StatusLine("Processing video — this can take a couple of minutes…")
            is ReelPublishViewModel.Phase.Posting -> StatusLine("Posting…")
            is ReelPublishViewModel.Phase.Failure -> {
                Text(
                    phase.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                Spacer(Modifier.height(UsTheme.spacing.s))
            }
            is ReelPublishViewModel.Phase.Published -> {
                // Navigate on the SERVER's id, once.
                LaunchedEffect(phase.postId) { onPublished(phase.postId) }
            }
            is ReelPublishViewModel.Phase.Editing -> Unit
        }

        val busy = state.phase is ReelPublishViewModel.Phase.Uploading ||
            state.phase is ReelPublishViewModel.Phase.Processing ||
            state.phase is ReelPublishViewModel.Phase.Posting
        Button(
            onClick = viewModel::onPost,
            enabled = state.canPost && !busy,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("reel-post")
                .semantics {
                    contentDescription = when {
                        busy -> "Post reel. In progress."
                        state.canPost -> "Post reel"
                        else -> "Post reel. Unavailable: add a title first."
                    }
                },
        ) {
            if (busy) {
                CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
            } else {
                Text(if (state.phase is ReelPublishViewModel.Phase.Failure) "Try again" else "Post reel")
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.xl))
    }
}

@Composable
private fun StatusLine(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(vertical = UsTheme.spacing.s),
    )
}

// ════════════════════════════════════════════════════════════════════════
// POLL — question, options, post
// ════════════════════════════════════════════════════════════════════════

// LongMethod: one surface, one composable — the same trade the composer makes.
@Suppress("LongMethod")
@Composable
private fun PollSurface(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: PollComposerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    (state.phase as? PollComposerViewModel.Phase.Published)?.let { published ->
        LaunchedEffect(published.postId) { onPublished(published.postId) }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(UsTheme.spacing.pageHorizontal),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onClose) {
                Icon(
                    UsIcons.Close,
                    contentDescription = "Close",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            Text(
                "New poll",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
        }

        OutlinedTextField(
            value = state.question,
            onValueChange = viewModel::onQuestionChanged,
            placeholder = { Text("Ask a question…") },
            textStyle = MaterialTheme.typography.bodyLarge.copy(fontSize = QUESTION_TEXT_SIZE),
            modifier = Modifier
                .fillMaxWidth()
                .testTag("poll-question"),
            minLines = 2,
        )

        Spacer(Modifier.height(UsTheme.spacing.m))

        state.options.forEachIndexed { index, option ->
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(bottom = UsTheme.spacing.s),
            ) {
                OutlinedTextField(
                    value = option,
                    onValueChange = { viewModel.onOptionChanged(index, it) },
                    placeholder = { Text("Option ${index + 1}") },
                    singleLine = true,
                    modifier = Modifier
                        .weight(1f)
                        .testTag("poll-option-$index"),
                )
                if (state.options.size > 2) {
                    IconButton(onClick = { viewModel.onRemoveOption(index) }) {
                        Icon(
                            UsIcons.Close,
                            contentDescription = "Remove option ${index + 1}",
                            tint = UsTheme.extended.textMuted,
                        )
                    }
                }
            }
        }

        if (state.canAddOption) {
            TextButton(
                onClick = viewModel::onAddOption,
                modifier = Modifier.testTag("poll-add-option"),
            ) { Text("Add option") }
        }

        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(UsTheme.radii.medium))
                .clickable { viewModel.onAllowsMultipleChanged(!state.allowsMultiple) }
                .padding(vertical = UsTheme.spacing.s),
        ) {
            Switch(
                checked = state.allowsMultiple,
                onCheckedChange = viewModel::onAllowsMultipleChanged,
                modifier = Modifier.testTag("poll-multiple"),
            )
            Spacer(Modifier.width(UsTheme.spacing.m))
            Text(
                "Allow choosing more than one",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
        }

        when (val phase = state.phase) {
            is PollComposerViewModel.Phase.RetryableFailure -> {
                Text(
                    phase.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            is PollComposerViewModel.Phase.TerminalFailure -> {
                Text(
                    phase.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            else -> Unit
        }

        Spacer(Modifier.height(UsTheme.spacing.m))

        val posting = state.phase is PollComposerViewModel.Phase.Posting
        Button(
            onClick = if (state.phase is PollComposerViewModel.Phase.RetryableFailure) {
                viewModel::onRetry
            } else {
                viewModel::onPost
            },
            enabled = state.canPost,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("poll-post")
                .semantics {
                    contentDescription = when {
                        posting -> "Post poll. In progress."
                        state.canPost -> "Post poll"
                        else -> "Post poll. Unavailable: add a question and at least two options."
                    }
                },
        ) {
            if (posting) {
                CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
            } else {
                Text(
                    if (state.phase is PollComposerViewModel.Phase.RetryableFailure) {
                        "Try again"
                    } else {
                        "Post poll"
                    },
                )
            }
        }
        Spacer(Modifier.height(UsTheme.spacing.xl))
    }
}

// ── Constants ───────────────────────────────────────────────────────────

private const val MAX_PICK = 10
private const val PERCENT = 100
private val QUESTION_TEXT_SIZE = 19.sp

// ── The floating rail pill, per the create-post redesign (93:4) ─────────

/** Each tool's touch container. */
private val RAIL_TOOL = 40.dp

/** Glyph size inside a tool. */
private val RAIL_GLYPH = 24.dp

// Per-tool accents, sampled from the Figma toolbar glyphs.
@Suppress("MagicNumber")
private val CREATE_TEXT_PURPLE = Color(0xFFAB47BC)

@Suppress("MagicNumber")
private val CREATE_IMAGE_GREEN = Color(0xFF4CAF50)

@Suppress("MagicNumber")
private val CREATE_REEL_RED = Color(0xFFEF5350)

@Suppress("MagicNumber")
private val CREATE_POLL_BLUE = Color(0xFF2196F3)

@Suppress("MagicNumber")
private val CREATE_LIVE_RED = Color(0xFFFF3366)
