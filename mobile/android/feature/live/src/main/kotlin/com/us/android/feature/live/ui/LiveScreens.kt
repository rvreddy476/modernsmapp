package com.us.android.feature.live.ui

import android.Manifest
import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.LightSystemBarGlyphs
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.live.data.BADGE_FOUNDING_CREATOR
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveGateAction
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.hostMessageActions
import com.us.android.feature.live.data.liveStatusOf
import com.us.android.feature.live.data.overlayComments
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
    /** The creator's own LIVE centre: upcoming, past and recordings. */
    onCreatorCentre: () -> Unit,
    viewModel: LiveHubViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsTheme(darkTheme = true) {
        // 2026-10-02: the room is dark on a light device too, so both system bars keep light glyphs.
        LightSystemBarGlyphs()
        Column(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .testTag("live-hub"),
        ) {
            LiveTopBar(title = "Live", onClose = onClose) {
                IconButton(onClick = onCreatorCentre, modifier = Modifier.testTag("live-creator-centre")) {
                    Icon(UsIcons.Radio, contentDescription = "Your live streams", tint = UsTheme.extended.onMedia)
                }
                // A pill, sized to its label: the full-width primary button
                // stretched across most of the bar (2026-10-17).
                UsPillButton(
                    text = "Go live",
                    onClick = onGoLive,
                    modifier = Modifier.testTag("live-go-live"),
                )
            }
            HubFilters(state = state, onFeed = viewModel::onFeed, onCategory = viewModel::onCategory)

            when {
                state.loading -> UsLoadingState()
                state.error != null -> UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::refresh)
                state.streams.isEmpty() && state.upcoming.isEmpty() -> UsEmptyState(
                    title = if (state.feed == LiveHubViewModel.Feed.Following) {
                        "Nobody you follow is live"
                    } else {
                        "Nobody is live right now"
                    },
                    detail = "Go live and be the first.",
                )

                else -> LazyColumn(
                    contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    if (state.streams.isEmpty()) {
                        item(key = "none-live") { HubNote("Nobody is live here right now.") }
                    }
                    items(state.streams, key = { it.id }) { stream ->
                        LiveNowRow(stream = stream, onClick = { onWatch(stream.id) })
                    }
                    if (state.upcoming.isNotEmpty()) {
                        item(key = "upcoming-heading") { HubHeading("Upcoming") }
                        items(state.upcoming, key = { "upcoming-${it.id}" }) { stream -> UpcomingRow(stream) }
                    }
                }
            }
        }
    }
}

/** For you / Following, then the categories on air. Selected is filled; the rest are outlined. */
@Composable
private fun HubFilters(
    state: LiveHubViewModel.UiState,
    onFeed: (LiveHubViewModel.Feed) -> Unit,
    onCategory: (String?) -> Unit,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xs)
            .testTag("live-feeds"),
    ) {
        LiveHubViewModel.Feed.entries.forEach { feed ->
            UsPillButton(
                text = feed.label,
                onClick = { onFeed(feed) },
                filled = state.feed == feed,
                modifier = Modifier.testTag("live-feed-${feed.name}"),
            )
        }
    }
    if (state.categories.isNotEmpty()) {
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xs),
            modifier = Modifier
                .fillMaxWidth()
                .testTag("live-categories"),
        ) {
            item(key = "all") {
                UsPillButton(text = "All", onClick = { onCategory(null) }, filled = state.category == null)
            }
            items(state.categories, key = { it.slug }) { category ->
                UsPillButton(
                    text = categoryLabel(category),
                    onClick = { onCategory(category.slug) },
                    filled = state.category == category.slug,
                    modifier = Modifier.testTag("live-category"),
                )
            }
        }
    }
}

@Composable
private fun HubHeading(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.titleSmall,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.onMedia,
        modifier = Modifier.padding(top = UsTheme.spacing.s),
    )
}

@Composable
private fun HubNote(text: String) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.onMediaMuted)
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
        StreamText(
            title = stream.title,
            detail = creatorName(stream.creator),
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

/** An upcoming stream: read-only here (reminders are a web control for now). */
@Composable
private fun UpcomingRow(stream: LiveStreamDto) {
    StreamCard(
        title = stream.title,
        detail = listOfNotNull(creatorName(stream.creator), startsAtLabel(stream.scheduledAt)).joinToString(" · "),
        tag = "live-upcoming-row",
    )
}

/** A stream's title and one quiet line under it. */
@Composable
private fun StreamText(title: String, detail: String, modifier: Modifier = Modifier) {
    Column(modifier = modifier) {
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        if (detail.isNotBlank()) {
            Text(
                detail,
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A list card that is not a door anywhere: title and a detail line. */
@Composable
private fun StreamCard(title: String, detail: String, tag: String) {
    StreamText(
        title = title,
        detail = detail,
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(UsTheme.extended.bgCardSolid)
            .padding(UsTheme.spacing.xl)
            .testTag(tag),
    )
}

// ── Your LIVE (creator centre, read-only) ──────────────────────────────

@Composable
fun LiveCreatorCentreScreen(
    onClose: () -> Unit,
    viewModel: LiveCreatorCentreViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsTheme(darkTheme = true) {
        LightSystemBarGlyphs()
        Column(
            modifier = Modifier
                .fillMaxSize()
                .background(MaterialTheme.colorScheme.scrim)
                .testTag("live-creator-centre-screen"),
        ) {
            LiveTopBar(title = "Your live streams", onClose = onClose)
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xs),
            ) {
                LiveCreatorCentreViewModel.Tab.entries.forEach { tab ->
                    UsPillButton(
                        text = tab.label,
                        onClick = { viewModel.onTab(tab) },
                        filled = state.tab == tab,
                        modifier = Modifier.testTag("live-centre-tab-${tab.name}"),
                    )
                }
            }
            when {
                state.loading -> UsLoadingState()
                state.error != null -> UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::refresh)
                state.rows.isEmpty() -> UsEmptyState(title = centreEmptyTitle(state.tab))
                else -> LazyColumn(
                    contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    items(state.rows, key = { it.id }) { stream ->
                        StreamCard(
                            title = stream.title,
                            detail = centreDetail(state.tab, stream),
                            tag = "live-centre-row",
                        )
                    }
                }
            }
        }
    }
}

private fun centreEmptyTitle(tab: LiveCreatorCentreViewModel.Tab): String = when (tab) {
    LiveCreatorCentreViewModel.Tab.Upcoming -> "Nothing scheduled"
    LiveCreatorCentreViewModel.Tab.Past -> "No past streams yet"
    LiveCreatorCentreViewModel.Tab.Recordings -> "No recordings yet"
}

private fun centreDetail(tab: LiveCreatorCentreViewModel.Tab, stream: LiveStreamDto): String = when (tab) {
    LiveCreatorCentreViewModel.Tab.Upcoming -> startsAtLabel(stream.scheduledAt).orEmpty()
    LiveCreatorCentreViewModel.Tab.Past -> pastStreamLabel(stream)
    LiveCreatorCentreViewModel.Tab.Recordings -> recordingLabel(stream)
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
            // The shell leaves the status bar to the LIVE screens (2026-10-17)
            // so their dark ground runs under it; the bar's row sits below it.
            .statusBarsPadding()
            // The trailing pill keeps a full gutter from the screen edge.
            .padding(start = UsTheme.spacing.m, end = UsTheme.spacing.xxl, top = UsTheme.spacing.s, bottom = UsTheme.spacing.s),
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
    /** The "not yet" screen's "Create a post": `:app` opens the create flow. */
    onCreatePost: () -> Unit,
    /** The "not yet" screen's "Verify email": `:app` opens the account screen. */
    onVerifyEmail: () -> Unit,
    /** The "not yet" screen's "Verify phone number": `:app` opens the account screen. */
    onVerifyPhone: () -> Unit,
    viewModel: GoLiveViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var permissionsGranted by remember { mutableStateOf(false) }
    val panels = remember { HostPanels() }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants -> permissionsGranted = grants.values.all { it } }

    // On air, leaving asks first: a stray back press must not end a broadcast.
    val requestClose: () -> Unit = {
        if (state.isOnAir) {
            panels.confirmEnd = true
        } else {
            onClose()
        }
    }
    BackHandler(enabled = state.isOnAir) { panels.confirmEnd = true }
    // Back from the create flow or the account screen: the answer may have changed.
    LifecycleResumeEffect(viewModel) {
        viewModel.onCheckAgain()
        onPauseOrDispose {}
    }
    val onGateAction: (LiveGateAction) -> Unit = { action ->
        when (action) {
            LiveGateAction.CreatePost -> onCreatePost()
            LiveGateAction.VerifyEmail -> onVerifyEmail()
            LiveGateAction.VerifyPhone -> onVerifyPhone()
            LiveGateAction.CheckAgain -> viewModel.onCheckAgain()
        }
    }

    UsTheme(darkTheme = true) {
        // 2026-10-02: the room is dark on a light device too, so both system bars keep light glyphs.
        LightSystemBarGlyphs()
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
                    if (state.isOnAir) HostBarActions(panels)
                }
                GoLivePhase(
                    state = state,
                    permissionsGranted = permissionsGranted,
                    viewModel = viewModel,
                    onRequestPermissions = {
                        permissionLauncher.launch(arrayOf(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO))
                    },
                    onOpenChat = { panels.chatOpen = true },
                    onGateAction = onGateAction,
                    onEnd = { panels.confirmEnd = true },
                )
            }
            UsMessageHost(
                message = state.notice?.let { UsMessage(it, UsMessageType.Info) },
                onDismiss = viewModel::onNoticeShown,
            )
        }

        HostSheets(state = state, viewModel = viewModel, panels = panels)
    }
}

/**
 * What is open over the host's screen: the End confirmation, the full chat,
 * the moderation tools, and the message whose menu is up. One holder rather
 * than four screen locals, so the bar and the sheets can open and close them
 * without the screen function growing a line per panel. Screen-local: a
 * sheet does not need to outlive the process.
 */
@Stable
private class HostPanels {
    var confirmEnd by mutableStateOf(false)
    var toolsOpen by mutableStateOf(false)
    var chatOpen by mutableStateOf(false)
    var selected by mutableStateOf<LiveChatMessageDto?>(null)
}

/** On air, the bar offers the full chat and the moderation tools. */
@Composable
private fun HostBarActions(panels: HostPanels) {
    IconButton(onClick = { panels.chatOpen = true }, modifier = Modifier.testTag("live-host-chat")) {
        Icon(UsIcons.Comment, contentDescription = "Chat", tint = UsTheme.extended.onMedia)
    }
    IconButton(onClick = { panels.toolsOpen = true }, modifier = Modifier.testTag("live-host-tools")) {
        Icon(UsIcons.Sliders, contentDescription = "Moderation", tint = UsTheme.extended.onMedia)
    }
}

/** The End confirmation, the full chat, the host's per-message menu and the moderators-and-bans sheet. */
@Composable
private fun HostSheets(state: GoLiveViewModel.UiState, viewModel: GoLiveViewModel, panels: HostPanels) {
    if (panels.confirmEnd) {
        EndStreamDialog(
            onConfirm = {
                panels.confirmEnd = false
                viewModel.onEndStream()
            },
            onDismiss = { panels.confirmEnd = false },
        )
    }
    if (panels.chatOpen && state.isOnAir) {
        LiveChatSheet(
            chat = ChatSheetState(
                messages = state.chat.messages,
                hostId = state.hostId,
                moderators = state.moderators,
                hint = "Tap a message to moderate it.",
                draft = state.draft,
                sending = state.sending,
                canChat = true,
            ),
            onMessageClick = { panels.selected = it },
            onMessageLongClick = { panels.selected = it },
            onDraftChanged = viewModel::onDraftChanged,
            onSend = viewModel::onSendChat,
            onDismiss = { panels.chatOpen = false },
        )
    }
    val onClearSelected = { panels.selected = null }
    panels.selected?.let { message ->
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
    if (panels.toolsOpen) {
        HostToolsSheet(
            moderators = state.moderators,
            banned = state.banned,
            people = state.people,
            onRemoveModerator = viewModel::onRemoveModerator,
            onUnban = viewModel::onUnban,
            onDismiss = { panels.toolsOpen = false },
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
    onOpenChat: () -> Unit,
    onGateAction: (LiveGateAction) -> Unit,
    onEnd: () -> Unit,
) {
    when (val phase = state.phase) {
        GoLiveViewModel.Phase.Checking -> UsLoadingState(label = "Checking")

        is GoLiveViewModel.Phase.NotYet -> LiveNotYetView(
            gate = phase.gate,
            busy = state.rechecking,
            onAction = onGateAction,
        )

        GoLiveViewModel.Phase.Setup -> GoLiveSetup(
            title = state.title,
            canGoLive = state.canGoLive && permissionsGranted,
            permissionsGranted = permissionsGranted,
            viewerCap = state.viewerCap,
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
                // The latest comments over the picture, so the host reads the
                // room without leaving the camera (founder, 2026-10-02). A
                // tap opens the full chat, where the host answers and moderates.
                LiveChatOverlay(
                    messages = state.chat.overlayComments(),
                    hostId = state.hostId,
                    moderators = state.moderators,
                    onClick = onOpenChat,
                    emptyHint = "No comments yet. Tap to open chat.",
                    modifier = Modifier.align(Alignment.BottomStart),
                )
            }
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
    viewerCap: Int,
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
        viewerCapNote(viewerCap)?.let { note ->
            Spacer(Modifier.height(UsTheme.spacing.m))
            Text(
                note,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.onMediaMuted,
                modifier = Modifier.testTag("live-viewer-cap"),
            )
        }
    }
}

// ── Watching ────────────────────────────────────────────────────────────

@Composable
fun LiveWatchScreen(
    onClose: () -> Unit,
    viewModel: LiveWatchViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    // Full screen (2026-10-02): the chat list steps aside and the latest
    // comments float over the video instead. A tap on them brings the list back.
    var fullScreen by rememberSaveable { mutableStateOf(false) }
    val share = rememberLiveSharer()

    UsTheme(darkTheme = true) {
        // 2026-10-02: the room is dark on a light device too, so both system bars keep light glyphs.
        LightSystemBarGlyphs()
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
                    WatchBarActions(
                        fullScreen = fullScreen,
                        onToggleFullScreen = { fullScreen = !fullScreen },
                        onShare = if (state.showsShare) {
                            { share(state.title, state.streamId) }
                        } else {
                            null
                        },
                        onReport = viewModel::onReportStream,
                    )
                }
                if (!state.isHost && state.hostId.isNotBlank()) {
                    WatchHostRow(state = state, onFollow = viewModel::onFollowHost)
                }
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .weight(1f),
                ) {
                    WatchStage(state = state, viewModel = viewModel)
                    if (fullScreen && chatShown(state)) {
                        LiveChatOverlay(
                            messages = state.chat.overlayComments(),
                            hostId = state.hostId,
                            moderators = state.moderators,
                            onClick = { fullScreen = false },
                            emptyHint = null,
                            modifier = Modifier.align(Alignment.BottomStart),
                        )
                    }
                    if (state.showsHearts) {
                        LiveHeartButton(
                            count = state.heartCount,
                            block = state.heartBlock,
                            onTap = viewModel::onHeart,
                            modifier = Modifier
                                .align(Alignment.BottomEnd)
                                .padding(UsTheme.spacing.l),
                        )
                    }
                }
                if (chatShown(state)) state.pinned?.let { LivePinnedBar(it) }
                if (!fullScreen && chatShown(state)) {
                    LiveChatList(
                        messages = state.chat.messages,
                        hostId = state.hostId,
                        moderators = state.moderators,
                        onMessageClick = null,
                        onMessageLongClick = viewModel::onMessageLongPress,
                        hint = if (state.canModerate) {
                            "Press and hold a message to moderate it."
                        } else {
                            "Press and hold a message to report it."
                        },
                        joins = state.joins,
                        meId = state.meId,
                    )
                }
                when {
                    state.showsComposer -> LiveChatComposer(
                        draft = state.draft,
                        sending = state.sending,
                        onDraftChanged = viewModel::onDraftChanged,
                        onSend = viewModel::onSendChat,
                    )
                    !state.canChat -> Unit
                    state.chatBanned -> LiveChatClosedNotice(CHAT_BANNED_NOTICE_COPY)
                    state.muted -> LiveChatClosedNotice(MUTED_NOTICE_COPY)
                }
            }
            UsMessageHost(
                message = state.notice?.let { UsMessage(it, UsMessageType.Error) },
                onDismiss = viewModel::onNoticeShown,
            )
        }

        WatchSheets(state = state, viewModel = viewModel)
    }
}

/** Full screen (or back to the chat), Share (viewers only; null hides it), and Report. */
@Composable
private fun WatchBarActions(
    fullScreen: Boolean,
    onToggleFullScreen: () -> Unit,
    onShare: (() -> Unit)?,
    onReport: () -> Unit,
) {
    IconButton(onClick = onToggleFullScreen, modifier = Modifier.testTag("live-full-screen")) {
        Icon(
            if (fullScreen) UsIcons.Minimize else UsIcons.Maximize,
            contentDescription = if (fullScreen) "Show chat" else "Full screen",
            tint = UsTheme.extended.onMedia,
        )
    }
    if (onShare != null) {
        IconButton(onClick = onShare, modifier = Modifier.testTag("live-share")) {
            Icon(UsIcons.Share, contentDescription = "Share this live stream", tint = UsTheme.extended.onMedia)
        }
    }
    IconButton(onClick = onReport, modifier = Modifier.testTag("live-report-stream")) {
        Icon(UsIcons.Flag, contentDescription = "Report stream", tint = UsTheme.extended.onMedia)
    }
}

/**
 * Who is live, under the bar: the host's name (never an id), the Founding
 * creator badge, and Follow while the viewer is known not to follow them.
 * Not drawn for the host watching their own stream.
 */
@Composable
private fun WatchHostRow(state: LiveWatchViewModel.UiState, onFollow: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.xs)
            .testTag("live-host-row"),
    ) {
        Text(
            creatorName(state.host),
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMedia,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
        if (state.host?.badges.orEmpty().any { it.equals(BADGE_FOUNDING_CREATOR, ignoreCase = true) }) {
            FoundingCreatorBadge()
        }
        Spacer(Modifier.weight(1f))
        if (state.showsFollow) {
            UsFollowButton(
                onClick = onFollow,
                busy = state.followBusy,
                modifier = Modifier.testTag("live-follow"),
            )
        }
    }
}

/** Share the room's web address through the system sheet. */
@Composable
private fun rememberLiveSharer(): (title: String, streamId: String) -> Unit {
    val context = LocalContext.current
    return remember(context) {
        { title, streamId ->
            liveShareText(title, streamId)?.let { text ->
                val send = Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, text)
                }
                // createChooser: without it Android can silently reuse a previous default target.
                context.startActivity(Intent.createChooser(send, null))
            }
        }
    }
}

/** A moderator's menu for one message, and the report sheet. */
@Composable
private fun WatchSheets(state: LiveWatchViewModel.UiState, viewModel: LiveWatchViewModel) {
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

/** The chat exists once the join has succeeded: no list and no overlay over a loader or a failure. */
private fun chatShown(state: LiveWatchViewModel.UiState): Boolean = !state.loading && state.joinError == null

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
