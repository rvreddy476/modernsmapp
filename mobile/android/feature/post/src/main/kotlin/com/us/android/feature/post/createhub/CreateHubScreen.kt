package com.us.android.feature.post.createhub

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.composer.ComposerMode
import com.us.android.feature.post.composer.ComposerScreen
import java.io.File

/**
 * The Create hub — one composer, opened on the surface the sheet chose.
 *
 * ## THERE IS NO RAIL
 *
 * The choice of what to make happens on the Create SHEET, once, before this
 * screen exists. It used to happen here, on a footer rail that could switch
 * between Text, Image, Reel and Poll after arrival; the founder's redesign
 * (2026-09-04) moves that decision up a level, so this screen is exactly one
 * composer with a type name in its top bar, a "×" to leave, and its publish
 * control. Nothing here selects a format.
 *
 * ## WHAT EACH SURFACE IS
 *
 *  - **Text** — the proven composer.
 *  - **Article** — the same composer in long-form mode: a title over a
 *    taller body, posted as `content_type: post` with the title set.
 *  - **Photo** — the in-app gallery grid (camera as its first tile), then the
 *    Post Studio with the picks already imported. Unchanged.
 *  - **Reel** — the same grid over videos; one tap picks, then the
 *    TikTok-shaped form in [ReelSurface]: description, cover strip, tag
 *    people, location, audience, category, switches; upload/transcode/
 *    publish states shown honestly.
 *  - **Audio** — record a voice note or pick a track, preview, caption, post
 *    as a `voice` post. See [VoicePublishViewModel] for what the server
 *    accepts today.
 *  - **Poll** — question + 2–6 options + multi-select, posted as a `poll`.
 *
 * Live is not a surface: the sheet's Go Live row navigates to the live hub.
 */
@Composable
fun CreateHubScreen(
    surface: CreateSurface,
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    onOpenStudio: (uris: List<String>) -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .testTag("create-hub"),
    ) {
        when (surface) {
            CreateSurface.Text -> ComposerScreen(
                onClose = onClose,
                onPublished = onPublished,
                mode = ComposerMode.Post,
            )
            CreateSurface.Article -> ComposerScreen(
                onClose = onClose,
                onPublished = onPublished,
                mode = ComposerMode.Article,
            )
            CreateSurface.Photo -> ImageSourceSurface(
                onClose = onClose,
                onOpenStudio = onOpenStudio,
            )
            CreateSurface.Reel -> ReelSurface(onClose = onClose, onPublished = onPublished)
            CreateSurface.Audio -> VoiceSurface(onClose = onClose, onPublished = onPublished)
            CreateSurface.Poll -> PollSurface(onClose = onClose, onPublished = onPublished)
        }
    }
}

// ════════════════════════════════════════════════════════════════════════
// Shared chrome: the type-name top bar and the publish pill
// ════════════════════════════════════════════════════════════════════════

/**
 * "×" on the left, the type name (Outfit Bold 17), the publish control on
 * the right. Every non-composer surface shares it so the hub reads as one
 * screen family whichever tile opened it.
 */
@Composable
internal fun CreateTopBar(
    title: String,
    onClose: () -> Unit,
    action: @Composable () -> Unit = {},
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .size(TAP_TARGET)
                .clip(CircleShape)
                .clickable(onClick = onClose)
                .semantics { contentDescription = "Close" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
            )
        }
        Spacer(Modifier.width(UsTheme.spacing.s))
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium.copy(fontSize = TOP_BAR_TITLE_SIZE),
            color = UsTheme.extended.textPrimary,
        )
        Spacer(Modifier.weight(1f))
        action()
    }
}

/**
 * The commit action as a compact pill in the top bar — the composer's own
 * pattern. A disabled control always states its reason through
 * [description]; "Post, disabled" with no explanation is the most common
 * accessibility failure in a composer.
 */
@Composable
internal fun PublishPill(
    text: String,
    enabled: Boolean,
    busy: Boolean,
    onClick: () -> Unit,
    description: String,
    testTag: String,
) {
    UsButton(
        text = text,
        onClick = onClick,
        enabled = enabled,
        loading = busy,
        modifier = Modifier
            .width(PILL_WIDTH)
            .testTag(testTag)
            .semantics { contentDescription = description },
    )
}

// ════════════════════════════════════════════════════════════════════════
// PHOTO — Camera or Gallery, then the Studio
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

    val openCamera = rememberCameraLaunch {
        val target = captureUri(context, "jpg")
        cameraTarget = target
        takePicture.launch(target)
    }
    MediaGallerySurface(
        kind = GalleryKind.Photos,
        title = "New photo post",
        subtitle = "Up to ten photos. Editing opens next.",
        onClose = onClose,
        onCamera = openCamera,
        onPicked = { uris -> onOpenStudio(uris.map { it.toString() }) },
        onSystemPicker = {
            pickImages.launch(
                PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
            )
        },
    )
}

/**
 * Runs [capture] once the CAMERA permission is held, asking for it first when
 * it is not.
 *
 * Delegated capture (ACTION_IMAGE_CAPTURE / ACTION_VIDEO_CAPTURE) needs no
 * permission on its own — but this app DECLARES android.permission.CAMERA
 * (`:core:call` needs it for video calls), and once a manifest declares it
 * Android insists the runtime grant exists before the camera intent may
 * start; without it the launch throws a SecurityException and the app dies.
 * The emulator happened to have the grant, the founder's phone did not.
 * A refusal leaves the gallery and system picker as the way in.
 */
@Composable
internal fun rememberCameraLaunch(capture: () -> Unit): () -> Unit {
    val context = LocalContext.current
    val latest = rememberUpdatedState(capture)
    val request = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted -> if (granted) latest.value() }
    return {
        val held = ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) ==
            PackageManager.PERMISSION_GRANTED
        if (held) latest.value() else request.launch(Manifest.permission.CAMERA)
    }
}

/** A grantable cache URI for the system camera to write into. */
internal fun captureUri(context: android.content.Context, extension: String): android.net.Uri {
    val dir = File(context.cacheDir, "create_capture").apply { mkdirs() }
    val file = File(dir, "capture_${System.currentTimeMillis()}.$extension")
    return FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
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

    val posting = state.phase is PollComposerViewModel.Phase.Posting
    val retrying = state.phase is PollComposerViewModel.Phase.RetryableFailure

    Column(modifier = Modifier.fillMaxSize()) {
        CreateTopBar(title = CreateSurface.Poll.label, onClose = onClose) {
            PublishPill(
                text = if (retrying) "Try again" else "Post",
                enabled = state.canPost,
                busy = posting,
                onClick = if (retrying) viewModel::onRetry else viewModel::onPost,
                description = when {
                    posting -> "Post poll. In progress."
                    state.canPost -> "Post poll"
                    else -> "Post poll. Unavailable: add a question and at least two options."
                },
                testTag = "poll-post",
            )
        }

        Column(
            modifier = Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
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
                is PollComposerViewModel.Phase.RetryableFailure -> Text(
                    phase.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                is PollComposerViewModel.Phase.TerminalFailure -> Text(
                    phase.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                else -> Unit
            }
            Spacer(Modifier.height(UsTheme.spacing.xl))
        }
    }
}

// ── Constants ───────────────────────────────────────────────────────────

private const val MAX_PICK = 10
private val QUESTION_TEXT_SIZE = 19.sp
private val TOP_BAR_TITLE_SIZE = 17.sp
private val TAP_TARGET = 40.dp
private val PILL_WIDTH = 92.dp
