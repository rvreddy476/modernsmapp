package com.us.android.feature.dating.clips

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.ProgressiveMediaSource
import androidx.media3.ui.compose.ContentFrame
import androidx.media3.ui.compose.SURFACE_TYPE_TEXTURE_VIEW
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerFactory
import com.us.android.core.network.di.AuthenticatedClient
import dagger.hilt.android.lifecycle.HiltViewModel
import okhttp3.OkHttpClient
import javax.inject.Inject
import javax.inject.Singleton

/**
 * [ClipEngine] over an ExoPlayer from `:core:media`'s factory.
 *
 * The clip's route needs the bearer and answers 307 to a short-lived media
 * URL, so it is read through the app's AUTHENTICATED OkHttp client — the same
 * one dating photos load through: the bearer goes to the API origin only, and
 * OkHttp follows the 307 without it. And, like a dating photo, it is never
 * written to disk: no media cache. The route stays the same while the clip
 * behind it can be replaced or withdrawn, and a clip must not outlive the
 * viewer's permission to hear it.
 */
@OptIn(UnstableApi::class)
class ExoClipEngine internal constructor(
    private val exo: ExoPlayer,
    private val dataSources: OkHttpDataSource.Factory,
    private val owner: ClipPlayback,
) : ClipEngine {

    /** For the video surface. */
    val player: Player get() = exo

    private val listener = object : Player.Listener {
        override fun onPlaybackStateChanged(playbackState: Int) {
            when (playbackState) {
                Player.STATE_READY -> owner.onReady()
                Player.STATE_ENDED -> owner.onEnded()
                else -> Unit
            }
        }

        override fun onPlayerError(error: PlaybackException) = owner.onError()
    }

    init {
        // The factory's players loop and start silent, as a reel does; a clip plays once.
        exo.repeatMode = Player.REPEAT_MODE_OFF
        exo.volume = 0f
        // A voice answer takes audio focus like any audio the viewer started;
        // a video, which starts muted, leaves the viewer's music alone.
        exo.setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(C.USAGE_MEDIA)
                .setContentType(if (owner.kind == ClipKind.AUDIO) C.AUDIO_CONTENT_TYPE_SPEECH else C.AUDIO_CONTENT_TYPE_MOVIE)
                .build(),
            owner.kind == ClipKind.AUDIO,
        )
        exo.setHandleAudioBecomingNoisy(true)
        exo.addListener(listener)
    }

    override fun load(url: String) {
        exo.setMediaSource(ProgressiveMediaSource.Factory(dataSources).createMediaSource(MediaItem.fromUri(url)))
        exo.prepare()
    }

    override fun setPlaying(play: Boolean) {
        if (exo.playWhenReady != play) exo.playWhenReady = play
    }

    override fun setVolume(volume: Float) {
        if (exo.volume != volume) exo.volume = volume
    }

    override fun rewind() = exo.seekTo(0L)

    override fun release() {
        exo.removeListener(listener)
        exo.release()
    }
}

/** Makes clip engines: the shared player factory, the authenticated client, no cache. */
@Singleton
@OptIn(UnstableApi::class)
class ClipEngines @Inject constructor(
    private val playerFactory: PlayerFactory,
    @AuthenticatedClient client: OkHttpClient,
) {
    private val dataSources = OkHttpDataSource.Factory(client)

    fun create(owner: ClipPlayback): ClipEngine = ExoClipEngine(playerFactory.create(), dataSources, owner)
}

/** A screen's clips: how each is made, and which one may play. */
@HiltViewModel
class ClipPlaybackViewModel @Inject constructor(private val engines: ClipEngines) : ViewModel() {

    val focus = ClipFocus()

    fun playback(clip: PromptClipUi): ClipPlayback = ClipPlayback(clip.kind, clip.url, engines::create)
}

/**
 * A card's clip: a compact voice row or a muted video. Nothing plays until
 * Play is tapped; the player pauses in the background and is released when
 * this leaves the screen.
 */
@Composable
fun PromptClipPlayer(clip: PromptClipUi, modifier: Modifier = Modifier, viewModel: ClipPlaybackViewModel = hiltViewModel()) {
    val playback = remember(clip.url, clip.kind) { viewModel.playback(clip) }
    val ui by playback.state.collectAsStateWithLifecycle()
    DisposableEffect(playback) {
        onDispose {
            viewModel.focus.left(clip.url)
            playback.release()
        }
    }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, playback) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_PAUSE) playback.pause() }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    val toggle = {
        if (ui.phase != ClipPlayerPhase.PLAYING && ui.phase != ClipPlayerPhase.LOADING) viewModel.focus.starting(clip.url, playback)
        playback.toggle()
    }
    when (clip.kind) {
        ClipKind.AUDIO -> VoiceClip(clip, ui, toggle, modifier)
        ClipKind.VIDEO -> VideoClip(clip, ui, (playback.currentEngine as? ExoClipEngine)?.player, toggle, playback::toggleMute, modifier)
    }
}

@Composable
private fun VoiceClip(clip: PromptClipUi, ui: ClipPlayerUi, onToggle: () -> Unit, modifier: Modifier) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.bgCardSolid)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        PlayButton(ui.phase, onToggle)
        Column(Modifier.weight(1f)) {
            Text(ClipCopy.VOICE_ANSWER, style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textPrimary)
            if (ui.phase == ClipPlayerPhase.FAILED) {
                Text(ClipCopy.UNPLAYABLE, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        val length = ClipRules.duration(clip.durationMs)
        if (length.isNotEmpty()) Text(length, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textMuted)
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun VideoClip(
    clip: PromptClipUi,
    ui: ClipPlayerUi,
    player: Player?,
    onToggle: () -> Unit,
    onToggleMute: () -> Unit,
    modifier: Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .aspectRatio(VIDEO_RATIO)
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgCanvas),
    ) {
        if (player != null) {
            // A TextureView: the rounded frame clips it, which a SurfaceView would punch through.
            ContentFrame(
                player = player,
                modifier = Modifier.fillMaxSize(),
                surfaceType = SURFACE_TYPE_TEXTURE_VIEW,
                contentScale = ContentScale.Fit,
                shutter = { Box(Modifier.fillMaxSize().background(UsTheme.extended.bgCanvas)) },
            )
        }
        Box(Modifier.align(Alignment.Center)) { PlayButton(ui.phase, onToggle) }
        Row(
            modifier = Modifier.align(Alignment.BottomStart).fillMaxWidth().padding(UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val label = if (ui.phase == ClipPlayerPhase.FAILED) ClipCopy.UNPLAYABLE else ClipCopy.summary(ClipKind.VIDEO, clip.durationMs)
            Text(
                label,
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .weight(1f, fill = false)
                    .background(UsTheme.extended.bgCanvas.copy(alpha = 0.7f), RoundedCornerShape(UsTheme.radii.full))
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
            )
            Box(Modifier.weight(1f))
            IconButton(
                onClick = onToggleMute,
                modifier = Modifier.background(UsTheme.extended.bgCanvas.copy(alpha = 0.7f), CircleShape),
            ) {
                Icon(
                    if (ui.muted) UsIcons.SoundOff else UsIcons.SoundOn,
                    contentDescription = if (ui.muted) ClipCopy.UNMUTE else ClipCopy.MUTE,
                    tint = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

@Composable
private fun PlayButton(phase: ClipPlayerPhase, onToggle: () -> Unit) {
    IconButton(
        onClick = onToggle,
        modifier = Modifier.size(PLAY_SIZE).background(UsTheme.extended.accentSolid, CircleShape),
    ) {
        when (phase) {
            ClipPlayerPhase.LOADING -> CircularProgressIndicator(
                color = UsTheme.extended.textPrimary,
                strokeWidth = 2.dp,
                modifier = Modifier.size(18.dp),
            )
            ClipPlayerPhase.PLAYING -> Icon(UsIcons.Pause, contentDescription = ClipCopy.PAUSE, tint = UsTheme.extended.textPrimary)
            else -> Icon(UsIcons.Play, contentDescription = ClipCopy.PLAY, tint = UsTheme.extended.textPrimary)
        }
    }
}

private val PLAY_SIZE = 44.dp
private const val VIDEO_RATIO = 0.8f
