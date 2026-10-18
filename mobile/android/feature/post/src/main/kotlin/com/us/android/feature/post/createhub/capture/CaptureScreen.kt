package com.us.android.feature.post.createhub.capture

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageCapture
import androidx.camera.core.ImageCaptureException
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.LightSystemBarGlyphs
import com.us.android.feature.post.createhub.CreateScope
import com.us.android.feature.post.createhub.CreateSheet
import com.us.android.feature.post.createhub.CreateSurface
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Create, camera first (post-screens parity, 2026-10-18): the "+" opens the
 * camera. The mode chips under the shutter pick what to make — Photo is shot
 * here; Text, the reels and the long video hand over to their own composers
 * ([CaptureMode.handsOverTo]); More opens the Create sheet with everything
 * else (Poll, Audio, Article, Go Live, Drafts) so nothing is lost. The
 * newest photo bottom-left opens [CreatePicker]. Without the camera
 * permission the screen IS the picker — gallery first.
 */
@Composable
fun CaptureScreen(
    onClose: () -> Unit,
    /** A mode or a sheet tile handed over; [initialVideo] is a video picked here, for the reel flow to start from. */
    onOpenSurface: (surface: CreateSurface, initialVideo: String?) -> Unit,
    onOpenStudio: (uris: List<String>) -> Unit,
    onOpenLive: () -> Unit,
    onOpenDrafts: () -> Unit,
) {
    val context = LocalContext.current
    var cameraAllowed by remember { mutableStateOf(hasCamera(context)) }
    var asked by rememberSaveable { mutableStateOf(false) }
    var picking by rememberSaveable { mutableStateOf(false) }
    var sheet by rememberSaveable { mutableStateOf(false) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        asked = true
        cameraAllowed = ok
    }
    LaunchedEffect(Unit) { if (!cameraAllowed && !asked) permission.launch(Manifest.permission.CAMERA) }

    val onPicked: (PickerResult) -> Unit = { result ->
        when (result) {
            is PickerResult.Photos -> onOpenStudio(result.uris)
            is PickerResult.Video -> onOpenSurface(CreateSurface.Reel, result.uri)
        }
    }
    BackHandler(enabled = picking && cameraAllowed) { picking = false }

    when {
        // Gallery first: no camera, so the picker is the screen, with the way to ask again.
        !cameraAllowed && asked -> CreatePicker(
            onClose = onClose,
            onResult = onPicked,
            onCamera = { permission.launch(Manifest.permission.CAMERA) },
        )
        picking -> CreatePicker(onClose = { picking = false }, onResult = onPicked)
        else -> UsTheme(darkTheme = true) {
            LightSystemBarGlyphs()
            CameraStage(
                cameraAllowed = cameraAllowed,
                onClose = onClose,
                onCaptured = { uri -> onOpenStudio(listOf(uri.toString())) },
                onMode = { mode -> mode.handsOverTo?.let { onOpenSurface(it, null) } },
                onOpenPicker = { picking = true },
                onMore = { sheet = true },
            )
        }
    }
    if (sheet) {
        CreateSheet(
            onPick = { surface -> onOpenSurface(surface, null) },
            onOpenLive = onOpenLive,
            onDismiss = { sheet = false },
            scope = CreateScope.App,
            onOpenDrafts = onOpenDrafts,
        )
    }
}

@Suppress("LongMethod", "LongParameterList")
@Composable
private fun CameraStage(
    cameraAllowed: Boolean,
    onClose: () -> Unit,
    onCaptured: (Uri) -> Unit,
    onMode: (CaptureMode) -> Unit,
    onOpenPicker: () -> Unit,
    onMore: () -> Unit,
) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current
    var back by rememberSaveable { mutableStateOf(true) }
    var flash by rememberSaveable { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    val capture = remember { ImageCapture.Builder().setCaptureMode(ImageCapture.CAPTURE_MODE_MINIMIZE_LATENCY).build() }
    val previewView = remember { PreviewView(context) }
    LaunchedEffect(flash) { capture.flashMode = if (flash) ImageCapture.FLASH_MODE_ON else ImageCapture.FLASH_MODE_OFF }
    DisposableEffect(back, cameraAllowed) {
        if (!cameraAllowed) return@DisposableEffect onDispose {}
        val future = ProcessCameraProvider.getInstance(context)
        future.addListener(
            {
                runCatching {
                    val provider = future.get()
                    val preview = Preview.Builder().build().also { it.surfaceProvider = previewView.surfaceProvider }
                    provider.unbindAll()
                    provider.bindToLifecycle(
                        lifecycle,
                        if (back) CameraSelector.DEFAULT_BACK_CAMERA else CameraSelector.DEFAULT_FRONT_CAMERA,
                        preview,
                        capture,
                    )
                }.onFailure { failed = true }
            },
            ContextCompat.getMainExecutor(context),
        )
        onDispose { runCatching { future.get().unbindAll() } }
    }
    val newest by produceState<ImageBitmap?>(initialValue = null) {
        value = withContext(Dispatchers.IO) {
            runCatching { queryPickerMedia(context).firstOrNull { !it.isVideo } }.getOrNull()
                ?.let { loadThumb(context, Uri.parse(it.uri)) }
        }
    }

    Box(modifier = Modifier.fillMaxSize().background(UsTheme.extended.stage).testTag("create-capture")) {
        if (cameraAllowed) AndroidView(factory = { previewView }, modifier = Modifier.fillMaxSize())
        if (failed || !cameraAllowed) {
            Text(
                if (failed) "The camera couldn't start." else "Waiting for the camera…",
                color = UsTheme.extended.onMedia,
                modifier = Modifier.align(Alignment.Center),
            )
        }
        IconButton(
            onClick = onClose,
            modifier = Modifier.align(Alignment.TopStart).statusBarsPadding().padding(UsTheme.spacing.m).testTag("create-capture-close"),
        ) {
            Icon(UsIcons.Close, contentDescription = "Close", tint = UsTheme.extended.onMedia)
        }
        Column(
            modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            IconButton(onClick = { back = !back }, modifier = Modifier.testTag("create-capture-flip")) {
                Icon(UsIcons.RotateCw, contentDescription = "Switch camera", tint = UsTheme.extended.onMedia)
            }
            IconButton(onClick = { flash = !flash }, modifier = Modifier.testTag("create-capture-flash")) {
                Icon(
                    if (flash) UsIcons.Zap else UsIcons.ZapOff,
                    contentDescription = if (flash) "Flash on" else "Flash off",
                    tint = UsTheme.extended.onMedia,
                )
            }
        }
        Column(
            modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().navigationBarsPadding().padding(bottom = UsTheme.spacing.l),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Box(modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xxl), contentAlignment = Alignment.Center) {
                // The newest photo: the way into the gallery.
                Box(
                    modifier = Modifier
                        .align(Alignment.CenterStart)
                        .size(GALLERY_THUMB)
                        .clip(RoundedCornerShape(UsTheme.radii.small))
                        .border(THUMB_RIM, UsTheme.extended.onMedia, RoundedCornerShape(UsTheme.radii.small))
                        .background(UsTheme.extended.mediaPlate)
                        .clickable(role = Role.Button, onClickLabel = "Open the gallery", onClick = onOpenPicker)
                        .testTag("create-capture-gallery"),
                    contentAlignment = Alignment.Center,
                ) {
                    newest?.let { Image(bitmap = it, contentDescription = "Gallery", contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize()) }
                        ?: Icon(UsIcons.Image, contentDescription = "Gallery", tint = UsTheme.extended.onMedia)
                }
                Box(
                    modifier = Modifier
                        .size(SHUTTER)
                        .border(SHUTTER_RING, UsTheme.extended.onMedia, CircleShape)
                        .padding(SHUTTER_GAP)
                        .background(if (busy) UsTheme.extended.onMediaMuted else UsTheme.extended.onMedia, CircleShape)
                        .clickable(enabled = cameraAllowed && !busy && !failed, role = Role.Button) {
                            busy = true
                            val file = File(context.cacheDir, "capture-${System.currentTimeMillis()}.jpg")
                            capture.takePicture(
                                ImageCapture.OutputFileOptions.Builder(file).build(),
                                ContextCompat.getMainExecutor(context),
                                object : ImageCapture.OnImageSavedCallback {
                                    override fun onImageSaved(output: ImageCapture.OutputFileResults) {
                                        busy = false
                                        onCaptured(Uri.fromFile(file))
                                    }

                                    override fun onError(exception: ImageCaptureException) {
                                        busy = false
                                        file.delete()
                                    }
                                },
                            )
                        }
                        .semantics { contentDescription = "Take photo" }
                        .testTag("create-capture-shutter"),
                )
            }
            ModeChips(selected = DEFAULT_CAPTURE_MODE, onMode = onMode, onMore = onMore)
        }
    }
}

@Composable
private fun ModeChips(selected: CaptureMode, onMode: (CaptureMode) -> Unit, onMore: () -> Unit) {
    Row(
        modifier = Modifier
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.xxl)
            .selectableGroup()
            .testTag("create-capture-modes"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        CaptureMode.entries.forEach { mode ->
            val active = mode == selected
            Text(
                text = mode.label,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
                color = if (active) UsTheme.extended.stage else UsTheme.extended.onMedia,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(if (active) UsTheme.extended.onMedia else UsTheme.extended.mediaPlate)
                    .selectable(selected = active, role = Role.Tab) { onMode(mode) }
                    .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                    .testTag("create-capture-mode:${mode.name.lowercase()}"),
            )
        }
        Text(
            text = "More",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.onMedia,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .border(THUMB_RIM, UsTheme.extended.mediaRim, RoundedCornerShape(UsTheme.radii.full))
                .clickable(role = Role.Button, onClick = onMore)
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                .testTag("create-capture-more"),
        )
    }
}

private fun hasCamera(context: android.content.Context): Boolean =
    ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED

private val SHUTTER = 76.dp
private val SHUTTER_RING = 4.dp
private val SHUTTER_GAP = 6.dp
private val GALLERY_THUMB = 44.dp
private val THUMB_RIM = 1.5.dp
