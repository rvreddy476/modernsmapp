package com.us.android.feature.live.ui

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
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
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.feature.live.data.LiveStreamDto
import io.livekit.android.room.Room
import io.livekit.android.room.track.VideoTrack
import livekit.org.webrtc.SurfaceViewRenderer

/**
 * LIVE — the hub, the broadcaster surface, and the viewer surface.
 *
 * All three deliberately run DARK in both themes: live video is a media
 * surface, same rule as reels and calls.
 */
@Composable
fun LiveHubScreen(
    onClose: () -> Unit,
    onGoLive: () -> Unit,
    onWatch: (streamId: String) -> Unit,
    viewModel: LiveHubViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .testTag("live-hub"),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m),
        ) {
            IconButton(onClick = onClose) {
                Icon(UsIcons.Close, contentDescription = "Close", tint = Color.White)
            }
            Text(
                "Live",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier.weight(1f),
            )
            Button(
                onClick = onGoLive,
                modifier = Modifier.testTag("live-go-live"),
            ) { Text("Go live") }
        }

        when {
            state.loading -> Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center,
            ) { CircularProgressIndicator() }

            state.streams.isEmpty() -> UsEmptyState(
                title = "Nobody is live right now",
                detail = "Go live and be the first.",
            )

            else -> LazyColumn(
                contentPadding = androidx.compose.foundation.layout.PaddingValues(
                    horizontal = UsTheme.spacing.xl,
                    vertical = UsTheme.spacing.m,
                ),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                items(state.streams, key = { it.id }) { stream ->
                    LiveNowRow(stream = stream, onClick = { onWatch(stream.id) })
                }
            }
        }
    }
}

@Composable
private fun LiveNowRow(stream: LiveStreamDto, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgCardSolid)
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.xl),
    ) {
        LivePill()
        Text(
            stream.title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = Color.White,
            modifier = Modifier.weight(1f),
        )
    }
}

@Composable
private fun LivePill() {
    Text(
        "LIVE",
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.ExtraBold,
        color = Color.White,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(UsTheme.extended.liveRed)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs),
    )
}

// ── Broadcasting ────────────────────────────────────────────────────────

@Composable
fun GoLiveScreen(
    onClose: () -> Unit,
    viewModel: GoLiveViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var permissionsGranted by remember { mutableStateOf(false) }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants -> permissionsGranted = grants.values.all { it } }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .testTag("go-live"),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m),
        ) {
            IconButton(onClick = {
                if (state.phase == GoLiveViewModel.Phase.Live) viewModel.onEndStream()
                onClose()
            }) {
                Icon(UsIcons.Close, contentDescription = "Close", tint = Color.White)
            }
            Text(
                "Go live",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier.weight(1f),
            )
            if (state.phase == GoLiveViewModel.Phase.Live) LivePill()
        }

        GoLivePhase(
            state = state,
            permissionsGranted = permissionsGranted,
            viewModel = viewModel,
            onRequestPermissions = {
                permissionLauncher.launch(
                    arrayOf(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO),
                )
            },
        )
    }
}

@Composable
private fun GoLivePhase(
    state: GoLiveViewModel.UiState,
    permissionsGranted: Boolean,
    viewModel: GoLiveViewModel,
    onRequestPermissions: () -> Unit,
) {
    when (val phase = state.phase) {
        GoLiveViewModel.Phase.Setup -> GoLiveSetup(
            title = state.title,
            canGoLive = state.canGoLive && permissionsGranted,
            permissionsGranted = permissionsGranted,
            onTitleChanged = viewModel::onTitleChanged,
            onRequestPermissions = onRequestPermissions,
            onGoLive = viewModel::onGoLive,
        )

        GoLiveViewModel.Phase.Connecting -> Box(
            modifier = Modifier.fillMaxSize(),
            contentAlignment = Alignment.Center,
        ) { CircularProgressIndicator() }

        GoLiveViewModel.Phase.Live -> Column(modifier = Modifier.fillMaxSize()) {
            VideoSurface(
                room = viewModel.room,
                track = viewModel.localVideoTrack(),
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            )
            Button(
                onClick = viewModel::onEndStream,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(UsTheme.spacing.xl)
                    .testTag("end-live"),
            ) { Text("End stream") }
        }

        is GoLiveViewModel.Phase.Failure -> Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(UsTheme.spacing.xl),
            verticalArrangement = Arrangement.Center,
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                phase.message,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
            )
        }

        GoLiveViewModel.Phase.Ended -> UsEmptyState(
            title = "Stream ended",
            detail = "Your broadcast has finished.",
        )
    }
}

@Composable
private fun GoLiveSetup(
    title: String,
    canGoLive: Boolean,
    permissionsGranted: Boolean,
    onTitleChanged: (String) -> Unit,
    onRequestPermissions: () -> Unit,
    onGoLive: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xl),
        verticalArrangement = Arrangement.Center,
    ) {
        OutlinedTextField(
            value = title,
            onValueChange = onTitleChanged,
            label = { Text("What's your stream about?") },
            singleLine = true,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-title"),
        )
        Spacer(Modifier.height(UsTheme.spacing.l))
        if (!permissionsGranted) {
            Button(
                onClick = onRequestPermissions,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("live-permissions"),
            ) { Text("Allow camera and microphone") }
            Spacer(Modifier.height(UsTheme.spacing.m))
        }
        Button(
            onClick = onGoLive,
            enabled = canGoLive,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-start"),
        ) { Text("Go live") }
    }
}

// ── Watching ────────────────────────────────────────────────────────────

@Composable
fun LiveWatchScreen(
    onClose: () -> Unit,
    viewModel: LiveWatchViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black)
            .imePadding()
            .testTag("live-watch"),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
        ) {
            IconButton(onClick = onClose) {
                Icon(UsIcons.Close, contentDescription = "Close", tint = Color.White)
            }
            Text(
                state.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                modifier = Modifier.weight(1f),
            )
            LivePill()
        }

        Box(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f),
        ) {
            when {
                state.error != null -> UsEmptyState(
                    title = "Couldn't join",
                    detail = state.error.orEmpty(),
                )
                state.ended -> UsEmptyState(
                    title = "Stream ended",
                    detail = "This broadcast has finished.",
                )
                state.connecting -> Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center,
                ) { CircularProgressIndicator() }
                else -> key(state.videoVersion) {
                    VideoSurface(
                        room = viewModel.room,
                        track = viewModel.remoteVideo,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        }

        WatchChatPanel(
            state = state,
            onDraftChanged = viewModel::onDraftChanged,
            onSend = viewModel::onSendChat,
        )
    }
}

@Composable
private fun WatchChatPanel(
    state: LiveWatchViewModel.UiState,
    onDraftChanged: (String) -> Unit,
    onSend: () -> Unit,
) {
    LazyColumn(
        modifier = Modifier
            .fillMaxWidth()
            .height(CHAT_HEIGHT),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(
            horizontal = UsTheme.spacing.xl,
            vertical = UsTheme.spacing.s,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        reverseLayout = true,
    ) {
        items(state.chat.asReversed(), key = { it.id }) { message ->
            Text(
                message.text,
                style = MaterialTheme.typography.bodySmall,
                color = Color.White,
            )
        }
    }

    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.xl,
                vertical = UsTheme.spacing.m,
            ),
    ) {
        OutlinedTextField(
            value = state.draft,
            onValueChange = onDraftChanged,
            placeholder = { Text("Say something…") },
            singleLine = true,
            modifier = Modifier
                .weight(1f)
                .testTag("live-chat-input"),
        )
        IconButton(
            onClick = onSend,
            modifier = Modifier.testTag("live-chat-send"),
        ) {
            Icon(UsIcons.Share, contentDescription = "Send", tint = Color.White)
        }
    }
}

// ── Rendering ───────────────────────────────────────────────────────────

/**
 * A LiveKit video track on an org.webrtc [SurfaceViewRenderer].
 *
 * The renderer must be initialised THROUGH the room (it owns the EGL
 * context) and released on the way out, or the surface leaks a GL thread
 * per visit.
 */
@Composable
private fun VideoSurface(room: Room?, track: VideoTrack?, modifier: Modifier = Modifier) {
    if (room == null || track == null) {
        Box(modifier = modifier.background(Color.Black))
        return
    }
    AndroidView(
        factory = { context ->
            SurfaceViewRenderer(context).also {
                room.initVideoRenderer(it)
                track.addRenderer(it)
            }
        },
        onRelease = { view ->
            track.removeRenderer(view)
            view.release()
        },
        modifier = modifier,
    )
}

private val CHAT_HEIGHT = 160.dp
