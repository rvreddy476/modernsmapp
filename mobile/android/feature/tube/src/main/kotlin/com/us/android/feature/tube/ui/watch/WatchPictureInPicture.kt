package com.us.android.feature.tube.ui.watch

import android.app.PendingIntent
import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.drawable.Icon
import android.os.Build
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.activity.compose.LocalActivity
import androidx.annotation.RequiresApi
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.core.app.PictureInPictureModeChangedInfo
import androidx.core.content.ContextCompat
import androidx.core.util.Consumer
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.platform.testTag
import androidx.annotation.OptIn
import androidx.compose.foundation.layout.Box
import androidx.compose.ui.Modifier
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Whether leaving the app now should shrink the video into a
 * picture-in-picture window (web parity, 2026-10-24: the web keeps a long
 * video playing in its mini-player; on a phone the system's window is that
 * place). Only a video that is PLAYING — not paused, not still loading, not
 * locked to members, not ended — and only on a device that offers the window.
 * Pure, so the rule is a table test.
 */
fun pictureInPictureEligible(content: WatchContent, playing: Boolean, ended: Boolean, supported: Boolean): Boolean =
    supported &&
        playing &&
        !ended &&
        content is WatchContent.Ready &&
        !content.locked &&
        content.playback != null

/** The window's one control: Pause while it plays, Play while it is held. */
data class PipToggle(val title: String, val playIcon: Boolean)

fun pipToggle(playing: Boolean): PipToggle = if (playing) PipToggle("Pause", playIcon = false) else PipToggle("Play", playIcon = true)

/** The broadcast the window's control sends; the receiver is the app's own and not exported. */
internal const val ACTION_PIP_PLAY_PAUSE = "com.us.android.feature.tube.PIP_PLAY_PAUSE"

/**
 * Keeps the Activity's picture-in-picture parameters in step with the watch
 * screen and answers the window's play/pause, while the screen is shown.
 *
 *  - Android 12 and later enter on their own: `autoEnterEnabled` is set while
 *    [eligible] and cleared the moment it is not (and when the screen goes),
 *    so no other screen of the app ever shrinks into a window.
 *  - Before 12 the Activity's user-leave hint (Home, Recents) enters by hand.
 *  - The window carries one action, play/pause, through a broadcast only
 *    this app can receive.
 *
 * Answers whether the Activity is in the window now: the screen then draws
 * the bare video, with no controls and no details.
 */
@Composable
fun rememberWatchPictureInPicture(eligible: Boolean, playing: Boolean, onTogglePlay: () -> Unit): Boolean {
    val activity = LocalActivity.current as? ComponentActivity ?: return false
    val supported = remember(activity) { activity.supportsPictureInPicture() }
    var inWindow by remember { mutableStateOf(false) }
    val toggle by rememberUpdatedState(onTogglePlay)
    val canEnter by rememberUpdatedState(supported && eligible)

    DisposableEffect(activity) {
        val modeListener = Consumer<PictureInPictureModeChangedInfo> { inWindow = it.isInPictureInPictureMode }
        val leaveListener = Runnable {
            if (canEnter && Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
                runCatching { activity.enterPictureInPictureMode(activity.pipParams(playing = true, autoEnter = false)) }
            }
        }
        val receiver = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) {
                if (intent.action == ACTION_PIP_PLAY_PAUSE) toggle()
            }
        }
        activity.addOnPictureInPictureModeChangedListener(modeListener)
        activity.addOnUserLeaveHintListener(leaveListener)
        ContextCompat.registerReceiver(activity, receiver, IntentFilter(ACTION_PIP_PLAY_PAUSE), ContextCompat.RECEIVER_NOT_EXPORTED)
        onDispose {
            activity.removeOnPictureInPictureModeChangedListener(modeListener)
            activity.removeOnUserLeaveHintListener(leaveListener)
            runCatching { activity.unregisterReceiver(receiver) }
            // Leaving the watch screen: nothing else in the app enters the window on its own.
            if (supported && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                runCatching { activity.setPictureInPictureParams(activity.pipParams(playing = false, autoEnter = false)) }
            }
        }
    }
    DisposableEffect(activity, supported, eligible, playing) {
        if (supported) {
            runCatching { activity.setPictureInPictureParams(activity.pipParams(playing, autoEnter = eligible)) }
        }
        onDispose {}
    }
    return inWindow
}

private fun Context.supportsPictureInPicture(): Boolean =
    packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)

/** A 16:9 window with the play/pause action; auto-enter on Android 12 and later. */
private fun Context.pipParams(playing: Boolean, autoEnter: Boolean): PictureInPictureParams {
    val toggle = pipToggle(playing)
    val intent = PendingIntent.getBroadcast(
        this,
        PIP_REQUEST_CODE,
        Intent(ACTION_PIP_PLAY_PAUSE).setPackage(packageName),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )
    val icon = Icon.createWithResource(
        this,
        if (toggle.playIcon) android.R.drawable.ic_media_play else android.R.drawable.ic_media_pause,
    )
    val builder = PictureInPictureParams.Builder()
        .setAspectRatio(Rational(PIP_WIDTH, PIP_HEIGHT))
        .setActions(listOf(RemoteAction(icon, toggle.title, toggle.title, intent)))
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) builder.autoEnter(autoEnter)
    return builder.build()
}

@RequiresApi(Build.VERSION_CODES.S)
private fun PictureInPictureParams.Builder.autoEnter(enabled: Boolean): PictureInPictureParams.Builder =
    setAutoEnterEnabled(enabled).setSeamlessResizeEnabled(true)

private const val PIP_REQUEST_CODE = 4_271
private const val PIP_WIDTH = 16
private const val PIP_HEIGHT = 9

/** The video alone, for the window: no controls, no details, no overlays a finger cannot reach. */
@OptIn(UnstableApi::class)
@Composable
internal fun PictureInPicturePlayer(player: Player, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(UsTheme.extended.stage)
            .testTag("watch_pip"),
    ) {
        PlayerSurface(player = player, surfaceType = SURFACE_TYPE_SURFACE_VIEW, modifier = Modifier.fillMaxSize())
    }
}
