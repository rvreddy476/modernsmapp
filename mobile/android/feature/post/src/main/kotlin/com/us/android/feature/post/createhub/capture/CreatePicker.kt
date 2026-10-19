package com.us.android.feature.post.createhub.capture

import android.Manifest
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.util.Size
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.post.createhub.mediaAccessOf
import com.us.android.feature.post.createhub.MediaAccess
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The create picker (2026-10-18): photos and videos together, under All /
 * Videos / Photos tabs and a "Recents" album switcher; a three-column grid
 * where a tap numbers the item in its circle; the picked items in a tray at
 * the foot with "Next (n)". Photos (up to ten) go to the photo studio; a
 * video, which posts alone, goes to the reel flow. [onCamera], when given,
 * is the way back to the camera from the gallery-first variant.
 */
@Composable
fun CreatePicker(
    onClose: () -> Unit,
    onResult: (PickerResult) -> Unit,
    modifier: Modifier = Modifier,
    onCamera: (() -> Unit)? = null,
) {
    val context = LocalContext.current
    val wanted = remember { pickerPermissions() }
    var access by remember { mutableStateOf<MediaAccess?>(null) }
    var epoch by remember { mutableIntStateOf(0) }
    val request = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        access = mediaAccessOf(grants(context, wanted))
        epoch++
    }
    val browse = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(MAX_PICKED_PHOTOS)) { uris ->
        if (uris.isNotEmpty()) onResult(PickerResult.Photos(uris.map(Uri::toString)))
    }
    LaunchedEffect(Unit) {
        val current = mediaAccessOf(grants(context, wanted))
        if (current == MediaAccess.Denied) request.launch(wanted.toTypedArray()) else access = current
    }
    Column(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .statusBarsPadding()
            .testTag("create-picker"),
    ) {
        when (access) {
            MediaAccess.Full, MediaAccess.Partial -> PickerBody(
                epoch = epoch,
                onClose = onClose,
                onResult = onResult,
                onCamera = onCamera,
                // Limited access (2026-10-19): say why the grid is short, and let
                // the person pick more or allow all — asking again does that.
                onManageAccess = if (access == MediaAccess.Partial) {
                    { request.launch(wanted.toTypedArray()) }
                } else {
                    null
                },
            )
            MediaAccess.Denied -> Column(
                modifier = Modifier.fillMaxSize().padding(UsTheme.spacing.xxl),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l, Alignment.CenterVertically),
            ) {
                Text(
                    "Allow access to your photos and videos to pick them here, or browse your files.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
                UsButton(text = "Allow access", onClick = { request.launch(wanted.toTypedArray()) }, modifier = Modifier.fillMaxWidth())
                UsSecondaryButton(
                    text = "Browse files",
                    onClick = { browse.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                    modifier = Modifier.fillMaxWidth(),
                )
                onCamera?.let { UsSecondaryButton(text = "Use the camera", onClick = it, modifier = Modifier.fillMaxWidth()) }
                UsSecondaryButton(text = "Close", onClick = onClose, modifier = Modifier.fillMaxWidth())
            }
            null -> Unit
        }
    }
}

@Suppress("LongMethod")
@Composable
private fun ColumnScope.PickerBody(
    epoch: Int,
    onClose: () -> Unit,
    onResult: (PickerResult) -> Unit,
    onCamera: (() -> Unit)?,
    onManageAccess: (() -> Unit)?,
) {
    val context = LocalContext.current
    val all by produceState<List<PickerItem>?>(initialValue = null, epoch) {
        value = withContext(Dispatchers.IO) { queryPickerMedia(context) }
    }
    var tab by rememberSaveable { mutableStateOf(PickerTab.ALL) }
    var album by rememberSaveable { mutableStateOf(ALL_ALBUMS) }
    var albumsOpen by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf(emptyList<PickerItem>()) }
    val items = pickerItems(all.orEmpty(), tab, album)

    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onClose, modifier = Modifier.testTag("create-picker-close")) {
            Icon(UsIcons.Close, contentDescription = "Close", tint = UsTheme.extended.textPrimary)
        }
        Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.Center) {
            Row(
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .clickable(role = Role.Button, onClickLabel = "Choose an album") { albumsOpen = true }
                    .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                    .testTag("create-picker-album"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            ) {
                Text(album, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold, color = UsTheme.extended.textPrimary)
                Icon(UsIcons.ChevronDown, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(CHEVRON))
            }
            DropdownMenu(expanded = albumsOpen, onDismissRequest = { albumsOpen = false }) {
                pickerAlbums(all.orEmpty()).forEach { name ->
                    DropdownMenuItem(text = { Text(name) }, onClick = {
                        album = name
                        albumsOpen = false
                    })
                }
            }
        }
        if (onCamera != null) {
            IconButton(onClick = onCamera, modifier = Modifier.testTag("create-picker-camera")) {
                Icon(UsIcons.Camera, contentDescription = "Camera", tint = UsTheme.extended.textPrimary)
            }
        } else {
            Box(Modifier.size(HEADER_SLOT))
        }
    }
    Row(
        modifier = Modifier.fillMaxWidth().selectableGroup(),
        horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
        PickerTab.entries.forEach { option ->
            val active = option == tab
            Text(
                text = option.label,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
                color = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                modifier = Modifier
                    .selectable(selected = active, role = Role.Tab) { tab = option }
                    .padding(UsTheme.spacing.l)
                    .testTag("create-picker-tab:${option.name.lowercase()}"),
            )
        }
    }
    onManageAccess?.let { LimitedAccessBanner(onManage = it) }
    LazyVerticalGrid(
        columns = GridCells.Fixed(GRID_COLUMNS),
        horizontalArrangement = Arrangement.spacedBy(GRID_GAP),
        verticalArrangement = Arrangement.spacedBy(GRID_GAP),
        modifier = Modifier.fillMaxWidth().weight(1f).testTag("create-picker-grid"),
    ) {
        items(items, key = { it.uri }) { item ->
            PickerTile(
                item = item,
                number = selectionNumber(selected, item),
                onClick = { selected = toggleSelection(selected, item) },
            )
        }
    }
    if (all?.isEmpty() == true) {
        Text(
            "Nothing here yet. Take a photo, or browse your files.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(UsTheme.spacing.xxl),
        )
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
            .testTag("create-picker-tray"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        LazyRow(modifier = Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            items(selected, key = { it.uri }) { item ->
                Box(modifier = Modifier.size(TRAY_THUMB).clip(RoundedCornerShape(UsTheme.radii.small))) {
                    Thumb(item)
                }
            }
        }
        UsButton(
            text = nextLabel(selected.size),
            onClick = { pickerResult(selected)?.let(onResult) },
            enabled = selected.isNotEmpty(),
            modifier = Modifier.testTag("create-picker-next"),
        )
    }
}

@Composable
private fun PickerTile(item: PickerItem, number: Int?, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .aspectRatio(1f)
            .background(UsTheme.extended.bgRaised)
            .clickable(role = Role.Checkbox, onClick = onClick)
            .semantics { contentDescription = (if (item.isVideo) "Video" else "Photo") + (number?.let { ", picked $it" } ?: "") }
            .testTag("create-picker-item"),
    ) {
        Thumb(item)
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(UsTheme.spacing.s)
                .size(BADGE)
                .clip(CircleShape)
                .background(if (number != null) UsTheme.extended.accentStrong else UsTheme.extended.mediaPlate)
                .border(BADGE_RING, UsTheme.extended.onMedia, CircleShape),
        ) {
            number?.let {
                Text("$it", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold, color = UsTheme.extended.onAccent)
            }
        }
        item.durationMs?.let { ms ->
            Text(
                text = "%d:%02d".format(ms / MS_PER_MINUTE, (ms / MS_PER_SECOND) % SECONDS_PER_MINUTE),
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.onMedia,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(UsTheme.spacing.s)
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .background(UsTheme.extended.mediaPlate)
                    .padding(horizontal = UsTheme.spacing.xs),
            )
        }
    }
}

@Composable
private fun Thumb(item: PickerItem) {
    val context = LocalContext.current
    val thumb by produceState<ImageBitmap?>(initialValue = null, item.uri) {
        value = withContext(Dispatchers.IO) { loadThumb(context, Uri.parse(item.uri)) }
    }
    thumb?.let { Image(bitmap = it, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize()) }
}

private fun pickerPermissions(): List<String> = when {
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE -> listOf(
        Manifest.permission.READ_MEDIA_IMAGES,
        Manifest.permission.READ_MEDIA_VIDEO,
        Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED,
    )
    Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
        listOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
    else -> listOf(Manifest.permission.READ_EXTERNAL_STORAGE)
}

private fun grants(context: Context, wanted: List<String>): Map<String, Boolean> =
    wanted.associateWith { ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED }

/** Photos and videos together, newest first, with their album. */
internal fun queryPickerMedia(context: Context): List<PickerItem> {
    val collection = MediaStore.Files.getContentUri("external")
    val projection = arrayOf(
        MediaStore.MediaColumns._ID,
        MediaStore.Files.FileColumns.MEDIA_TYPE,
        MediaStore.Video.VideoColumns.DURATION,
        MediaStore.MediaColumns.BUCKET_DISPLAY_NAME,
    )
    val selection = "${MediaStore.Files.FileColumns.MEDIA_TYPE} IN (?, ?)"
    val args = arrayOf(
        MediaStore.Files.FileColumns.MEDIA_TYPE_IMAGE.toString(),
        MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO.toString(),
    )
    val cursor = runCatching {
        context.contentResolver.query(collection, projection, selection, args, "${MediaStore.MediaColumns.DATE_ADDED} DESC")
    }.getOrNull() ?: return emptyList()
    return cursor.use { c ->
        val id = c.getColumnIndexOrThrow(MediaStore.MediaColumns._ID)
        val type = c.getColumnIndexOrThrow(MediaStore.Files.FileColumns.MEDIA_TYPE)
        val duration = c.getColumnIndex(MediaStore.Video.VideoColumns.DURATION)
        val bucket = c.getColumnIndex(MediaStore.MediaColumns.BUCKET_DISPLAY_NAME)
        buildList {
            while (c.moveToNext() && size < RECENT_LIMIT) {
                val video = c.getInt(type) == MediaStore.Files.FileColumns.MEDIA_TYPE_VIDEO
                val base = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
                add(
                    PickerItem(
                        uri = ContentUris.withAppendedId(base, c.getLong(id)).toString(),
                        isVideo = video,
                        durationMs = if (video && duration >= 0) c.getLong(duration) else null,
                        album = if (bucket >= 0) c.getString(bucket) else null,
                    ),
                )
            }
        }
    }
}

internal fun loadThumb(context: Context, uri: Uri): ImageBitmap? = runCatching {
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
        context.contentResolver.loadThumbnail(uri, Size(THUMB_PX, THUMB_PX), null).asImageBitmap()
    } else {
        null
    }
}.getOrNull()

/**
 * The note over a limited grid: the person chose some photos only, so the
 * grid shows those. "Manage" asks again, which offers picking more or
 * allowing all.
 */
@Composable
private fun LimitedAccessBanner(onManage: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.xs)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCardSolid)
            .padding(start = UsTheme.spacing.l)
            .testTag("create-picker-limited"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "You've allowed access to some photos and videos.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onManage, modifier = Modifier.testTag("create-picker-manage")) { Text("Manage") }
    }
}

private const val GRID_COLUMNS = 3
private const val RECENT_LIMIT = 300
private const val THUMB_PX = 384
private const val MS_PER_SECOND = 1_000L
private const val MS_PER_MINUTE = 60_000L
private const val SECONDS_PER_MINUTE = 60L
private val GRID_GAP = 2.dp
private val BADGE = 22.dp
private val BADGE_RING = 1.5.dp
private val CHEVRON = 18.dp
private val TRAY_THUMB = 44.dp
private val HEADER_SLOT = 48.dp
