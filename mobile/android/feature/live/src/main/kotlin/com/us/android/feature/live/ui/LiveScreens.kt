package com.us.android.feature.live.ui

import android.Manifest
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.hostMessageActions
import com.us.android.feature.live.data.liveStatusOf
import com.us.android.feature.live.data.showsLiveBadge
import com.us.android.feature.live.data.showsViewerCount
import com.us.android.feature.live.data.viewerMessageActions
import io.livekit.android.room.Room
import io.livekit.android.room.track.VideoTrack
import livekit.org.webrtc.SurfaceViewRenderer

/**
 * LIVE — the hub, the broadcaster surface, and the viewer surface.
 *
 * All three deliberately run DARK in both themes: live video is a media
 * surface, same rule as reels and calls. Each screen sets the dark theme
 * itself so every token (and every house state view) reads on the black.
 */
@Composable
fun LiveHubScreen(
    onClose: () -> Unit,
    onGoLive: () -> Unit,
    onWatch: (streamId: String) -> Unit,
    viewModel: LiveHubViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsTheme(darkTheme = true) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .testTag("live-hub"),
        ) {
            LiveTopBar(title = "Live", onClose = onClose) {
                UsButton(
                    text = "Go live",
                    onClick = onGoLive,
                    modifier = Modifier.testTag("live-go-live"),
                )
            }

            when {
                state.loading -> UsLoadingState()
                state.error != null -> UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::refresh)
                state.streams.isEmpty() -> UsEmptyState(
                    title = "Nobody is live right now",
                    detail = "Go live and be the first.",
                )

                else -> LazyColumn(
                    contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    items(state.streams, key = { it.id }) { stream ->
                        LiveNowRow(stream = stream, onClick = { onWatch(stream.id) })
                    }
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
            .padding(UsTheme.spacing.xl)
            .testTag("live-now-row"),
    ) {
        val status = liveStatusOf(stream.status)
        LiveStatusPill(status = status)
        Text(
            stream.title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        if (showsViewerCount(status)) {
            Text(
                viewerCountLabel(stream.viewerCount),
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textMuted,
            )
        }
    }
}

/** The shared top bar: close, a title, and whatever the screen puts on the right. */
@Composable
private fun LiveTopBar(
    title: String,
    onClose: () -> Unit,
    trailing: @Composable () -> Unit = {},
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
    ) {
        IconButton(onClick = onClose, modifier = Modifier.testTag("live-close")) {
            Icon(UsIcons.Close, contentDescription = "Close", tint = UsTheme.extended.onMedia)
        }
        Text(
            title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.onMedia,
            maxLines = 1,
            modifier = Modifier.weight(1f),
        )
        trailing()
    }
}

/**
 * The status badge. Red only for the server's `live`; amber while
 * reconnecting; quiet for everything else; nothing for an unknown status.
 */
@Composable
fun LiveStatusPill(status: LiveStatus, modifier: Modifier = Modifier) {
    val label = statusPillLabel(status) ?: return
    val fill = when {
        showsLiveBadge(status) -> UsTheme.extended.liveRed
        status == LiveStatus.Reconnecting -> UsTheme.extended.statusWarning
        else -> UsTheme.extended.bgRaised
    }
    Text(
        label,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.ExtraBold,
        color = UsTheme.extended.onMedia,
        modifier = modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(fill)
            .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.xs)
            .testTag("live-status-pill"),
    )
}

/** "12 watching", from the server's count (the host is not in it). */
@Composable
fun ViewerCountLabel(count: Int, modifier: Modifier = Modifier) {
    Text(
        viewerCountLabel(count),
        style = MaterialTheme.typography.labelMedium,
        color = UsTheme.extended.onMediaMuted,
        modifier = modifier.testTag("live-viewer-count"),
    )
}

/** Title and one line over the media surface for every state that is not "on air with a picture". */
@Composable
fun StatusBanner(copy: StatusCopy, modifier: Modifier = Modifier, busy: Boolean = false) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal)
            .testTag("live-status-banner"),
    ) {
        if (busy) {
            CircularProgressIndicator(color = UsTheme.extended.onMedia)
            Spacer(Modifier.height(UsTheme.spacing.l))
        }
        Text(
            copy.title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.onMedia,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        Text(
            copy.detail,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMediaMuted,
            textAlign = TextAlign.Center,
        )
    }
}

// ── Broadcasting ────────────────────────────────────────────────────────

@Composable
fun GoLiveScreen(
    onClose: () -> Unit,
    viewModel: GoLiveViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var permissionsGranted by remember { mutableStateOf(false) }
    var confirmEnd by remember { mutableStateOf(false) }
    var toolsOpen by remember { mutableStateOf(false) }
    var selected by remember { mutableStateOf<LiveChatMessageDto?>(null) }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants -> permissionsGranted = grants.values.all { it } }

    // On air, leaving asks first: a stray back press must not end a broadcast.
    val requestClose: () -> Unit = {
        if (state.isOnAir) {
            confirmEnd = true
        } else {
            onClose()
        }
    }
    BackHandler(enabled = state.isOnAir) { confirmEnd = true }

    UsTheme(darkTheme = true) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .testTag("go-live"),
        ) {
            Column(modifier = Modifier.fillMaxSize()) {
                LiveTopBar(title = "Go live", onClose = requestClose) {
                    state.status?.let { status ->
                        if (showsViewerCount(status)) ViewerCountLabel(state.viewerCount)
                        LiveStatusPill(status)
                    }
                    if (state.isOnAir) {
                        IconButton(onClick = { toolsOpen = true }, modifier = Modifier.testTag("live-host-tools")) {
                            Icon(UsIcons.Sliders, contentDescription = "Moderation", tint = UsTheme.extended.onMedia)
                        }
                    }
                }
                GoLivePhase(
                    state = state,
                    permissionsGranted = permissionsGranted,
                    viewModel = viewModel,
                    onRequestPermissions = {
                        permissionLauncher.launch(arrayOf(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO))
                    },
                    onSelectMessage = { selected = it },
                    onEnd = { confirmEnd = true },
                )
            }
            UsMessageHost(
                message = state.notice?.let { UsMessage(it, UsMessageType.Info) },
                onDismiss = viewModel::onNoticeShown,
            )
        }

        if (confirmEnd) {
            EndStreamDialog(
                onConfirm = {
                    confirmEnd = false
                    viewModel.onEndStream()
                },
                onDismiss = { confirmEnd = false },
            )
        }
        HostSheets(
            state = state,
            viewModel = viewModel,
            selected = selected,
            onClearSelected = { selected = null },
            toolsOpen = toolsOpen,
            onCloseTools = { toolsOpen = false },
        )
    }
}

/** The host's per-message menu and the moderators-and-bans sheet. */
@Suppress("LongParameterList")
@Composable
private fun HostSheets(
    state: GoLiveViewModel.UiState,
    viewModel: GoLiveViewModel,
    selected: LiveChatMessageDto?,
    onClearSelected: () -> Unit,
    toolsOpen: Boolean,
    onCloseTools: () -> Unit,
) {
    selected?.let { message ->
        HostMessageSheet(
            message = message,
            actions = hostMessageActions(message, state.hostId, state.moderators, state.banned),
            onAction = { action ->
                onClearSelected()
                viewModel.onHostAction(message, action)
            },
            onDismiss = onClearSelected,
        )
    }
    if (toolsOpen) {
        HostToolsSheet(
            moderators = state.moderators,
            banned = state.banned,
            onRemoveModerator = viewModel::onRemoveModerator,
            onUnban = viewModel::onUnban,
            onDismiss = onCloseTools,
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun GoLivePhase(
    state: GoLiveViewModel.UiState,
    permissionsGranted: Boolean,
    viewModel: GoLiveViewModel,
    onRequestPermissions: () -> Unit,
    onSelectMessage: (LiveChatMessageDto) -> Unit,
    onEnd: () -> Unit,
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

        GoLiveViewModel.Phase.Preparing -> StatusBanner(
            copy = StatusCopy("Starting…", "Setting up your stream."),
            busy = true,
        )

        is GoLiveViewModel.Phase.OnAir -> Column(modifier = Modifier.fillMaxSize()) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f),
            ) {
                key(state.videoVersion) {
                    VideoSurface(
                        room = viewModel.room,
                        track = viewModel.localVideoTrack(),
                        modifier = Modifier.fillMaxSize(),
                    )
                }
                hostStatusCopy(phase.status, EndedReason.Unknown)?.let { copy ->
                    StatusBanner(
                        copy = copy,
                        busy = true,
                        modifier = Modifier.background(UsTheme.extended.glassBg),
                    )
                }
            }
            ChatList(
                messages = state.chat.messages,
                onMessageClick = onSelectMessage,
                onMessageLongClick = onSelectMessage,
                hint = "Tap a message to moderate it.",
            )
            UsSecondaryButton(
                text = "End stream",
                onClick = onEnd,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(UsTheme.spacing.xl)
                    .testTag("end-live"),
            )
        }

        is GoLiveViewModel.Phase.Over -> {
            val copy = hostStatusCopy(phase.status, phase.reason) ?: return
            StatusBanner(
                copy = if (phase.unconfirmed) {
                    copy.copy(detail = "We couldn't confirm the end with the server. Viewers will see it end shortly.")
                } else {
                    copy
                },
            )
        }

        is GoLiveViewModel.Phase.Refused -> RefusalView(
            refusal = phase.refusal,
            onTryAgain = viewModel::onTryAgain,
        )
    }
}

@Composable
private fun RefusalView(refusal: GoLiveRefusal, onTryAgain: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal)
            .testTag("live-refusal"),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            refusal.message,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.onMedia,
            textAlign = TextAlign.Center,
        )
        if (refusal.canRetry) {
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            UsSecondaryButton(
                text = "Try again",
                onClick = onTryAgain,
                modifier = Modifier.testTag("live-refusal-retry"),
            )
        }
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
        UsTextField(
            value = title,
            onValueChange = onTitleChanged,
            label = "What's your stream about?",
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-title"),
        )
        Spacer(Modifier.height(UsTheme.spacing.l))
        if (!permissionsGranted) {
            UsSecondaryButton(
                text = "Allow camera and microphone",
                onClick = onRequestPermissions,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("live-permissions"),
            )
            Spacer(Modifier.height(UsTheme.spacing.m))
        }
        UsButton(
            text = "Go live",
            onClick = onGoLive,
            enabled = canGoLive,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-start"),
        )
    }
}

// ── Watching ────────────────────────────────────────────────────────────

@Composable
fun LiveWatchScreen(
    onClose: () -> Unit,
    viewModel: LiveWatchViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsTheme(darkTheme = true) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .imePadding()
                .testTag("live-watch"),
        ) {
            Column(modifier = Modifier.fillMaxSize()) {
                LiveTopBar(title = state.title, onClose = onClose) {
                    if (showsViewerCount(state.status)) ViewerCountLabel(state.viewerCount)
                    LiveStatusPill(state.status)
                    IconButton(onClick = viewModel::onReportStream, modifier = Modifier.testTag("live-report-stream")) {
                        Icon(UsIcons.Flag, contentDescription = "Report stream", tint = UsTheme.extended.onMedia)
                    }
                }
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .weight(1f),
                ) {
                    WatchStage(state = state, viewModel = viewModel)
                }
                if (!state.loading && state.joinError == null) {
                    ChatList(
                        messages = state.chat.messages,
                        onMessageClick = null,
                        onMessageLongClick = viewModel::onMessageLongPress,
                        hint = if (state.canModerate) {
                            "Press and hold a message to moderate it."
                        } else {
                            "Press and hold a message to report it."
                        },
                    )
                }
                if (state.canChat) {
                    ChatComposer(
                        draft = state.draft,
                        onDraftChanged = viewModel::onDraftChanged,
                        onSend = viewModel::onSendChat,
                    )
                }
            }
            UsMessageHost(
                message = state.notice?.let { UsMessage(it, UsMessageType.Error) },
                onDismiss = viewModel::onNoticeShown,
            )
        }

        state.selected?.let { message ->
            ViewerMessageSheet(
                message = message,
                actions = viewerMessageActions(message, state.hostId, state.canModerate),
                onAction = { action -> viewModel.onViewerAction(message, action) },
                onDismiss = viewModel::onDismissMessage,
            )
        }
        state.reportTarget?.let { target ->
            LiveReportSheet(
                aboutMessage = target is LiveWatchViewModel.ReportTarget.Message,
                report = state.report,
                onSubmit = viewModel::onSubmitReport,
                onDismiss = viewModel::onDismissReport,
            )
        }
    }
}

/** The picture, or the truthful sentence about why there is none. */
@Composable
private fun BoxScope.WatchStage(state: LiveWatchViewModel.UiState, viewModel: LiveWatchViewModel) {
    when {
        state.joinError != null -> UsErrorState(message = state.joinError, onRetry = viewModel::onRetry)
        state.loading -> UsLoadingState(label = "Joining")
        else -> {
            if (state.hasVideo && !state.status.isOver) {
                key(state.videoVersion) {
                    VideoSurface(
                        room = viewModel.room,
                        track = viewModel.remoteVideo,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
            val copy = viewerStatusCopy(state.status, state.endedReason)
            when {
                copy != null -> StatusBanner(
                    copy = copy,
                    busy = !state.status.isOver,
                    modifier = if (state.hasVideo) Modifier.background(UsTheme.extended.glassBg) else Modifier,
                )
                !state.hasVideo -> UsLoadingState(
                    label = "Waiting for video",
                    modifier = Modifier.align(Alignment.Center),
                )
            }
        }
    }
}

/**
 * The chat, newest at the bottom. `GET …/chat` is newest first, which is
 * exactly what a reversed layout wants at index 0 — no re-reversing.
 */
@Composable
private fun ChatList(
    messages: List<LiveChatMessageDto>,
    onMessageClick: ((LiveChatMessageDto) -> Unit)?,
    onMessageLongClick: (LiveChatMessageDto) -> Unit,
    hint: String,
) {
    if (messages.isEmpty()) {
        Text(
            "No messages yet.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.onMediaMuted,
            modifier = Modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
        )
        return
    }
    LazyColumn(
        modifier = Modifier
            .fillMaxWidth()
            .height(CHAT_HEIGHT)
            .testTag("live-chat"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        reverseLayout = true,
    ) {
        items(messages, key = { it.id }) { message ->
            ChatRow(
                message = message,
                onClick = onMessageClick?.let { { it(message) } },
                onLongClick = { onMessageLongClick(message) },
            )
        }
        item(key = "hint") {
            Text(hint, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.onMediaMuted)
        }
    }
}

@Composable
private fun ChatRow(message: LiveChatMessageDto, onClick: (() -> Unit)?, onLongClick: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick ?: {}, onLongClick = onLongClick)
            .testTag("live-chat-row"),
    ) {
        Text(
            shortUserLabel(message.userId),
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.onMediaMuted,
        )
        Text(message.text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.onMedia)
    }
}

@Composable
private fun ChatComposer(draft: String, onDraftChanged: (String) -> Unit, onSend: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
    ) {
        OutlinedTextField(
            value = draft,
            onValueChange = onDraftChanged,
            placeholder = { Text("Say something…", style = MaterialTheme.typography.bodyMedium) },
            textStyle = MaterialTheme.typography.bodyMedium,
            singleLine = true,
            modifier = Modifier
                .weight(1f)
                .testTag("live-chat-input"),
        )
        IconButton(
            onClick = onSend,
            modifier = Modifier.testTag("live-chat-send"),
        ) {
            Icon(UsIcons.Send, contentDescription = "Send", tint = UsTheme.extended.onMedia)
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
        Box(modifier = modifier.background(MaterialTheme.colorScheme.scrim))
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

// ── Previews ────────────────────────────────────────────────────────────

@Preview
@Composable
private fun LiveStatusPillPreview() {
    UsTheme(darkTheme = true) {
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            LiveStatus.entries.forEach { LiveStatusPill(it) }
        }
    }
}

@Preview
@Composable
private fun LiveTopBarPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            LiveTopBar(title = "Weekend build", onClose = {}) {
                ViewerCountLabel(count = 12)
                LiveStatusPill(LiveStatus.Live)
            }
        }
    }
}

@Preview
@Composable
private fun ViewerCountLabelPreview() {
    UsTheme(darkTheme = true) { ViewerCountLabel(count = 1_240) }
}

@Preview
@Composable
private fun StatusBannerPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            viewerStatusCopy(LiveStatus.Reconnecting, EndedReason.Unknown)?.let { StatusBanner(it, busy = true) }
        }
    }
}

@Preview
@Composable
private fun RefusalViewPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            RefusalView(refusal = GoLiveRefusal(LIVE_PILOT_COPY, canRetry = false), onTryAgain = {})
        }
    }
}

@Preview
@Composable
private fun ChatListPreview() {
    UsTheme(darkTheme = true) {
        Column(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            ChatList(
                messages = listOf(
                    LiveChatMessageDto(id = "2", userId = "aa11bb22", text = "newest"),
                    LiveChatMessageDto(id = "1", userId = "cc33dd44", text = "older"),
                ),
                onMessageClick = null,
                onMessageLongClick = {},
                hint = "Press and hold a message to report it.",
            )
            ChatComposer(draft = "", onDraftChanged = {}, onSend = {})
        }
    }
}
