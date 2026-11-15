package com.us.android.feature.echo.ui

import android.graphics.Bitmap
import android.media.MediaMetadataRetriever
import android.net.Uri
import androidx.annotation.OptIn
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.compose.ContentFrame
import androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.ui.ForegroundPlayback
import com.us.android.core.media.ui.VideoLoadingIndicator
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteMedia
import com.us.android.feature.echo.data.StagedMedia
import com.us.android.feature.echo.data.videoDurationLabel
import com.us.android.feature.echo.data.videoTooLongNotice
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.withContext

/*
 * Video in notes (echo-v2-contract.md "Video in notes", 15 Nov 2026): one
 * video on its own, in the card's media area with the pictures' rounded
 * frame. Played over the app's ONE player stack (:core:media PlayerFactory
 * and MediaSources, HLS through the authenticated chain), on a media3
 * surface, as chat's video bubble does: timelines play it muted while the
 * card is mostly on screen (one at a time; a tap turns the sound on), the
 * note page plays it with controls.
 */

/** The app graph's playback seams, for a card that plays. */
@EntryPoint
@InstallIn(SingletonComponent::class)
interface EchoVideoEntryPoint {
    fun playerFactory(): PlayerFactory
    fun mediaSources(): MediaSources
    fun mediaUrlResolver(): MediaUrlResolver
}

@Composable
private fun videoEntry(): EchoVideoEntryPoint {
    val context = LocalContext.current
    return remember(context) {
        EntryPointAccessors.fromApplication(context.applicationContext, EchoVideoEntryPoint::class.java)
    }
}

/**
 * Which timeline video may play: the last one that came mostly into view.
 * A video the viewer started on a note page is [pinned], and holds every
 * autoplay still until it is paused or leaves.
 */
internal object EchoVideoFocus {
    var active by mutableStateOf<Any?>(null)
        private set
    var pinned by mutableStateOf<Any?>(null)
        private set

    fun claim(key: Any) {
        active = key
    }

    fun release(key: Any) {
        if (active === key) active = null
        if (pinned === key) pinned = null
    }

    fun pin(key: Any, on: Boolean) {
        if (on) pinned = key else if (pinned === key) pinned = null
    }
}

/**
 * A note's video: "Processing video…" while it transcodes (only its author
 * sees the note then), the muted timeline player or the note page's player
 * with controls, and under it the author's notice when it runs longer than
 * the note allows.
 */
@Composable
internal fun NoteVideo(
    note: Note,
    video: NoteMedia,
    posterUrl: String?,
    controls: Boolean,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth().testTag("echo_video:${video.mediaId}")) {
        val hls = video.hlsUrl
        when {
            note.isProcessing || hls == null -> ProcessingTile(posterUrl)
            controls -> PageVideo(hls = hls, posterUrl = posterUrl, durationMs = video.durationMs)
            else -> TimelineVideo(hls = hls, posterUrl = posterUrl, durationMs = video.durationMs)
        }
        if (note.videoTooLong) {
            Text(
                text = videoTooLongNotice(video.durationMs),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.statusWarning,
                modifier = Modifier.padding(top = UsTheme.spacing.s).testTag("echo_video_too_long"),
            )
        }
    }
}

/** The rounded 16:9 frame every Echo video sits in, the pictures' border included. */
@Composable
private fun VideoFrame(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(VIDEO_ASPECT)
            .clip(shape)
            .background(UsTheme.extended.stage)
            .border(1.dp, UsTheme.extended.borderSubtle, shape),
        content = content,
    )
}

@Composable
private fun ProcessingTile(posterUrl: String?) {
    VideoFrame(modifier = Modifier.testTag("echo_video_processing")) {
        posterUrl?.let {
            AsyncImage(
                model = it,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        Text(
            text = PROCESSING_VIDEO,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.onMedia,
            modifier = Modifier
                .align(Alignment.Center)
                .background(UsTheme.extended.mediaPlate, RoundedCornerShape(UsTheme.radii.full))
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s),
        )
    }
}

/**
 * A timeline video: muted, looping, playing only while at least 60% of it
 * is on screen and it is the one [EchoVideoFocus] picked; a tap turns the
 * sound on or off. The player exists only while it plays.
 */
@OptIn(UnstableApi::class)
@Composable
private fun TimelineVideo(hls: String, posterUrl: String?, durationMs: Long) {
    val key = remember { Any() }
    var visible by remember { mutableStateOf(false) }
    var foreground by remember { mutableStateOf(true) }
    var muted by remember { mutableStateOf(true) }
    LaunchedEffect(visible) { if (visible) EchoVideoFocus.claim(key) else EchoVideoFocus.release(key) }
    DisposableEffect(key) { onDispose { EchoVideoFocus.release(key) } }
    ForegroundPlayback(pause = { foreground = false }, resume = { foreground = true })
    val playing = visible && foreground && EchoVideoFocus.active === key && EchoVideoFocus.pinned == null
    val player = rememberNotePlayer(active = playing, hls = hls, longForm = false)
    LaunchedEffect(player, muted) { player?.volume = if (muted) 0f else 1f }
    VideoFrame(
        modifier = Modifier
            .onGloballyPositioned { coordinates ->
                val height = coordinates.size.height
                visible = height > 0 && coordinates.boundsInWindow().height >= height * VISIBLE_SHARE
            }
            .clickable { muted = !muted }
            .semantics { contentDescription = if (muted) "Video, muted. Tap for sound" else "Video. Tap to mute" }
            .testTag("echo_video_timeline"),
    ) {
        Poster(posterUrl)
        player?.let { VideoSurface(it, posterUrl) }
        Icon(
            imageVector = if (muted) UsIcons.SoundOff else UsIcons.SoundOn,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
            modifier = Modifier
                .align(Alignment.BottomStart)
                .padding(UsTheme.spacing.s)
                .background(UsTheme.extended.mediaPlate, CircleShape)
                .padding(UsTheme.spacing.xs)
                .size(GLYPH_SMALL),
        )
        DurationChip(videoDurationLabel(durationMs), Modifier.align(Alignment.BottomEnd))
    }
}

/**
 * The note page's video: the poster and Play until the viewer starts it,
 * then sound on, a tap to pause or play, the played bar (tap to seek), the
 * time and a mute toggle. While it plays every timeline video holds still.
 */
@OptIn(UnstableApi::class)
@Composable
@Suppress("LongMethod") // the poster, the surface and the control bar of one player
private fun PageVideo(hls: String, posterUrl: String?, durationMs: Long) {
    val key = remember { Any() }
    var started by remember { mutableStateOf(false) }
    var muted by remember { mutableStateOf(false) }
    var playing by remember { mutableStateOf(false) }
    var position by remember { mutableLongStateOf(0L) }
    var length by remember { mutableLongStateOf(durationMs) }
    var progress by remember { mutableFloatStateOf(0f) }
    val player = rememberNotePlayer(active = started, hls = hls, longForm = true)
    DisposableEffect(key) { onDispose { EchoVideoFocus.release(key) } }
    LaunchedEffect(playing) { EchoVideoFocus.pin(key, playing) }
    LaunchedEffect(player, muted) { player?.volume = if (muted) 0f else 1f }
    ForegroundPlayback(pause = { player?.playWhenReady = false })
    LaunchedEffect(player) {
        val active = player ?: return@LaunchedEffect
        while (isActive) {
            playing = active.isPlaying
            position = active.currentPosition.coerceAtLeast(0L)
            active.duration.takeIf { it > 0 }?.let { length = it }
            progress = if (length > 0) (position.toFloat() / length).coerceIn(0f, 1f) else 0f
            delay(TICK_MILLIS)
        }
    }
    val toggle: () -> Unit = {
        val active = player
        when {
            active == null -> started = true
            active.playbackState == Player.STATE_ENDED -> {
                active.seekTo(0)
                active.playWhenReady = true
            }
            else -> active.playWhenReady = !active.playWhenReady
        }
    }
    VideoFrame(modifier = Modifier.testTag("echo_video_page")) {
        Poster(posterUrl)
        player?.let {
            VideoSurface(it, posterUrl)
            VideoLoadingIndicator(player = it, onRetry = it::prepare)
        }
        Box(
            modifier = Modifier
                .fillMaxSize()
                .clickable(onClick = toggle)
                .semantics { contentDescription = if (playing) "Pause video" else "Play video" }
                .testTag("echo_video_toggle"),
        )
        if (!playing) {
            Icon(
                imageVector = UsIcons.Play,
                contentDescription = null,
                tint = UsTheme.extended.onMedia,
                modifier = Modifier
                    .align(Alignment.Center)
                    .background(UsTheme.extended.mediaPlate, CircleShape)
                    .padding(UsTheme.spacing.l)
                    .size(GLYPH_LARGE),
            )
        }
        if (player == null) {
            DurationChip(videoDurationLabel(durationMs), Modifier.align(Alignment.BottomEnd))
        } else {
            ControlBar(
                progress = progress,
                time = videoDurationLabel(position).ifEmpty { "0:00" } + " / " + videoDurationLabel(length),
                muted = muted,
                onMute = { muted = !muted },
                onSeek = { fraction -> if (length > 0) player.seekTo((length * fraction).toLong()) },
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
    }
}

/** The played bar (tap to seek), the time and the sound toggle, on a plate along the bottom. */
@Composable
private fun ControlBar(
    progress: Float,
    time: String,
    muted: Boolean,
    onMute: () -> Unit,
    onSeek: (Float) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.scrim)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("echo_video_controls"),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(SEEK_TARGET)
                .pointerInput(Unit) {
                    detectTapGestures { offset -> if (size.width > 0) onSeek(offset.x / size.width) }
                }
                .testTag("echo_video_seek"),
            contentAlignment = Alignment.CenterStart,
        ) {
            Box(
                Modifier
                    .fillMaxWidth()
                    .height(BAR_HEIGHT)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(UsTheme.extended.onMediaMuted),
            )
            Box(
                Modifier
                    .fillMaxWidth(progress)
                    .height(BAR_HEIGHT)
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(UsTheme.extended.echo.text),
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = time,
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.onMedia,
                modifier = Modifier.weight(1f),
            )
            Icon(
                imageVector = if (muted) UsIcons.SoundOff else UsIcons.SoundOn,
                contentDescription = if (muted) "Turn sound on" else "Mute",
                tint = UsTheme.extended.onMedia,
                modifier = Modifier
                    .clip(CircleShape)
                    .clickable(onClick = onMute)
                    .padding(UsTheme.spacing.xs)
                    .size(GLYPH_SMALL)
                    .testTag("echo_video_mute"),
            )
        }
    }
}

/**
 * A player for [hls] while [active], released when it stops being active
 * or leaves: a timeline card holds none while it is off screen. The short
 * player loops silently; the long-form one plays once with sound and audio
 * focus.
 */
@OptIn(UnstableApi::class)
@Composable
private fun rememberNotePlayer(active: Boolean, hls: String, longForm: Boolean): ExoPlayer? {
    val entry = videoEntry()
    var player by remember { mutableStateOf<ExoPlayer?>(null) }
    DisposableEffect(active, hls) {
        if (!active) return@DisposableEffect onDispose {}
        val url = entry.mediaUrlResolver().hlsUrl(hls) ?: return@DisposableEffect onDispose {}
        val created = if (longForm) entry.playerFactory().createLongForm() else entry.playerFactory().create()
        created.setMediaSource(entry.mediaSources().create(Playback.hls(url)))
        if (!longForm) created.repeatMode = Player.REPEAT_MODE_ONE
        created.prepare()
        created.playWhenReady = true
        player = created
        onDispose {
            player = null
            created.release()
        }
    }
    return player
}

/** The video, fitted in the frame, its poster over it until the first frame. */
@OptIn(UnstableApi::class)
@Composable
private fun VideoSurface(player: Player, posterUrl: String?) {
    // A TextureView: the rounded frame clips it, which a SurfaceView would punch through.
    ContentFrame(
        player = player,
        modifier = Modifier.fillMaxSize(),
        surfaceType = SURFACE_TYPE_TEXTURE_VIEW,
        contentScale = ContentScale.Fit,
        shutter = { Poster(posterUrl) },
    )
}

@Composable
private fun Poster(posterUrl: String?) {
    Box(Modifier.fillMaxSize().background(UsTheme.extended.stage)) {
        posterUrl?.let {
            AsyncImage(
                model = it,
                contentDescription = null,
                contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** "2:20" in the corner, on a plate. Nothing while the length is unknown. */
@Composable
internal fun DurationChip(label: String, modifier: Modifier = Modifier) {
    if (label.isEmpty()) return
    Text(
        text = label,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.onMedia,
        modifier = modifier
            .padding(UsTheme.spacing.s)
            .background(UsTheme.extended.mediaPlate, RoundedCornerShape(UsTheme.radii.pill))
            .padding(horizontal = UsTheme.spacing.s, vertical = 2.dp)
            .testTag("echo_video_duration"),
    )
}

/**
 * The composer's chosen video: its first frame (read from the local file),
 * its length, "Uploading video… 42%" while it goes up or "Upload failed",
 * and the remove ×.
 */
@Composable
internal fun StagedVideo(media: StagedMedia, onRemove: () -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val frame by produceState<Bitmap?>(null, media.localKey) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                val retriever = MediaMetadataRetriever()
                try {
                    retriever.setDataSource(context, Uri.parse(media.localKey))
                    retriever.getFrameAtTime(0)
                } finally {
                    retriever.release()
                }
            }.getOrNull()
        }
    }
    VideoFrame(modifier = modifier.testTag("echo_staged_video")) {
        frame?.let {
            Image(
                bitmap = it.asImageBitmap(),
                contentDescription = "Video",
                contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize(),
            )
        }
        val status = when {
            media.failed -> UPLOAD_FAILED
            media.uploading -> stagedVideoStatus(media.progress)
            else -> null
        }
        status?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.onMedia,
                modifier = Modifier
                    .align(Alignment.Center)
                    .background(UsTheme.extended.mediaPlate, RoundedCornerShape(UsTheme.radii.full))
                    .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                    .testTag("echo_staged_video_status"),
            )
        }
        if (media.uploading) {
            Box(
                Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth(media.progress.coerceIn(0f, 1f))
                    .height(BAR_HEIGHT)
                    .background(UsTheme.extended.echo.text),
            )
        }
        DurationChip(videoDurationLabel(media.durationMs), Modifier.align(Alignment.BottomEnd))
        Icon(
            UsIcons.Close,
            contentDescription = "Remove video",
            tint = UsTheme.extended.onMedia,
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(UsTheme.spacing.s)
                .background(UsTheme.extended.scrim, CircleShape)
                .clickable(onClick = onRemove)
                .padding(UsTheme.spacing.xs)
                .size(GLYPH_SMALL)
                .testTag("echo_staged_video_remove"),
        )
    }
}

/** A video in a picture grid (a quoted note): its poster, a play glyph and its length. */
@Composable
internal fun VideoPosterTile(item: NoteMedia, posterUrl: String?, modifier: Modifier = Modifier) {
    Box(modifier = modifier.background(UsTheme.extended.stage)) {
        posterUrl?.let {
            AsyncImage(
                model = it,
                contentDescription = item.altText.ifBlank { "Video" },
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        Icon(
            imageVector = UsIcons.Play,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
            modifier = Modifier
                .align(Alignment.Center)
                .background(UsTheme.extended.mediaPlate, CircleShape)
                .padding(UsTheme.spacing.m)
                .size(GLYPH_SMALL),
        )
        DurationChip(videoDurationLabel(item.durationMs), Modifier.align(Alignment.BottomEnd))
    }
}

/** "Uploading video…", then "Uploading video… 42%" once the bytes start moving. */
internal fun stagedVideoStatus(progress: Float): String {
    val percent = (progress.coerceIn(0f, 1f) * PERCENT).toInt()
    return if (percent <= 0) UPLOADING_VIDEO else "$UPLOADING_VIDEO $percent%"
}

internal const val PROCESSING_VIDEO = "Processing video…"
internal const val UPLOADING_VIDEO = "Uploading video…"
private const val UPLOAD_FAILED = "Upload failed"
private const val VIDEO_ASPECT = 16f / 9f
private const val VISIBLE_SHARE = 0.6f
private const val TICK_MILLIS = 250L
private const val PERCENT = 100
private val GLYPH_SMALL = 16.dp
private val GLYPH_LARGE = 28.dp
private val BAR_HEIGHT = 3.dp
private val SEEK_TARGET = 20.dp
