// MatchingDeclarationName: this file's primary export is the MediaGallerySurface
// composable; GalleryKind is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.createhub

import android.content.ContentUris
import android.content.Context
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.util.Size
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.wrapContentWidth
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.toMutableStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The in-app gallery — the media surfaces' whole UI.
 *
 * Choosing the Image or Reel tool lands HERE, not on a chooser and not on the
 * system sheet: the user's own photos are the content of the screen, one tap
 * away, with the camera as the FIRST TILE of the grid — the Instagram pattern
 * the founder asked for by name. Photos multi-select (the studio takes up to
 * ten) and confirm with Next; a video is single-tap because only one can post.
 *
 * The system photo picker survives only as the fallback when media permission
 * is denied — it needs no permission, so the flow degrades instead of dying.
 */
internal enum class GalleryKind { Photos, Videos }

@Suppress("LongParameterList")
@Composable
internal fun MediaGallerySurface(
    kind: GalleryKind,
    title: String,
    subtitle: String,
    onClose: () -> Unit,
    onCamera: () -> Unit,
    onPicked: (List<Uri>) -> Unit,
    onSystemPicker: () -> Unit,
) {
    val context = LocalContext.current
    var access by remember { mutableStateOf<Boolean?>(null) }

    val request = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants -> access = grants.values.any { it } }

    // Partial access (Android 14 "select photos") grants only the
    // VISUAL_USER_SELECTED permission and MediaStore then serves exactly the
    // chosen subset — which is a working gallery, so ANY grant counts.
    LaunchedEffect(Unit) {
        val wanted = mediaPermissions(kind)
        val alreadyGranted = wanted.any {
            ContextCompat.checkSelfPermission(context, it) ==
                android.content.pm.PackageManager.PERMISSION_GRANTED
        }
        if (alreadyGranted) access = true else request.launch(wanted.toTypedArray())
    }

    when (access) {
        true -> GalleryGrid(
            kind = kind,
            title = title,
            onClose = onClose,
            onCamera = onCamera,
            onPicked = onPicked,
        )
        false -> PermissionFallback(
            title = title,
            subtitle = subtitle,
            cameraDescription = if (kind == GalleryKind.Photos) "Take a photo" else "Record a video",
            onClose = onClose,
            onCamera = onCamera,
            onSystemPicker = onSystemPicker,
        )
        null -> Unit // The permission dialog is the screen; drawing under it is noise.
    }
}

private fun mediaPermissions(kind: GalleryKind): List<String> = when {
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE -> listOf(
        if (kind == GalleryKind.Photos) {
            android.Manifest.permission.READ_MEDIA_IMAGES
        } else {
            android.Manifest.permission.READ_MEDIA_VIDEO
        },
        android.Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED,
    )
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU -> listOf(
        if (kind == GalleryKind.Photos) {
            android.Manifest.permission.READ_MEDIA_IMAGES
        } else {
            android.Manifest.permission.READ_MEDIA_VIDEO
        },
    )
    else -> listOf(android.Manifest.permission.READ_EXTERNAL_STORAGE)
}

// ── The grid ────────────────────────────────────────────────────────────

// Figma create-post-redesign (93:4): preview pane over the grid, Select
// toggle for multi-pick, Next in the header. One tap previews AND selects;
// Select mode turns taps into badge-numbered multi-selection.
@Suppress("LongParameterList", "LongMethod")
@Composable
private fun GalleryGrid(
    kind: GalleryKind,
    title: String,
    onClose: () -> Unit,
    onCamera: () -> Unit,
    onPicked: (List<Uri>) -> Unit,
) {
    val context = LocalContext.current
    val media by produceState(initialValue = emptyList<GalleryItem>(), kind) {
        value = withContext(Dispatchers.IO) { queryMedia(context, kind) }
    }
    val selected = remember { emptyList<Uri>().toMutableStateList() }
    var focused by remember { mutableStateOf<GalleryItem?>(null) }
    var selectMode by remember { mutableStateOf(false) }
    val multiSelect = kind == GalleryKind.Photos

    // The newest item previews and preselects itself, so posting the latest
    // shot is header-Next away with zero grid taps.
    LaunchedEffect(media) {
        if (focused == null && media.isNotEmpty()) {
            focused = media.first()
            if (selected.isEmpty()) selected.add(media.first().uri)
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = UsTheme.spacing.m,
                    vertical = UsTheme.spacing.m,
                ),
        ) {
            // The way OUT of the create flow — abandoning a post mid-pick must
            // never require finishing it.
            IconButton(
                onClick = onClose,
                modifier = Modifier.testTag("create-gallery-close"),
            ) {
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = "Close",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            Text(
                title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            if (selected.isNotEmpty()) {
                Button(
                    onClick = { onPicked(selected.toList()) },
                    modifier = Modifier.testTag("create-gallery-next"),
                ) {
                    Text(if (selected.size > 1) "Next (${selected.size})" else "Next")
                }
            }
        }

        PreviewPane(focused = focused, selected = selected)

        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = UsTheme.spacing.xl,
                    vertical = UsTheme.spacing.m,
                ),
        ) {
            Text(
                "Recents",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            if (multiSelect) {
                SelectPill(
                    active = selectMode,
                    onToggle = {
                        selectMode = !selectMode
                        if (!selectMode) {
                            // Leaving multi-select collapses to the previewed
                            // item — what you see is what Next will post.
                            val keep = focused?.uri
                            selected.clear()
                            keep?.let(selected::add)
                        }
                    },
                )
            }
        }

        LazyVerticalGrid(
            columns = GridCells.Fixed(GRID_COLUMNS),
            horizontalArrangement = Arrangement.spacedBy(GRID_GAP),
            verticalArrangement = Arrangement.spacedBy(GRID_GAP),
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f),
        ) {
            item(key = "camera") { CameraTile(kind = kind, onClick = onCamera) }
            items(media, key = { it.id }) { item ->
                MediaTile(
                    item = item,
                    order = if (selectMode) selected.indexOf(item.uri) else -1,
                    onClick = {
                        focused = item
                        if (multiSelect && selectMode) {
                            if (selected.contains(item.uri)) {
                                selected.remove(item.uri)
                            } else if (selected.size < MAX_SELECT) {
                                selected.add(item.uri)
                            }
                        } else {
                            selected.clear()
                            selected.add(item.uri)
                        }
                    },
                )
            }
        }
    }
}

/**
 * The big look-at-it pane: whatever tile was tapped last, rendered large.
 * Dots below mirror the multi-selection, the filled dot being the previewed
 * item's place in it.
 */
@Composable
private fun PreviewPane(focused: GalleryItem?, selected: List<Uri>) {
    val context = LocalContext.current
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl)
            .height(PREVIEW_HEIGHT)
            .clip(RoundedCornerShape(PREVIEW_CORNER))
            .background(UsTheme.extended.bgCardSolid),
        contentAlignment = Alignment.Center,
    ) {
        val preview by produceState<ImageBitmap?>(initialValue = null, focused?.uri) {
            value = focused?.let {
                withContext(Dispatchers.IO) { loadThumbnail(context, it, PREVIEW_PX) }
            }
        }
        preview?.let {
            Image(
                bitmap = it,
                contentDescription = "Selected media preview",
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
    if (selected.size > 1) {
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = UsTheme.spacing.m)
                .wrapContentWidth(Alignment.CenterHorizontally),
        ) {
            val focusIndex = selected.indexOf(focused?.uri)
            repeat(selected.size) { index ->
                Box(
                    modifier = Modifier
                        .size(PREVIEW_DOT)
                        .clip(CircleShape)
                        .background(
                            if (index == focusIndex) {
                                MaterialTheme.colorScheme.primary
                            } else {
                                UsTheme.extended.textGhost
                            },
                        ),
                )
            }
        }
    }
}

/** Multi-select toggle, per the redesign's Select chip. */
@Composable
private fun SelectPill(active: Boolean, onToggle: () -> Unit) {
    Text(
        "Select",
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Bold,
        color = if (active) MaterialTheme.colorScheme.onPrimary else UsTheme.extended.textPrimary,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(
                if (active) MaterialTheme.colorScheme.primary else UsTheme.extended.bgCardSolid,
            )
            .clickable(onClick = onToggle)
            .padding(
                horizontal = UsTheme.spacing.l,
                vertical = UsTheme.spacing.s,
            )
            .semantics {
                contentDescription = if (active) "Select multiple, on" else "Select multiple"
            }
            .testTag("create-gallery-select"),
    )
}

/** The first tile of every gallery: capture something new instead. */
@Composable
private fun CameraTile(kind: GalleryKind, onClick: () -> Unit) {
    Column(
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .aspectRatio(1f)
            .background(UsTheme.extended.bgCardSolid)
            .clickable(onClick = onClick)
            .semantics {
                contentDescription =
                    if (kind == GalleryKind.Photos) "Take a photo" else "Record a video"
            }
            .testTag("create-source-camera"),
    ) {
        Icon(
            imageVector = UsIcons.Camera,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(CAMERA_GLYPH),
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            "Camera",
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun MediaTile(item: GalleryItem, order: Int, onClick: () -> Unit) {
    val context = LocalContext.current
    Box(
        modifier = Modifier
            .aspectRatio(1f)
            .background(UsTheme.extended.bgCardSolid)
            .clickable(onClick = onClick),
    ) {
        val thumb by produceState<ImageBitmap?>(initialValue = null, item.uri) {
            value = withContext(Dispatchers.IO) { loadThumbnail(context, item) }
        }
        thumb?.let {
            Image(
                bitmap = it,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (order >= 0) {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.s)
                    .size(SELECT_BADGE)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.primary),
            ) {
                Text(
                    "${order + 1}",
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onPrimary,
                )
            }
        }
        item.durationMs?.let { duration ->
            Text(
                formatDuration(duration),
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(UsTheme.spacing.s)
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .background(DURATION_SCRIM)
                    .padding(
                        horizontal = UsTheme.spacing.s,
                        vertical = UsTheme.spacing.xs,
                    ),
            )
        }
    }
}

// ── Permission-denied fallback ──────────────────────────────────────────

/**
 * Media permission denied: the in-app grid cannot exist, but posting still
 * can. The system picker needs no permission, so it becomes the way in.
 */
@Suppress("LongParameterList")
@Composable
private fun PermissionFallback(
    title: String,
    subtitle: String,
    cameraDescription: String,
    onClose: () -> Unit,
    onCamera: () -> Unit,
    onSystemPicker: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xl),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth(),
        ) {
            IconButton(onClick = onClose) {
                Icon(
                    imageVector = UsIcons.Close,
                    contentDescription = "Close",
                    tint = UsTheme.extended.textPrimary,
                )
            }
            Text(
                title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            IconButton(onClick = onCamera) {
                Icon(
                    imageVector = UsIcons.Camera,
                    contentDescription = cameraDescription,
                    tint = UsTheme.extended.textPrimary,
                )
            }
        }
        Text(
            subtitle,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f),
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                "Allow media access to pick right here — or use the system picker.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Spacer(Modifier.height(UsTheme.spacing.l))
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.large))
                    .background(UsTheme.extended.bgCard)
                    .clickable(onClick = onSystemPicker)
                    .padding(UsTheme.spacing.l)
                    .testTag("create-source-gallery"),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    "Open system picker",
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

// ── MediaStore plumbing ─────────────────────────────────────────────────

private data class GalleryItem(
    val id: Long,
    val uri: Uri,
    val durationMs: Long?,
)

private fun queryMedia(context: Context, kind: GalleryKind): List<GalleryItem> {
    val collection = if (kind == GalleryKind.Videos) {
        MediaStore.Video.Media.EXTERNAL_CONTENT_URI
    } else {
        MediaStore.Images.Media.EXTERNAL_CONTENT_URI
    }
    val durationColumn = MediaStore.Video.Media.DURATION
    val projection = if (kind == GalleryKind.Videos) {
        arrayOf(MediaStore.MediaColumns._ID, durationColumn)
    } else {
        arrayOf(MediaStore.MediaColumns._ID)
    }
    val cursor = context.contentResolver.query(
        collection,
        projection,
        null,
        null,
        "${MediaStore.MediaColumns.DATE_ADDED} DESC",
    ) ?: return emptyList()
    cursor.use { c ->
        val idCol = c.getColumnIndexOrThrow(MediaStore.MediaColumns._ID)
        val durCol = if (kind == GalleryKind.Videos) c.getColumnIndex(durationColumn) else -1
        return buildList {
            while (c.moveToNext() && size < RECENT_LIMIT) {
                val id = c.getLong(idCol)
                add(
                    GalleryItem(
                        id = id,
                        uri = ContentUris.withAppendedId(collection, id),
                        durationMs = if (durCol >= 0) c.getLong(durCol) else null,
                    ),
                )
            }
        }
    }
}

private fun loadThumbnail(
    context: Context,
    item: GalleryItem,
    sizePx: Int = THUMB_PX,
): ImageBitmap? = runCatching {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        context.contentResolver.loadThumbnail(item.uri, Size(sizePx, sizePx), null)
    } else {
        @Suppress("DEPRECATION")
        if (item.durationMs != null) {
            MediaStore.Video.Thumbnails.getThumbnail(
                context.contentResolver,
                item.id,
                MediaStore.Video.Thumbnails.MINI_KIND,
                null,
            )
        } else {
            MediaStore.Images.Thumbnails.getThumbnail(
                context.contentResolver,
                item.id,
                MediaStore.Images.Thumbnails.MINI_KIND,
                null,
            )
        }
    }
}.getOrNull()?.asImageBitmap()

private fun formatDuration(ms: Long): String {
    val totalSeconds = ms / MS_PER_SECOND
    val minutes = totalSeconds / SECONDS_PER_MINUTE
    val seconds = totalSeconds % SECONDS_PER_MINUTE
    return "%d:%02d".format(minutes, seconds)
}

// ── Constants ───────────────────────────────────────────────────────────

private const val GRID_COLUMNS = 4
private const val MAX_SELECT = 10
private const val RECENT_LIMIT = 120
private const val THUMB_PX = 512
private const val PREVIEW_PX = 1024
private val PREVIEW_HEIGHT = 300.dp
private val PREVIEW_CORNER = 24.dp
private val PREVIEW_DOT = 6.dp
private const val MS_PER_SECOND = 1000L
private const val SECONDS_PER_MINUTE = 60L
private val GRID_GAP = 2.dp
private val CAMERA_GLYPH = 28.dp
private val SELECT_BADGE = 24.dp

@Suppress("MagicNumber")
private val DURATION_SCRIM = Color(0x99000000)
