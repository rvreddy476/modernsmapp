package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMomentumHeader
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.notifications.ui.UnreadBadgeViewModel
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReportReason
import com.us.android.core.ui.UsStat
import com.us.android.core.ui.UsStatRow
import com.us.android.feature.profile.navigation.MomentumHeaderDestinations

/**
 * Profile screen — stateful entry point.
 *
 * This composable does exactly two things: collect state and forward events.
 * Everything that renders is a stateless composable below it, which is what
 * makes the screen previewable in every state and screenshot-testable without
 * a ViewModel, a DI graph, or a network fake.
 *
 * [onBack] is null when this screen is a tab root and non-null when it was
 * pushed onto the stack. The top bar renders a back control only in the second
 * case — a tab root with a back arrow sends the user somewhere they did not
 * come from.
 */
@Composable
fun ProfileScreen(
    destinations: ProfileDestinations,
    viewModel: ProfileViewModel = hiltViewModel(),
    startChatViewModel: StartChatViewModel = hiltViewModel(),
    mediaViewModel: ProfileMediaViewModel = hiltViewModel(),
    reportViewModel: ProfileReportViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val chatState by startChatViewModel.state.collectAsStateWithLifecycle()
    val mediaState by mediaViewModel.state.collectAsStateWithLifecycle()
    val reportState by reportViewModel.report.collectAsStateWithLifecycle()
    val shareProfile = rememberProfileSharer()

    val loadedProfile = (state as? ProfileUiState.Content)?.profile
    LaunchedEffect(loadedProfile?.avatarMediaId, loadedProfile?.coverMediaId) {
        loadedProfile?.let { profile ->
            mediaViewModel.bind(profile.avatarMediaId, profile.coverMediaId)
        }
    }

    // The server returns the conversation; the host decides that opening it
    // means pushing the thread. Consumed once and cleared, or the thread would
    // be pushed again every time this screen resumed.
    LaunchedEffect(chatState.openConversation) {
        chatState.openConversation?.let { open ->
            destinations.onOpenChat?.invoke(open.conversationId, open.title)
            startChatViewModel.onConversationOpened()
        }
    }

    ProfileContent(
        state = state,
        destinations = destinations,
        actions = ProfileActions(
            onRetry = viewModel::load,
            onFollowToggle = viewModel::onFollowToggle,
            onBlockToggle = viewModel::onBlockToggle,
            onDismissActionError = viewModel::dismissActionError,
            onConfirmCancelRequest = viewModel::onConfirmCancelRequest,
            onDismissCancelRequestConfirm = viewModel::onDismissCancelRequestConfirm,
            onMessage = if (destinations.onOpenChat == null) {
                null
            } else {
                { userId, name -> startChatViewModel.open(userId, name) }
            },
            onDismissChatError = startChatViewModel::dismissError,
            onOpenMenu = reportViewModel::reset,
            // The LOADED profile's id — the account on screen — never a
            // route argument the screen has not resolved yet.
            onReportProfile = { reason, details ->
                loadedProfile?.let { reportViewModel.report(it, reason, details) }
            },
            onShareProfile = { loadedProfile?.let(shareProfile) },
        ),
        chatState = chatState,
        media = ProfileMediaUrls(mediaState.avatarUrl, mediaState.coverUrl),
        report = reportState,
    )
}

data class ProfileDestinations(
    val onOpenFollowers: (userId: String) -> Unit,
    val onOpenFollowing: (userId: String) -> Unit,
    val onBack: (() -> Unit)? = null,
    val onEditProfile: (() -> Unit)? = null,
    val onOpenSettings: (() -> Unit)? = null,
    val onOpenChat: ((conversationId: String, title: String) -> Unit)? = null,
    /**
     * Opens the incoming follow-requests screen. Only ever supplied to the
     * OWN-profile registration — approving strangers into someone else's
     * private account is not a control a viewer of that profile could reach.
     */
    val onOpenFollowRequests: (() -> Unit)? = null,
    /**
     * Present on the Me TAB only: it wears the Momentum header (wordmark,
     * search, bell, More) like every other top-level page but Home. A pushed
     * profile keeps the titled bar with its back arrow.
     */
    val header: MomentumHeaderDestinations? = null,
    /**
     * A tile of the media grid was tapped (2026-09-05): `:app` decides
     * that a `long_video` opens Tube's watch screen and anything else the
     * post. Null leaves the tiles inert — a grid that cannot open anything
     * is still worth looking at.
     */
    val onOpenPost: ((postId: String, contentType: String) -> Unit)? = null,
    /**
     * A publish this screen was showing the progress of has landed, and its
     * content type says where. Only ever supplied to the OWN-profile
     * registration: the pending tiles are the viewer's own, so nobody else's
     * profile could raise it.
     */
    val onPublished: ((contentType: String) -> Unit)? = null,
    /**
     * The Me tab's drawer "Creator Hub" (T-31): `:app` pushes the
     * read-only hub. OWN-profile only, like [onEditProfile]: a creator's
     * numbers are theirs alone.
     */
    val onOpenCreatorHub: (() -> Unit)? = null,
    /** The Me tab's drawer "Offline videos": `:app` pushes the Offline page (Tube's graph). */
    val onOpenOffline: (() -> Unit)? = null,
    /** The Me tab's drawer "Upload": `:app` opens the Create sheet the bar's centre button opens. */
    val onOpenUpload: (() -> Unit)? = null,
)

/**
 * "Show this grid tab": the Me tab's drawer opened Liked or Saved. [seq]
 * makes a second tap on the same row a new request, so the page scrolls to
 * the grid again even when the tab is already the one selected.
 */
internal data class GridTabRequest(val tab: ProfileGridTab, val seq: Int)

internal data class ProfileActions(
    val onRetry: () -> Unit,
    val onFollowToggle: () -> Unit,
    val onBlockToggle: () -> Unit,
    val onDismissActionError: () -> Unit,
    val onConfirmCancelRequest: () -> Unit = {},
    val onDismissCancelRequestConfirm: () -> Unit = {},
    val onMessage: ((userId: String, displayName: String) -> Unit)? = null,
    val onDismissChatError: () -> Unit = {},
    /** The More menu on someone else's profile opened: a fresh report step. */
    val onOpenMenu: () -> Unit = {},
    /** Report the account on screen (entity type `user`, audit T-03). */
    val onReportProfile: (reason: UsReportReason, details: String) -> Unit = { _, _ -> },
    /** Share the account on screen's public web address (T-21). */
    val onShareProfile: () -> Unit = {},
)

internal data class ProfileMediaUrls(
    val avatar: String? = null,
    val cover: String? = null,
)

/** Stateless renderer. Receives immutable state and callbacks; fetches nothing. */
@Composable
internal fun ProfileContent(
    state: ProfileUiState,
    actions: ProfileActions,
    destinations: ProfileDestinations,
    modifier: Modifier = Modifier,
    chatState: StartChatUiState = StartChatUiState(),
    media: ProfileMediaUrls = ProfileMediaUrls(),
    report: UsPostReportState = UsPostReportState.Idle,
) {
    // Someone else's loaded profile only: the viewer's own gets no rows, so
    // no More control is drawn at all (audit T-03); the owner's Share profile
    // is a button beside Edit profile instead.
    val menuRows = (state as? ProfileUiState.Content)
        ?.let { profileMenuRows(isOwnProfile = it.profile.isOwnProfile, canShare = canShareProfile(it.profile)) }
        .orEmpty()
    var menuOpen by rememberSaveable { mutableStateOf(false) }
    // The Me tab's ☰ drawer (2026-10-17), and the grid tab it last asked for.
    var drawerOpen by rememberSaveable { mutableStateOf(false) }
    var tabRequest by remember { mutableStateOf<GridTabRequest?>(null) }
    val drawerSections = meDrawerFor(state, destinations)

    UsScaffold(
        modifier = modifier,
        topBar = {
            ProfileTopBar(
                state = state,
                destinations = destinations,
                hasMenu = menuRows.isNotEmpty(),
                onOpenMenu = {
                    actions.onOpenMenu()
                    menuOpen = true
                },
                onOpenDrawer = { drawerOpen = true }.takeIf { drawerSections.isNotEmpty() },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        when (state) {
            is ProfileUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading profile",
            )

            is ProfileUiState.Error -> UsErrorState(
                message = state.message,
                modifier = Modifier.padding(padding),
                onRetry = if (state.retryable) actions.onRetry else null,
            )

            is ProfileUiState.Content -> LoadedProfile(
                state = state,
                actions = actions,
                destinations = destinations,
                chatState = chatState,
                media = media,
                tabRequest = tabRequest,
                modifier = Modifier.padding(padding),
            )
        }
    }

    if (menuOpen && menuRows.isNotEmpty()) {
        ProfileMoreSheet(
            rows = menuRows,
            report = report,
            onReport = actions.onReportProfile,
            onDismiss = { menuOpen = false },
            onShare = actions.onShareProfile,
        )
    }

    if (drawerOpen && drawerSections.isNotEmpty()) {
        MeDrawer(
            sections = drawerSections,
            onRow = { row ->
                val tab = row.gridTab()
                if (tab != null) {
                    tabRequest = GridTabRequest(tab, (tabRequest?.seq ?: 0) + 1)
                } else {
                    destinations.openDrawerRow(row)
                }
            },
            onDismiss = { drawerOpen = false },
        )
    }
}

/**
 * The top of a profile. The Me tab wears the Momentum header with the
 * standard corner (Search, the bell) and the ☰ in More's slot, which opens
 * its drawer: Messages, Creator Hub and Settings & privacy live there
 * (Settings is still beside "Edit profile" too). A pushed profile gets a
 * titled bar whose title tracks the loaded profile; while loading it stays
 * generic rather than flashing a placeholder name that then changes.
 */
@Composable
private fun ProfileTopBar(
    state: ProfileUiState,
    destinations: ProfileDestinations,
    hasMenu: Boolean,
    onOpenMenu: () -> Unit,
    onOpenDrawer: (() -> Unit)?,
) {
    val header = destinations.header
    if (header != null) {
        OwnProfileHeader(header, onMenu = onOpenDrawer)
        return
    }
    UsTopBar(
        title = state.title(),
        onBack = destinations.onBack,
        actions = {
            if (destinations.onOpenSettings != null) {
                IconButton(onClick = destinations.onOpenSettings) {
                    Icon(UsIcons.Settings, contentDescription = "Settings")
                }
            }
            if (hasMenu) {
                IconButton(onClick = onOpenMenu, modifier = Modifier.testTag("profile_more")) {
                    Icon(UsIcons.More, contentDescription = "More options")
                }
            }
        },
    )
}

/**
 * The drawer this page offers. Before the profile loads, the Me tab (the one
 * page with the header) is the viewer's own; after, the loaded profile says.
 */
internal fun meDrawerFor(state: ProfileUiState, destinations: ProfileDestinations): List<MeDrawerSection> =
    meDrawerSections(
        isOwnProfile = (state as? ProfileUiState.Content)?.profile?.isOwnProfile ?: (destinations.header != null),
        doors = meDrawerDoors(state, destinations),
    )

/** Which of the drawer's doors this page was given; Liked and Saved need the own grid on screen. */
internal fun meDrawerDoors(state: ProfileUiState, destinations: ProfileDestinations): MeDrawerDoors {
    val content = state as? ProfileUiState.Content
    return MeDrawerDoors(
        grid = content != null && content.profile.isOwnProfile && !content.unavailable,
        offline = destinations.onOpenOffline != null,
        messages = destinations.header != null,
        creatorHub = destinations.onOpenCreatorHub != null,
        upload = destinations.onOpenUpload != null,
        settings = destinations.onOpenSettings != null,
    )
}

/** A drawer row that leaves the profile, to the destination `:app` gave for it. */
private fun ProfileDestinations.openDrawerRow(row: MeDrawerRow) {
    when (row) {
        MeDrawerRow.OFFLINE -> onOpenOffline?.invoke()
        MeDrawerRow.MESSAGES -> header?.onOpenMessages?.invoke()
        MeDrawerRow.CREATOR_HUB -> onOpenCreatorHub?.invoke()
        MeDrawerRow.UPLOAD -> onOpenUpload?.invoke()
        MeDrawerRow.SETTINGS -> onOpenSettings?.invoke()
        // Opened in place, by a grid tab request.
        MeDrawerRow.LIKED, MeDrawerRow.SAVED -> Unit
    }
}

/**
 * The bottom of a profile: its media grid, or the placeholder that stands
 * in for it on someone else's private account the viewer does not follow.
 * The viewer's own profile is never hidden from them, however private.
 */
@Composable
private fun ProfileMedia(
    state: ProfileUiState.Content,
    destinations: ProfileDestinations,
    tabRequest: GridTabRequest?,
) {
    val profile = state.profile
    val hidden = !profile.isOwnProfile && profile.isPrivate &&
        state.relationship.followStatus != FollowStatus.FOLLOWING
    if (hidden) {
        PrivatePlaceholder()
    } else {
        ProfileMediaGrid(
            onOpenPost = destinations.onOpenPost,
            onPublished = destinations.onPublished,
            tabRequest = tabRequest,
        )
    }
}

private fun ProfileUiState.title(): String = when (this) {
    is ProfileUiState.Content -> if (profile.isOwnProfile) "My profile" else profile.nameForDisplay
    else -> "Profile"
}

@Composable
private fun LoadedProfile(
    state: ProfileUiState.Content,
    actions: ProfileActions,
    destinations: ProfileDestinations,
    chatState: StartChatUiState,
    media: ProfileMediaUrls,
    modifier: Modifier = Modifier,
    tabRequest: GridTabRequest? = null,
) {
    val profile = state.profile
    Box(modifier = modifier.fillMaxSize()) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            ProfileHeader(profile = profile, avatarUrl = media.avatar, coverUrl = media.cover)

            UsStatRow(
                stats = listOf(
                    UsStat("Posts", state.counts.posts),
                    UsStat("Followers", state.counts.followers) { destinations.onOpenFollowers(profile.userId) },
                    UsStat("Following", state.counts.following) { destinations.onOpenFollowing(profile.userId) },
                ),
            )

            // Editing is offered only when the host supplied a destination AND the
            // loaded profile is genuinely the viewer's. Two conditions rather than
            // one because the endpoint behind the form replaces the OWNER's fields
            // keyed off the access token — an edit control on someone else's page
            // would silently overwrite the viewer's own profile.
            if (profile.isOwnProfile && destinations.onEditProfile != null) {
                OwnProfileActions(
                    onEditProfile = destinations.onEditProfile,
                    onShareProfile = actions.onShareProfile.takeIf { canShareProfile(profile) },
                    // Settings sits here when the Momentum header owns the top
                    // bar; a pushed own-profile (no header) keeps it in the bar.
                    onOpenSettings = destinations.onOpenSettings?.takeIf { destinations.header != null },
                )
            }

            // Same gating as Edit profile, and for the same reason: approving
            // someone into THIS account only makes sense on the account's own
            // screen.
            if (profile.isOwnProfile && destinations.onOpenFollowRequests != null) {
                RequestsPill(
                    count = state.incomingFollowRequestCount,
                    onClick = destinations.onOpenFollowRequests,
                )
            }

            if (state.unavailable) {
                UnavailableNotice(
                    viewerBlocked = state.relationship.isBlocked,
                    busy = state.relationshipBusy,
                    onUnblock = actions.onBlockToggle,
                )
            } else if (!profile.isOwnProfile) {
                MessageControls(
                    profile = profile,
                    chatState = chatState,
                    onMessage = actions.onMessage,
                    onDismissChatError = actions.onDismissChatError,
                )

                RelationshipControls(
                    relationship = state.relationship,
                    busy = state.relationshipBusy,
                    onFollowToggle = actions.onFollowToggle,
                    onBlockToggle = actions.onBlockToggle,
                )
            }

            state.actionError?.let { error ->
                ActionErrorBanner(message = error, onDismiss = actions.onDismissActionError)
            }

            if (!state.unavailable) {
                ProfileMedia(state = state, destinations = destinations, tabRequest = tabRequest)
            }
        }

        // Over the page, not in the column — see [PublishSuccessBanner]. Own
        // profile only, which a non-null `onPublished` already means.
        if (destinations.onPublished != null) {
            PublishSuccessBanner(modifier = Modifier.align(Alignment.TopCenter))
        }
    }

    if (state.showCancelRequestConfirm) {
        CancelRequestDialog(
            onConfirm = actions.onConfirmCancelRequest,
            onDismiss = actions.onDismissCancelRequestConfirm,
        )
    }
}

/** The "Requests" pill on the own-profile header — a count once the first page has one. */
@Composable
private fun RequestsPill(count: Int?, onClick: () -> Unit) {
    UsSecondaryButton(
        text = count?.takeIf { it > 0 }?.let { "Requests ($it)" } ?: "Requests",
        onClick = onClick,
        modifier = Modifier.fillMaxWidth(),
    )
}

/**
 * A failed relationship action, transient and dismissible.
 *
 * Losing a successfully loaded profile to an error screen because an unfollow
 * failed would be a worse outcome than the failure itself.
 */
@Composable
private fun ActionErrorBanner(message: String, onDismiss: () -> Unit) {
    Text(
        text = message,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier
            .fillMaxWidth()
            .semantics { }
            .padding(bottom = UsTheme.spacing.m),
    )
    UsSecondaryButton(text = "Dismiss", onClick = onDismiss, modifier = Modifier.fillMaxWidth())
}

/**
 * A private account the viewer cannot see into: "no posts" would be a lie —
 * there may be plenty, just not to this viewer — so the grid is not drawn
 * and this says why. Everyone else gets [ProfileMediaGrid].
 */
@Composable
private fun PrivatePlaceholder() {
    UsEmptyState(
        title = "This account is private",
        detail = "Follow to see their posts.",
        modifier = Modifier.fillMaxWidth(),
    )
}

/**
 * A profile that blocked the viewer: one neutral sentence in place of the
 * Follow and Message controls and the grid. It reads like any unavailable
 * account, so it gives away neither the block nor who placed it. The
 * viewer's OWN block, when they placed one too ([viewerBlocked]), stays
 * theirs to undo.
 */
@Composable
private fun UnavailableNotice(viewerBlocked: Boolean, busy: Boolean, onUnblock: () -> Unit) {
    UsEmptyState(
        title = "This account isn't available",
        detail = "You can't follow or message this account right now.",
        modifier = Modifier
            .fillMaxWidth()
            .testTag("profile_unavailable"),
    )
    if (viewerBlocked) {
        UsSecondaryButton(
            text = "Unblock",
            onClick = onUnblock,
            enabled = !busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

/**
 * The owner's row under the counts: Edit profile, Share profile (T-21) when
 * the profile has a handle to link to — the visitor's Share lives in the
 * More menu — and the Settings glyph when the header does not carry it.
 */
@Composable
private fun OwnProfileActions(
    onEditProfile: () -> Unit,
    onShareProfile: (() -> Unit)?,
    onOpenSettings: (() -> Unit)?,
) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        // Compact: one line each at the label's own size. The default inset
        // left half a phone too narrow for "Share profile", which wrapped.
        UsSecondaryButton(
            text = "Edit profile",
            onClick = onEditProfile,
            compact = true,
            modifier = Modifier.weight(1f),
        )
        if (onShareProfile != null) {
            UsSecondaryButton(
                text = "Share profile",
                onClick = onShareProfile,
                compact = true,
                modifier = Modifier
                    .weight(1f)
                    .testTag("profile_share"),
            )
        }
        if (onOpenSettings != null) {
            IconButton(onClick = onOpenSettings) {
                Icon(
                    imageVector = UsIcons.Settings,
                    contentDescription = "Settings",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}

/** "Cancel your follow request?" — the one relationship action that confirms before it acts. */
@Composable
private fun CancelRequestDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Cancel follow request?") },
        text = { Text("They won't be notified that you asked.") },
        confirmButton = {
            TextButton(onClick = onConfirm) { Text("Cancel request") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Keep waiting") }
        },
    )
}

/**
 * Start a conversation with the person whose profile this is.
 *
 * The button is ALWAYS offered when the host has somewhere to open a thread —
 * it is never pre-filtered on a locally known privacy setting. graph-service
 * owns that decision and re-evaluates it on every attempt; a client-side copy
 * would be a second implementation of the privacy matrix that goes stale the
 * moment somebody changes a setting, and stale in whichever direction the last
 * deploy happened to choose. A refusal is answered with a reason instead.
 */
@Composable
private fun MessageControls(
    profile: Profile,
    chatState: StartChatUiState,
    onMessage: ((userId: String, displayName: String) -> Unit)?,
    onDismissChatError: () -> Unit,
) {
    if (onMessage != null) {
        UsSecondaryButton(
            text = "Message",
            onClick = { onMessage(profile.userId, profile.nameForDisplay) },
            enabled = !chatState.busy,
            modifier = Modifier.fillMaxWidth(),
        )
    }

    // A policy refusal is NOT retryable, so it gets no retry control. The
    // answer stays the same until the other person changes their settings, and
    // a button that cannot work reads as a broken app rather than as a
    // boundary somebody set.
    chatState.notAllowed?.let { message ->
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.fillMaxWidth(),
        )
    }

    chatState.error?.let { message ->
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.fillMaxWidth(),
        )
        UsSecondaryButton(
            text = "Dismiss",
            onClick = onDismissChatError,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun ProfileHeader(
    profile: Profile,
    avatarUrl: String?,
    coverUrl: String?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.xxxxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        if (!coverUrl.isNullOrBlank()) {
            AsyncImage(
                model = coverUrl,
                contentDescription = "${profile.nameForDisplay}'s cover photo",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(152.dp)
                    .clip(MaterialTheme.shapes.large),
                contentScale = ContentScale.Crop,
            )
        }
        UsAvatar(
            name = profile.nameForDisplay,
            size = UsAvatarSize.Large,
            seed = profile.userId,
            imageUrl = avatarUrl,
            contentDescription = "${profile.nameForDisplay}'s profile photo",
        )

        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(
                    text = profile.nameForDisplay,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.semantics { heading() },
                )
                if (profile.isVerified) {
                    Text(
                        text = "Verified",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.statusSuccess,
                    )
                }
                if (profile.isPrivate) {
                    PrivateBadge()
                }
            }

            profile.subtitle()?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }

            if (profile.bio.isNotBlank()) {
                Text(
                    text = profile.bio,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.padding(top = UsTheme.spacing.m),
                )
            }
        }
    }
}

/** Lock icon + "Private", next to the name — the account-level flag, not a relationship. */
@Composable
private fun PrivateBadge() {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(
            imageVector = UsIcons.Lock,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(PRIVATE_BADGE_ICON),
        )
        Text(
            text = "Private",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

private val PRIVATE_BADGE_ICON = 14.dp

/**
 * Profession and location, joined only when both exist.
 *
 * Every string field on this payload can legitimately be empty — `PUT /me`
 * with an empty body clears them — so a naive `"$profession · $location"`
 * renders a bare middle dot for a large share of real accounts.
 */
private fun Profile.subtitle(): String? =
    listOf(profession, location).filter { it.isNotBlank() }.joinToString(" · ").ifBlank { null }

@Composable
private fun RelationshipControls(
    relationship: ProfileRelationship,
    busy: Boolean,
    onFollowToggle: () -> Unit,
    onBlockToggle: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        if (relationship.isBlocked) {
            // A blocked account offers exactly one action. Showing Follow
            // beside Blocked would present a control the server will reject.
            UsSecondaryButton(
                text = "Unblock",
                onClick = onBlockToggle,
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            when (relationship.followStatus) {
                // A tap here does not unfollow — it arms the cancel-request
                // confirmation. The button itself does not know that; it only
                // reports the tap, same as every other state.
                FollowStatus.FOLLOWING, FollowStatus.REQUESTED -> UsSecondaryButton(
                    text = if (relationship.followStatus == FollowStatus.FOLLOWING) "Following" else "Requested",
                    onClick = onFollowToggle,
                    enabled = !busy,
                    modifier = Modifier.fillMaxWidth(),
                )

                FollowStatus.NONE -> UsButton(
                    text = "Follow",
                    onClick = onFollowToggle,
                    loading = busy,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            UsSecondaryButton(
                text = "Block",
                onClick = onBlockToggle,
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

// ── Previews ────────────────────────────────────────────────────────────
//
// Every state the screen can reach. These are the screenshot-test entry
// points and the reason the renderer is stateless.

private val previewProfile = Profile(
    userId = "03b43cb4-420b-4d2d-baae-2ade11490b19",
    displayName = "Ada Lovelace",
    bio = "Mathematician. Writing notes on the Analytical Engine.",
    category = "personal",
    profession = "Mathematician",
    website = "",
    location = "London",
    badgeFlags = 0,
    isVerified = true,
    verificationLevel = "email",
    statusText = "",
    statusEmoji = "",
    profileThemeColor = "#1A73E8",
    memberSinceBadge = true,
    counts = ProfileCounts(followers = 12_400, following = 318, friends = 12, posts = 42),
    createdAt = "2026-08-16T17:53:05Z",
)

private fun previewState(
    profile: Profile = previewProfile,
    relationship: ProfileRelationship = ProfileRelationship(),
    busy: Boolean = false,
    actionError: String? = null,
) = ProfileUiState.Content(
    profile = profile,
    stats = null,
    relationship = relationship,
    relationshipBusy = busy,
    actionError = actionError,
)

@Composable
private fun PreviewHost(state: ProfileUiState) = UsTheme {
    ProfileContent(
        state = state,
        actions = ProfileActions(
            onRetry = {},
            onFollowToggle = {},
            onBlockToggle = {},
            onDismissActionError = {},
        ),
        destinations = ProfileDestinations({}, {}, onEditProfile = {}),
    )
}

@Preview(name = "Other user — not following", showBackground = true, heightDp = 640)
@Composable
private fun ProfileOtherPreview() = PreviewHost(previewState())

@Preview(name = "Other user — following", showBackground = true, heightDp = 640)
@Composable
private fun ProfileFollowingPreview() = PreviewHost(
    previewState(relationship = ProfileRelationship(isFollowing = true, followStatus = FollowStatus.FOLLOWING)),
)

@Preview(name = "Other user — blocked", showBackground = true, heightDp = 640)
@Composable
private fun ProfileBlockedPreview() =
    PreviewHost(previewState(relationship = ProfileRelationship(isBlocked = true)))

@Preview(name = "Other user — not available to the viewer", showBackground = true, heightDp = 640)
@Composable
private fun ProfileUnavailablePreview() =
    PreviewHost(previewState(relationship = ProfileRelationship(isBlockedByTarget = true)))

@Preview(name = "Private account — not following", showBackground = true, heightDp = 640)
@Composable
private fun ProfilePrivateNotFollowingPreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(isPrivate = true),
        relationship = ProfileRelationship(isPrivate = true),
    ),
)

@Preview(name = "Private account — requested", showBackground = true, heightDp = 640)
@Composable
private fun ProfilePrivateRequestedPreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(isPrivate = true),
        relationship = ProfileRelationship(isPrivate = true, followStatus = FollowStatus.REQUESTED),
    ),
)

@Preview(name = "Private account — following", showBackground = true, heightDp = 640)
@Composable
private fun ProfilePrivateFollowingPreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(isPrivate = true),
        relationship = ProfileRelationship(
            isFollowing = true,
            isPrivate = true,
            followStatus = FollowStatus.FOLLOWING,
        ),
    ),
)

@Preview(name = "Own profile", showBackground = true, heightDp = 640)
@Composable
private fun ProfileOwnPreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(
            personal = PersonalProfile(
                firstName = "Ada",
                lastName = "Lovelace",
                dateOfBirth = "1815-12-10T00:00:00Z",
                gender = "female",
                timezone = "",
                introMediaUrl = "",
                ctaUrl = "",
                updatedAt = "2026-08-16T17:59:08Z",
            ),
        ),
    ),
)

@Preview(name = "Cleared display name", showBackground = true, heightDp = 640)
@Composable
private fun ProfileClearedNamePreview() = PreviewHost(
    previewState(
        profile = previewProfile.copy(displayName = "", profession = "", location = ""),
    ),
)

@Preview(name = "Action failed", showBackground = true, heightDp = 640)
@Composable
private fun ProfileActionErrorPreview() =
    PreviewHost(previewState(actionError = "That didn't go through. Try again."))

@Preview(name = "Loading", showBackground = true, heightDp = 320)
@Composable
private fun ProfileLoadingPreview() = PreviewHost(ProfileUiState.Loading)

@Preview(name = "Error — retryable", showBackground = true, heightDp = 320)
@Composable
private fun ProfileErrorPreview() = PreviewHost(
    ProfileUiState.Error("You're offline. Check your connection and try again.", retryable = true),
)

@Preview(name = "Error — terminal", showBackground = true, heightDp = 320)
@Composable
private fun ProfileErrorTerminalPreview() =
    PreviewHost(ProfileUiState.Error("This profile isn't available.", retryable = false))

/**
 * The Me tab's Momentum header with its live unread count. The pure layout
 * is [UsMomentumHeader]; this only binds the badge, refreshed when the tab
 * appears rather than polled — the same contract Home uses.
 *
 * founder, 2026-10-02: every page but Home reads Search, the bell, More.
 * 2026-10-17: on the Me tab More's slot is the ☰, which opens the drawer
 * ([MeDrawer], hosted by the page so it knows which grid tabs are there).
 */
@Composable
private fun OwnProfileHeader(
    header: MomentumHeaderDestinations,
    onMenu: (() -> Unit)?,
    viewModel: UnreadBadgeViewModel = hiltViewModel(),
) {
    val count by viewModel.count.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.refresh() }

    UsMomentumHeader(
        unreadCount = count,
        onSearch = header.onOpenSearch,
        onNotifications = header.onOpenNotifications,
        modifier = Modifier.testTag("me_header"),
        onMenu = onMenu,
    )
}
