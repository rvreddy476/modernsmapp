package com.us.android.feature.chat.ui

import android.os.Build
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
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
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW
import coil3.compose.AsyncImage
import com.giphy.sdk.ui.views.GPHMediaView
import com.us.android.core.chat.data.GifInfo
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.StickerInfo
import com.us.android.core.chat.data.VoiceInfo
import com.us.android.core.chat.data.formatVoiceDuration
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.MediaSources
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.network.ApiConfig
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/** The app graph's playback and lookup seams, for bubbles that play or hydrate. */
@EntryPoint
@InstallIn(SingletonComponent::class)
interface ChatBubbleEntryPoint {
    fun playerFactory(): PlayerFactory
    fun mediaSources(): MediaSources
    fun mediaRepository(): MediaRepository
    fun profileRepository(): ProfileRepository
    fun apiConfig(): ApiConfig
}

@Composable
private fun bubbleEntry(): ChatBubbleEntryPoint {
    val context = LocalContext.current
    return remember(context) {
        EntryPointAccessors.fromApplication(context.applicationContext, ChatBubbleEntryPoint::class.java)
    }
}

/** The authorized serve route for a chat attachment (media-service checks the chat access). */
private fun ApiConfig.serveUrl(mediaId: String) = baseUrl.trimEnd('/') + "/v1/media/" + mediaId + "/serve"

/**
 * A voice message (CONTRACT §9): play/pause, the waveform with the played
 * part inked, and the length. Played through the app's ONE authenticated
 * media chain; one player per bubble, created on first play, released when
 * the bubble leaves.
 */
@OptIn(UnstableApi::class)
@Composable
internal fun VoiceBubble(mediaId: String?, voice: VoiceInfo, ink: Color) {
    val entry = bubbleEntry()
    var player by remember { mutableStateOf<androidx.media3.exoplayer.ExoPlayer?>(null) }
    var playing by remember { mutableStateOf(false) }
    var progress by remember { mutableFloatStateOf(0f) }
    DisposableEffect(Unit) { onDispose { player?.release() } }
    LaunchedEffect(playing, player) {
        val active = player ?: return@LaunchedEffect
        while (playing && isActive) {
            val duration = active.duration.takeIf { it > 0 } ?: voice.durationMs.coerceAtLeast(1)
            progress = (active.currentPosition.toFloat() / duration).coerceIn(0f, 1f)
            if (active.playbackState == Player.STATE_ENDED) {
                playing = false
                progress = 0f
                active.seekTo(0)
                active.playWhenReady = false
            }
            delay(PROGRESS_TICK_MILLIS)
        }
    }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier.widthIn(min = VOICE_MIN_WIDTH).testTag("bubble-voice"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(PLAY_TARGET)
                .clip(CircleShape)
                .background(ink.copy(alpha = PLAY_GROUND_ALPHA))
                .clickable(enabled = mediaId != null) {
                    val id = mediaId ?: return@clickable
                    val current = player ?: entry.playerFactory().create().also { created ->
                        created.setMediaSource(entry.mediaSources().create(Playback.original(entry.apiConfig().serveUrl(id))))
                        created.prepare()
                        player = created
                    }
                    playing = !playing
                    current.playWhenReady = playing
                }
                .semantics { contentDescription = if (playing) "Pause voice message" else "Play voice message" },
        ) {
            Icon(
                imageVector = if (playing) UsIcons.Pause else UsIcons.Play,
                contentDescription = null,
                tint = ink,
                modifier = Modifier.size(PLAY_GLYPH),
            )
        }
        Waveform(points = voice.waveform, progress = progress, ink = ink, modifier = Modifier.weight(1f))
        Text(
            text = formatVoiceDuration(voice.durationMs),
            style = MaterialTheme.typography.labelSmall,
            color = ink,
        )
    }
}

/** The waveform: one bar per point (0..255), the played fraction at full ink, the rest faded. */
@Composable
internal fun Waveform(points: List<Int>, progress: Float, ink: Color, modifier: Modifier = Modifier) {
    val bars = points.ifEmpty { List(FLAT_BARS) { FLAT_LEVEL } }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(BAR_GAP),
        modifier = modifier.height(WAVE_HEIGHT),
    ) {
        bars.forEachIndexed { index, level ->
            val played = (index + 1).toFloat() / bars.size <= progress
            Box(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .height(WAVE_HEIGHT * (MIN_BAR + (1f - MIN_BAR) * level.coerceIn(0, MAX_LEVEL) / MAX_LEVEL))
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(if (played) ink else ink.copy(alpha = UNPLAYED_ALPHA)),
            )
        }
    }
}

/**
 * A video attachment: its poster with a play glyph; a tap plays it full
 * screen over the authenticated chain (the delivery's HLS when it has one,
 * the original otherwise).
 */
@Composable
internal fun VideoBubble(mediaId: String, blurred: Boolean, onReveal: () -> Unit) {
    val entry = bubbleEntry()
    var open by remember { mutableStateOf(false) }
    val poster by produceState<String?>(null, mediaId) {
        value = (entry.mediaRepository().delivery(mediaId) as? AppResult.Success)?.data?.posterUrl
    }
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(VIDEO_ASPECT)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.stage)
            .clickable { if (blurred) onReveal() else open = true }
            .testTag("bubble-video"),
    ) {
        AsyncImage(
            model = poster,
            contentDescription = "Video",
            contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize().sensitiveBlur(blurred),
        )
        if (blurred) {
            SensitiveCover()
        } else {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier.size(PLAY_TARGET).background(UsTheme.extended.mediaPlate, CircleShape),
            ) {
                Icon(UsIcons.Play, contentDescription = null, tint = UsTheme.extended.onMedia, modifier = Modifier.size(PLAY_GLYPH))
            }
        }
    }
    if (open) VideoPlayerDialog(mediaId = mediaId, onDismiss = { open = false })
}

@OptIn(UnstableApi::class)
@Composable
private fun VideoPlayerDialog(mediaId: String, onDismiss: () -> Unit) {
    val entry = bubbleEntry()
    val player = remember { entry.playerFactory().create() }
    LaunchedEffect(mediaId) {
        val hls = (entry.mediaRepository().delivery(mediaId) as? AppResult.Success)?.data?.hlsUrl
        val playback = hls?.takeIf { it.isNotBlank() }?.let { Playback.hls(it) }
            ?: Playback.original(entry.apiConfig().serveUrl(mediaId))
        player.setMediaSource(entry.mediaSources().create(playback))
        player.prepare()
        player.playWhenReady = true
    }
    DisposableEffect(Unit) { onDispose { player.release() } }
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(modifier = Modifier.fillMaxSize().background(UsTheme.extended.stage).testTag("chat-video-player")) {
            PlayerSurface(player = player, surfaceType = SURFACE_TYPE_TEXTURE_VIEW, modifier = Modifier.fillMaxSize())
            IconButton(
                onClick = onDismiss,
                modifier = Modifier.align(Alignment.TopStart).statusBarsPadding().padding(UsTheme.spacing.m),
            ) {
                Icon(UsIcons.Close, contentDescription = "Close video", tint = UsTheme.extended.onMedia)
            }
        }
    }
}

/** Blurs where the platform can (API 31+); elsewhere the cover above it is opaque instead. */
internal fun Modifier.sensitiveBlur(on: Boolean): Modifier =
    if (on && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) blur(SENSITIVE_BLUR) else this

/** "Sensitive content — tap to view", over a blurred (or, before API 31, fully covered) attachment. */
@Composable
internal fun SensitiveCover() {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .fillMaxSize()
            .background(
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) UsTheme.extended.scrim else UsTheme.extended.stage,
            )
            .padding(UsTheme.spacing.m)
            .testTag("bubble-sensitive"),
    ) {
        Box(modifier = Modifier.weight(1f))
        Icon(UsIcons.EyeOff, contentDescription = null, tint = UsTheme.extended.onMedia)
        Text("May be sensitive", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.onMedia)
        Text("Tap to view", style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.onMediaMuted)
        Box(modifier = Modifier.weight(1f))
    }
}

@Composable
internal fun StickerBubble(sticker: StickerInfo) {
    AsyncImage(
        model = sticker.url,
        contentDescription = "Sticker",
        contentScale = ContentScale.Fit,
        modifier = Modifier.size(STICKER_SIZE).testTag("bubble-sticker"),
    )
}

/**
 * A GIF: Giphy's own view (GPHMediaView) when Giphy is configured for this
 * build, so it animates and carries the SDK's handling; the still (or the
 * first frame) through Coil otherwise. The url is the provider's, unchanged.
 */
@Composable
internal fun GifBubble(gif: GifInfo) {
    val context = LocalContext.current
    val aspect = if (gif.width > 0 && gif.height > 0) gif.width.toFloat() / gif.height else 1f
    val modifier = Modifier
        .width(GIF_WIDTH)
        .aspectRatio(aspect.coerceIn(MIN_GIF_ASPECT, MAX_GIF_ASPECT))
        .clip(RoundedCornerShape(UsTheme.radii.medium))
        .testTag("bubble-gif")
    if (GiphySetup.ensure(context)) {
        AndroidView(
            factory = { ctx -> GPHMediaView(ctx) },
            update = { view -> view.loadAsset(gif.url) },
            modifier = modifier,
        )
    } else {
        AsyncImage(model = gif.stillUrl ?: gif.url, contentDescription = "GIF", contentScale = ContentScale.Crop, modifier = modifier)
    }
}

/** A shared profile, hydrated through the profile read every surface uses. */
@Composable
internal fun ProfileCardBubble(userId: String, ink: Color, onOpen: (String) -> Unit) {
    val entry = bubbleEntry()
    val profile by produceState<com.us.android.core.model.Profile?>(null, userId) {
        value = (entry.profileRepository().getProfile(userId) as? AppResult.Success)?.data
    }
    val name = profile?.displayName?.ifBlank { null } ?: "Profile"
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .widthIn(min = CARD_MIN_WIDTH)
            .clickable { onOpen(userId) }
            .testTag("bubble-profile"),
    ) {
        UsAvatar(name = name, seed = userId, size = UsAvatarSize.Medium)
        Column(modifier = Modifier.weight(1f, fill = false)) {
            Text(name, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, color = ink, maxLines = 1,
                overflow = TextOverflow.Ellipsis)
            profile?.username?.takeIf { it.isNotBlank() }?.let {
                Text("@$it", style = MaterialTheme.typography.bodySmall, color = ink.copy(alpha = MUTED_INK))
            }
            Text("View profile", style = MaterialTheme.typography.labelSmall, color = ink.copy(alpha = MUTED_INK))
        }
    }
}

/** A shared sound or a shared LIVE: a labelled card that opens where it lives. */
@Composable
internal fun LinkCardBubble(title: String, detail: String, live: Boolean, ink: Color, tag: String, onOpen: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier.widthIn(min = CARD_MIN_WIDTH).clickable(onClick = onOpen).testTag(tag),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CARD_ICON)
                .background(if (live) UsTheme.extended.liveRed else ink.copy(alpha = PLAY_GROUND_ALPHA), RoundedCornerShape(UsTheme.radii.small)),
        ) {
            if (live) {
                Text("LIVE", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold, color = UsTheme.extended.onStatus)
            } else {
                Icon(UsIcons.Music, contentDescription = null, tint = ink)
            }
        }
        Column {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, color = ink)
            Text(detail, style = MaterialTheme.typography.labelSmall, color = ink.copy(alpha = MUTED_INK))
        }
    }
}

/** A call log line: centred, small, with the phone glyph (missed in the danger colour). */
@Composable
internal fun CallLogRow(message: Message) {
    val text = message.text.ifBlank { "Call" }
    val missed = text.startsWith("Missed", ignoreCase = true)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s, Alignment.CenterHorizontally),
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.s)
            .testTag("bubble-call"),
    ) {
        Icon(
            imageVector = UsIcons.Phone,
            contentDescription = null,
            tint = if (missed) UsTheme.extended.statusDanger else UsTheme.extended.textMuted,
            modifier = Modifier.size(CALL_GLYPH),
        )
        Text(
            text = text,
            style = MaterialTheme.typography.labelMedium,
            color = if (missed) UsTheme.extended.statusDanger else UsTheme.extended.textMuted,
        )
    }
}

/** An unsent message: never its old content. */
@Composable
internal fun TombstoneText(isOwn: Boolean, ink: Color) {
    Text(
        text = if (isOwn) "You unsent a message" else "This message was unsent",
        style = MaterialTheme.typography.bodyMedium,
        fontStyle = FontStyle.Italic,
        color = ink.copy(alpha = MUTED_INK),
        modifier = Modifier.testTag("bubble-deleted"),
    )
}

/** "Forwarded", above a forwarded message's content. */
@Composable
internal fun ForwardedLabel(ink: Color) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier.padding(bottom = UsTheme.spacing.xs).testTag("bubble-forwarded"),
    ) {
        Icon(UsIcons.Forward, contentDescription = null, tint = ink.copy(alpha = MUTED_INK), modifier = Modifier.size(CALL_GLYPH))
        Text("Forwarded", style = MaterialTheme.typography.labelSmall, fontStyle = FontStyle.Italic, color = ink.copy(alpha = MUTED_INK))
    }
}

private const val PROGRESS_TICK_MILLIS = 100L
private const val PLAY_GROUND_ALPHA = 0.12f
private const val UNPLAYED_ALPHA = 0.35f
private const val MUTED_INK = 0.7f
private const val MIN_BAR = 0.15f
private const val MAX_LEVEL = 255
private const val FLAT_BARS = 32
private const val FLAT_LEVEL = 60
private const val VIDEO_ASPECT = 4f / 5f
private const val MIN_GIF_ASPECT = 0.5f
private const val MAX_GIF_ASPECT = 2f
private val VOICE_MIN_WIDTH = 200.dp
private val PLAY_TARGET = 36.dp
private val PLAY_GLYPH = 18.dp
private val WAVE_HEIGHT = 28.dp
private val BAR_GAP = 2.dp
private val SENSITIVE_BLUR = 24.dp
private val STICKER_SIZE = 140.dp
private val GIF_WIDTH = 220.dp
private val CARD_MIN_WIDTH = 200.dp
private val CARD_ICON = 40.dp
private val CALL_GLYPH = 14.dp
