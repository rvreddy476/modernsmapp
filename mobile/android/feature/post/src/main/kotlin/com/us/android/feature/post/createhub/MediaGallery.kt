// MatchingDeclarationName: this file's primary export is the MediaGallerySurface
// composable; GalleryKind is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.createhub

import android.app.Activity
import android.content.ContentUris
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.provider.Settings
import android.util.Size
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
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
import androidx.compose.foundation.lazy.grid.GridItemSpan
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
import androidx.compose.runtime.mutableIntStateOf
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
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
 * the founder asked for by name — and Browse as the second, so any file on
 * the device is reachable through the system picker whatever the app has
 * been allowed to read. Photos multi-select (the studio takes up to ten) and
 * confirm with Next; a video is single-tap because only one can post.
 *
 * Access comes in three sizes — see [MediaAccess]. Partial access (Android
 * 14's "Select photos") is a WORKING gallery of the chosen subset, with a
 * banner saying so and the way to choose more; the founder's phone held
 * exactly that grant with nothing selected and read as "unable to see
 * gallery" (2026-09-04). Denied keeps the fallback, whose Allow, Browse and
 * Camera each still lead somewhere.
 */
internal enum class GalleryKind { Photos, Videos }

/**
 * What the app may read of the user's media, from the runtime grants.
 *
 *  - [Full]: a real media permission is held (READ_MEDIA_IMAGES /
 *    READ_MEDIA_VIDEO, or READ_EXTERNAL_STORAGE before 13). The grid is the
 *    library.
 *  - [Partial]: ONLY READ_MEDIA_VISUAL_USER_SELECTED is held — Android 14+
 *    "Select photos". MediaStore serves the chosen subset, which may be
 *    empty. The grid shows it under a banner with Manage and Settings.
 *  - [Denied]: nothing. The fallback: Allow access, Browse, Camera.
 */
internal enum class MediaAccess { Full, Partial, Denied }

/**
 * The access rule, pure so it is a table test: [grants] is permission →
 * granted for the permissions the surface asked for. Any granted permission
 * other than the user-selected one is full access; the user-selected one
 * alone is partial; nothing granted is denied.
 */
internal fun mediaAccessOf(grants: Map<String, Boolean>): MediaAccess {
    val granted = grants.filterValues { it }.keys
    return when {
        (granted - PARTIAL_ACCESS_PERMISSION).isNotEmpty() -> MediaAccess.Full
        PARTIAL_ACCESS_PERMISSION in granted -> MediaAccess.Partial
        else -> MediaAccess.Denied
    }
}

/** Android 14's "Select photos" grant — a subset, never the library. */
internal const val PARTIAL_ACCESS_PERMISSION = "android.permission.READ_MEDIA_VISUAL_USER_SELECTED"

/** One cell of the Recents grid, in the order [galleryTiles] fixes. */
internal sealed interface GalleryTile<out T> {
    /** Capture something new. Always first. */
    data object Camera : GalleryTile<Nothing>

    /** The system picker — any file, under any grant. Always second. */
    data object Browse : GalleryTile<Nothing>

    /** A media item the grid can show. */
    data class Media<T>(val item: T) : GalleryTile<T>
}

/** Camera, Browse, then the media newest-first, exactly as queried. */
internal fun <T> galleryTiles(media: List<T>): List<GalleryTile<T>> = buildList {
    add(GalleryTile.Camera)
    add(GalleryTile.Browse)
    media.forEach { add(GalleryTile.Media(it)) }
}

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
    val wanted = remember(kind) { mediaPermissions(kind) }
    var access by remember { mutableStateOf<MediaAccess?>(null) }
    // Whether the system dialog has been put to the user once this visit —
    // what tells a fresh denial from a "don't ask again" one on Allow.
    var asked by remember { mutableStateOf(false) }
    // Bumped on every permission result so the grid re-queries: on Android
    // 14+ "select more photos" changes what MediaStore serves without
    // changing which permissions are held.
    var grantEpoch by remember { mutableIntStateOf(0) }

    val request = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) {
        // Read the grants back from the system rather than from the result
        // map: a partial grant reports the media permission as denied and
        // only the user-selected one as granted, and the map for a "select
        // more" round trip is not a full picture either.
        asked = true
        access = mediaAccessOf(currentGrants(context, wanted))
        grantEpoch++
    }
    val requestAgain = { request.launch(wanted.toTypedArray()) }
    val openSettings = { openAppSettings(context) }

    LaunchedEffect(Unit) {
        val current = mediaAccessOf(currentGrants(context, wanted))
        if (current == MediaAccess.Denied) requestAgain() else access = current
    }

    when (access) {
        MediaAccess.Full, MediaAccess.Partial -> GalleryGrid(
            kind = kind,
            title = title,
            partial = access == MediaAccess.Partial,
            grantEpoch = grantEpoch,
            onClose = onClose,
            onCamera = onCamera,
            onBrowse = onSystemPicker,
            onPicked = onPicked,
            onManage = requestAgain,
            onSettings = openSettings,
        )
        MediaAccess.Denied -> PermissionFallback(
            title = title,
            subtitle = subtitle,
            kind = kind,
            onClose = onClose,
            // The dialog again while the system will still show it; once it
            // won't — "don't ask again" — the app's settings page is the only
            // place the grant can change, so that is where Allow goes.
            onAllow = { if (canAskAgain(context, wanted, asked)) requestAgain() else openSettings() },
            onCamera = onCamera,
            onBrowse = onSystemPicker,
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

private fun currentGrants(context: Context, wanted: List<String>): Map<String, Boolean> =
    wanted.associateWith { ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED }

/**
 * Will the system still put the dialog up? Before the first ask, always.
 * After one, only while some wanted permission still shows a rationale —
 * the platform's own signal that the user has not said "don't ask again".
 */
private fun canAskAgain(context: Context, wanted: List<String>, asked: Boolean): Boolean {
    if (!asked) return true
    val activity = context.findActivity() ?: return true
    return wanted.any { ActivityCompat.shouldShowRequestPermissionRationale(activity, it) }
}

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

/** The app's own page in system settings — where a "don't ask again" grant is changed. */
private fun openAppSettings(context: Context) {
    val intent = Intent(
        Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
        Uri.fromParts("package", context.packageName, null),
    ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
    runCatching { context.startActivity(intent) }
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
    partial: Boolean,
    grantEpoch: Int,
    onClose: () -> Unit,
    onCamera: () -> Unit,
    onBrowse: () -> Unit,
    onPicked: (List<Uri>) -> Unit,
    onManage: () -> Unit,
    onSettings: () -> Unit,
) {
    val context = LocalContext.current
    // Null while the query runs: an empty list is a real answer, not a gap.
    val media by produceState<List<GalleryItem>?>(initialValue = null, kind, grantEpoch) {
        value = withContext(Dispatchers.IO) { queryMedia(context, kind) }
    }
    val selected = remember { emptyList<Uri>().toMutableStateList() }
    var focused by remember { mutableStateOf<GalleryItem?>(null) }
    var selectMode by remember { mutableStateOf(false) }
    val multiSelect = kind == GalleryKind.Photos

    // The newest item previews and preselects itself, so posting the latest
    // shot is header-Next away with zero grid taps.
    LaunchedEffect(media) {
        val newest = media?.firstOrNull() ?: return@LaunchedEffect
        if (focused == null) {
            focused = newest
            if (selected.isEmpty()) selected.add(newest.uri)
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        GalleryHeader(
            title = title,
            selectedCount = selected.size,
            onClose = onClose,
            onNext = { onPicked(selected.toList()) },
        )

        if (partial) {
            PartialAccessBanner(kind = kind, onManage = onManage, onSettings = onSettings)
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

        val tiles = galleryTiles(media.orEmpty())
        LazyVerticalGrid(
            columns = GridCells.Fixed(GRID_COLUMNS),
            horizontalArrangement = Arrangement.spacedBy(GRID_GAP),
            verticalArrangement = Arrangement.spacedBy(GRID_GAP),
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .testTag("create-gallery-grid"),
        ) {
            items(tiles, key = { it.key() }) { tile ->
                when (tile) {
                    GalleryTile.Camera -> CameraTile(kind = kind, onClick = onCamera)
                    GalleryTile.Browse -> BrowseTile(onClick = onBrowse)
                    is GalleryTile.Media -> MediaTile(
                        item = tile.item,
                        order = if (selectMode) selected.indexOf(tile.item.uri) else -1,
                        onClick = {
                            focused = tile.item
                            selected.pick(tile.item.uri, multi = multiSelect && selectMode)
                        },
                    )
                }
            }
            // Granted, queried, nothing: say so under the tiles, with the two
            // ways out — otherwise two lonely tiles read as a broken grid.
            if (media?.isEmpty() == true) {
                item(key = "empty", span = { GridItemSpan(maxLineSpan) }) {
                    EmptyMedia(kind = kind, partial = partial)
                }
            }
        }
    }
}

/** Close, the title, and Next once something is selected. */
@Composable
private fun GalleryHeader(title: String, selectedCount: Int, onClose: () -> Unit, onNext: () -> Unit) {
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
        if (selectedCount > 0) {
            Button(
                onClick = onNext,
                modifier = Modifier.testTag("create-gallery-next"),
            ) {
                Text(if (selectedCount > 1) "Next ($selectedCount)" else "Next")
            }
        }
    }
}

/**
 * A tap on a media tile. In multi-select it toggles the item, up to the
 * studio's ten; otherwise the tap IS the selection — one item, this one.
 */
private fun MutableList<Uri>.pick(uri: Uri, multi: Boolean) {
    if (!multi) {
        clear()
        add(uri)
    } else if (contains(uri)) {
        remove(uri)
    } else if (size < MAX_SELECT) {
        add(uri)
    }
}

private fun GalleryTile<GalleryItem>.key(): Any = when (this) {
    GalleryTile.Camera -> "camera"
    GalleryTile.Browse -> "browse"
    is GalleryTile.Media -> item.id
}

/**
 * Partial access, said plainly, with the two places it changes: Manage
 * re-runs the request, which on Android 14+ is the system's "select more"
 * sheet; Settings is the app's page for switching to the whole library.
 */
@Composable
private fun PartialAccessBanner(kind: GalleryKind, onManage: () -> Unit, onSettings: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl)
            .padding(bottom = UsTheme.spacing.m)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCardSolid)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
            .testTag("create-gallery-partial"),
    ) {
        Text(
            if (kind == GalleryKind.Photos) {
                "You've allowed access to only some photos."
            } else {
                "You've allowed access to only some videos."
            },
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        BannerAction(label = "Manage", onClick = onManage, tag = "create-gallery-manage")
        BannerAction(label = "Settings", onClick = onSettings, tag = "create-gallery-settings")
    }
}

@Composable
private fun BannerAction(label: String, onClick: () -> Unit, tag: String) {
    Text(
        label,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.accentSolid,
        modifier = Modifier
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .padding(vertical = UsTheme.spacing.xs)
            .semantics { role = Role.Button }
            .testTag(tag),
    )
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
    ActionTile(
        icon = UsIcons.Camera,
        label = "Camera",
        description = if (kind == GalleryKind.Photos) "Take a photo" else "Record a video",
        tag = "create-source-camera",
        onClick = onClick,
    )
}

/**
 * The second tile of every gallery: the system picker. It needs no
 * permission and sees every file, so it is the way to anything the grid
 * cannot show — under partial access, and under full access to a file the
 * MediaStore index has not caught up with.
 */
@Composable
private fun BrowseTile(onClick: () -> Unit) {
    ActionTile(
        icon = UsIcons.Folder,
        label = "Browse",
        description = "Browse files",
        tag = "create-source-browse",
        onClick = onClick,
    )
}

@Composable
private fun ActionTile(
    icon: ImageVector,
    label: String,
    description: String,
    tag: String,
    onClick: () -> Unit,
) {
    Column(
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .aspectRatio(1f)
            .background(UsTheme.extended.bgCardSolid)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics {
                contentDescription = description
                role = Role.Button
            }
            .testTag(tag),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(CAMERA_GLYPH),
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** Under the two action tiles when the query came back with nothing. */
@Composable
private fun EmptyMedia(kind: GalleryKind, partial: Boolean) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xxl)
            .testTag("create-gallery-empty"),
    ) {
        Text(
            if (kind == GalleryKind.Photos) "No photos here yet" else "No videos here yet",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            if (partial) {
                "Choose more with Manage, or Browse your files."
            } else {
                "Take one with the camera, or Browse your files."
            },
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
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
 * can. Three real ways forward, each a visible button: Allow (the dialog
 * again, or the app's settings once the system has stopped showing it),
 * Browse (the system picker needs no permission) and the camera.
 */
@Suppress("LongParameterList")
@Composable
private fun PermissionFallback(
    title: String,
    subtitle: String,
    kind: GalleryKind,
    onClose: () -> Unit,
    onAllow: () -> Unit,
    onCamera: () -> Unit,
    onBrowse: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xl)
            .testTag("create-gallery-denied"),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth(),
        ) {
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
                if (kind == GalleryKind.Photos) {
                    "Allow photo access to pick right here — or browse your files, or take a new one."
                } else {
                    "Allow video access to pick right here — or browse your files, or record a new one."
                },
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Spacer(Modifier.height(UsTheme.spacing.xl))
            UsButton(
                text = "Allow access",
                onClick = onAllow,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("create-source-allow"),
            )
            Spacer(Modifier.height(UsTheme.spacing.m))
            UsSecondaryButton(
                text = "Browse files",
                onClick = onBrowse,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("create-source-gallery"),
            )
            Spacer(Modifier.height(UsTheme.spacing.m))
            UsSecondaryButton(
                text = if (kind == GalleryKind.Photos) "Take a photo" else "Record a video",
                onClick = onCamera,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("create-source-camera"),
            )
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
