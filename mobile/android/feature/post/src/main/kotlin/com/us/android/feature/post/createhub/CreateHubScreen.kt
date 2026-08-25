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
 *  - **Image** — Camera or Gallery, then straight into the Post Studio with
 *    the picked photos already imported.
 *  - **Reel** — Camera or Gallery for a video, then title + caption + Post;
 *    upload/transcode/publish states shown honestly.
 *  - **Poll** — question + 2–6 options + multi-select, posted as a real
 *    `poll` post.
 */
@Composable
fun CreateHubScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    onOpenStudio: (uris: List<String>) -> Unit,
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
                CreateSurface.Image -> ImageSourceSurface(onOpenStudio = onOpenStudio)
                CreateSurface.Reel -> ReelSurface(onClose = onClose, onPublished = onPublished)
                CreateSurface.Poll -> PollSurface(onClose = onClose, onPublished = onPublished)
            }
        }
        CreateRail(selected = surface, onSelect = { surface = it })
    }
}

private enum class CreateSurface(val label: String) {
    Text("TEXT"),
    Image("IMAGE"),
    Reel("REEL"),
    Poll("POLL"),
}

/** The footer rail — the hub's one and only format switch. */
@Composable
private fun CreateRail(selected: CreateSurface, onSelect: (CreateSurface) -> Unit) {
    Column {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(RAIL_BG)
                .padding(vertical = UsTheme.spacing.m),
            horizontalArrangement = Arrangement.SpaceEvenly,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            CreateSurface.entries.forEach { candidate ->
                val active = candidate == selected
                Text(
                    text = candidate.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
                    letterSpacing = RAIL_LETTER_SPACING,
                    color = if (active) Color.White else RAIL_DIM,
                    modifier = Modifier
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .clickable { onSelect(candidate) }
                        .padding(
                            horizontal = UsTheme.spacing.m,
                            vertical = UsTheme.spacing.s,
                        )
                        .semantics {
                            contentDescription = "Create ${candidate.label.lowercase()}" +
                                if (active) ", selected" else ""
                        }
                        .testTag("create-rail-${candidate.label.lowercase()}"),
                )
            }
        }
    }
}

// ════════════════════════════════════════════════════════════════════════
// IMAGE — Camera or Gallery, then the Studio
// ════════════════════════════════════════════════════════════════════════

@Composable
private fun ImageSourceSurface(onOpenStudio: (uris: List<String>) -> Unit) {
    val context = LocalContext.current
    var cameraTarget by remember { mutableStateOf<android.net.Uri?>(null) }

    val pickImages = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(MAX_PICK),
    ) { uris -> if (uris.isNotEmpty()) onOpenStudio(uris.map { it.toString() }) }

    val takePicture = rememberLauncherForActivityResult(
        ActivityResultContracts.TakePicture(),
    ) { saved -> if (saved) cameraTarget?.let { onOpenStudio(listOf(it.toString())) } }

    SourceChooser(
        title = "New photo post",
        subtitle = "Up to ten photos. Editing opens next.",
        cameraLabel = "Take a photo",
        galleryLabel = "Choose from gallery",
        onCamera = {
            val target = captureUri(context, "jpg")
            cameraTarget = target
            takePicture.launch(target)
        },
        onGallery = {
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

@Suppress("LongParameterList")
@Composable
private fun SourceChooser(
    title: String,
    subtitle: String,
    cameraLabel: String,
    galleryLabel: String,
    onCamera: () -> Unit,
    onGallery: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xl),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            subtitle,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.height(UsTheme.spacing.xl))
        SourceOption(label = cameraLabel, testTag = "create-source-camera", onClick = onCamera)
        Spacer(Modifier.height(UsTheme.spacing.m))
        SourceOption(label = galleryLabel, testTag = "create-source-gallery", onClick = onGallery)
    }
}

@Composable
private fun SourceOption(label: String, testTag: String, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgCard)
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.l)
            .testTag(testTag),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            label,
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
        )
    }
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
        SourceChooser(
            title = "New reel",
            subtitle = "Pick a video — it posts to Reels.",
            cameraLabel = "Record a video",
            galleryLabel = "Choose from gallery",
            onCamera = {
                val target = captureUri(context, "mp4")
                cameraTarget = target
                captureVideo.launch(target)
            },
            onGallery = {
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
private val HAIRLINE = 1.dp
private val QUESTION_TEXT_SIZE = 19.sp
private val RAIL_LETTER_SPACING = 2.sp

/** The rail is dark in both themes — it belongs to the creation surfaces. */
@Suppress("MagicNumber")
private val RAIL_BG = Color(0xFF101010)

@Suppress("MagicNumber")
private val RAIL_DIM = Color(0x80FFFFFF)
