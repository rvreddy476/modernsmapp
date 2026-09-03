package com.us.android.feature.notifications.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.ui.UsEmptyState
import com.us.android.feature.notifications.permission.NotificationPermissionPrompt
import java.time.Duration
import java.time.Instant

/**
 * The notification inbox — Slice D, restyled as Momentum's "Activity"
 * screen (Figma YsWb936muw8pwIxgb0je2A): a Follow Requests panel above the
 * list, rows banded New / This Week / Earlier, unread rows on the highlight
 * surface with a gradient avatar ring, and the one action a row can take
 * sitting ON the row as a gradient pill.
 *
 * Renders [NotificationsUiState] and calls back. It performs no network work
 * and keeps no parallel copy of read-state.
 *
 * ## THE TARGET GOES BACK TO :app
 *
 * Tapping a row hands a [NotificationTarget] to [onOpenTarget]. This module
 * does not know that a post target means `:feature:post` — `:app` owns that
 * mapping, which is what keeps features independent of one another. Same
 * contract the composer uses for `onPublished`.
 */
@Composable
fun NotificationsScreen(
    onBack: () -> Unit,
    onOpenTarget: (NotificationTarget) -> Unit,
    /**
     * Slice D / D-D7. Notification PREFERENCES live in `:feature:profile`; the
     * inbox is where a user actually thinks about them. `:app` owns the route,
     * so the two features stay independent.
     */
    onOpenPreferences: () -> Unit,
    /** Opens the profile module's approval queue — the Follow Requests panel. */
    onOpenFollowRequests: () -> Unit = {},
    viewModel: NotificationsViewModel = hiltViewModel(),
    /**
     * The runtime permission prompt — Slice D, D-D2.
     *
     * A SLOT with the real default rather than a hard call, because the prompt
     * resolves its own Hilt ViewModel and would otherwise force every test that
     * renders this screen to stand up a Hilt graph for a concern it is not
     * testing. The prompt has its own tests.
     */
    permissionPrompt: @Composable () -> Unit = { NotificationPermissionPrompt() },
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()

    // Paging by proximity to the end, not by "the last item appeared". A user
    // who flings past the bottom would otherwise see a stall before the next
    // page is even requested.
    val shouldLoadMore by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            last >= listState.layoutInfo.totalItemsCount - LOAD_MORE_THRESHOLD
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) viewModel.loadMore()
    }

    val callbacks = remember(viewModel, onOpenTarget) {
        RowCallbacks(
            onOpen = { notification, target ->
                viewModel.onNotificationOpened(notification)
                // NOT gated on the mark-read request. Making someone wait for
                // a write they can already see succeed is the wrong trade.
                if (target != NotificationTarget.None) onOpenTarget(target)
            },
            onFollow = viewModel::follow,
            onAccept = viewModel::acceptRequest,
            onDecline = viewModel::declineRequest,
            onBlock = viewModel::blockRequest,
            onAcceptFollow = viewModel::acceptFollowRequest,
            onDeclineFollow = viewModel::declineFollowRequest,
        )
    }

    UsScaffold(
        topBar = {
            ActivityTopBar(
                onBack = onBack,
                onOpenPreferences = onOpenPreferences,
                hasUnread = state.hasUnread,
                onMarkAllRead = viewModel::markAllRead,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            // Ask for the notification permission HERE — the user has just
            // said, by opening this screen, that they care about
            // notifications. See NotificationPermissionPrompt for why not at
            // first launch. Renders nothing unless there is something to say.
            permissionPrompt()

            // Momentum's Follow Requests panel — a raised shortcut to the
            // approval queue, derived from the SAME rows the list below
            // already loaded rather than a second fetch.
            val followRequests = remember(state.items) {
                state.items.filter { it.kind == NotificationKind.FollowRequest }
            }
            if (followRequests.isNotEmpty()) {
                FollowRequestsPanel(
                    count = followRequests.size,
                    hasUnread = followRequests.any { !it.isRead },
                    onClick = onOpenFollowRequests,
                )
            }

            when {
                state.isLoading -> LoadingBlock()

                state.error != null -> UsEmptyState(
                    title = "Something went wrong",
                    detail = state.error.orEmpty(),
                    modifier = Modifier.fillMaxWidth(),
                )

                state.isEmpty -> UsEmptyState(
                    title = "Nothing yet",
                    detail = "Reactions, comments, follows and message requests will show up here.",
                    modifier = Modifier.fillMaxWidth(),
                )

                else -> SectionedNotificationList(state = state, listState = listState, callbacks = callbacks)
            }
        }
    }
}

/**
 * The "Activity" header: back, the screen title (Outfit ExtraBold 22 —
 * `titleLarge`), and on the right "Mark all read" as an accent text link
 * whenever anything is unread, then the route to notification PREFERENCES
 * (Slice D, D-D7 — they live in `:feature:profile`; the inbox is where
 * someone actually forms the thought "this is too many notifications").
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ActivityTopBar(
    onBack: () -> Unit,
    onOpenPreferences: () -> Unit,
    hasUnread: Boolean,
    onMarkAllRead: () -> Unit,
) {
    TopAppBar(
        title = {
            Text(
                text = "Activity",
                style = MaterialTheme.typography.titleLarge,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.semantics { heading() },
            )
        },
        navigationIcon = {
            IconButton(onClick = onBack) {
                Icon(
                    imageVector = UsIcons.Back,
                    contentDescription = "Back",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        },
        actions = {
            // Rendered only while something is unread: a "Mark all read" that
            // can do nothing would be a promise the screen cannot keep.
            if (hasUnread) {
                TextButton(onClick = onMarkAllRead) {
                    Text(
                        text = "Mark all read",
                        style = MaterialTheme.typography.labelLarge,
                        fontSize = HEADER_LINK_SIZE,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.accentSolid,
                    )
                }
            }
            IconButton(onClick = onOpenPreferences) {
                Icon(
                    imageVector = UsIcons.Settings,
                    contentDescription = "Notification settings",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = Color.Transparent,
            scrolledContainerColor = Color.Transparent,
        ),
    )
}

/**
 * The Momentum Follow Requests panel: a raised shortcut above the list that
 * opens the same approval queue a profile's own entry point does. Derived
 * from the loaded rows rather than a dedicated count endpoint — Momentum's
 * server-driven behaviour (what a request IS, how it is decided) is
 * unchanged; only where the shortcut lives is new.
 */
@Composable
private fun FollowRequestsPanel(count: Int, hasUnread: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .clip(RoundedCornerShape(UsTheme.radii.panel))
            .background(UsTheme.extended.bgRaised)
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .size(REQUEST_ICON_CIRCLE)
                .clip(CircleShape)
                .background(UsTheme.extended.ctaGradient),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Requests,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(ROW_BADGE_GLYPH),
            )
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = "Follow Requests",
                style = MaterialTheme.typography.bodyMedium,
                fontSize = PANEL_TITLE_SIZE,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Approve or ignore requests",
                style = MaterialTheme.typography.bodyMedium,
                fontSize = PANEL_SUBTITLE_SIZE,
                color = UsTheme.extended.textMuted,
            )
        }
        if (hasUnread) {
            UnreadDot(isRead = false)
        }
        Text(
            text = "$count",
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** Everything a row can do, bundled so the list and the row share one signature. */
private data class RowCallbacks(
    val onOpen: (Notification, NotificationTarget) -> Unit,
    val onFollow: (Notification) -> Unit,
    val onAccept: (Notification) -> Unit,
    val onDecline: (Notification) -> Unit,
    val onBlock: (Notification) -> Unit,
    val onAcceptFollow: (Notification) -> Unit,
    val onDeclineFollow: (Notification) -> Unit,
)

/**
 * Three age bands, per the Momentum activity frame: "New" (today), "This
 * Week", then "Earlier". The list arrives newest-first, so grouping
 * preserves the order within each band.
 */
private enum class AgeBand(val label: String) { New("New"), Week("This Week"), Older("Earlier") }

@Composable
private fun SectionedNotificationList(
    state: NotificationsUiState,
    listState: LazyListState,
    callbacks: RowCallbacks,
) {
    val now = remember(state.items) { Instant.now() }
    val bands = remember(state.items, now) { state.items.groupBy { ageBand(it.createdAt, now) } }
    LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
        AgeBand.entries.forEach { band ->
            val rows = bands[band].orEmpty()
            if (rows.isNotEmpty()) {
                item(key = "section-${band.name}") { SectionHeader(band.label) }
                notificationRows(rows, state, callbacks)
            }
        }
        if (state.isLoadingMore) {
            item { LoadingBlock() }
        }
    }
}

private fun LazyListScope.notificationRows(
    rows: List<Notification>,
    state: NotificationsUiState,
    callbacks: RowCallbacks,
) {
    items(rows, key = { it.id }) { notification ->
        NotificationRow(
            notification = notification,
            action = state.rowActions[notification.id],
            requestPending = notification.entityId in state.pendingRequestIds,
            alreadyFollowing = notification.actorUserId in state.followingIds,
            callbacks = callbacks,
        )
    }
}

/** Section label — 14sp bold in the muted step, per the frame. */
@Composable
private fun SectionHeader(label: String) {
    Text(
        text = label,
        style = MaterialTheme.typography.bodyMedium,
        fontSize = SECTION_SIZE,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.m,
        ),
    )
}

private fun ageBand(isoInstant: String, now: Instant): AgeBand {
    val then = runCatching { Instant.parse(isoInstant) }.getOrNull() ?: return AgeBand.Older
    val age = Duration.between(then, now)
    return when {
        age < Duration.ofDays(1) -> AgeBand.New
        age < Duration.ofDays(WEEK_DAYS) -> AgeBand.Week
        else -> AgeBand.Older
    }
}

// Momentum activity row: 36dp avatar (gradient-ringed while unread), one
// line of text with the actor in bold and the time inline and muted, the
// row's action trailing, and an unread row sitting on the highlight
// surface. A missed call keeps its red phone badge — the one row type whose
// absence costs the user something, so it must not look like one more like.
@Composable
private fun NotificationRow(
    notification: Notification,
    action: RowActionState?,
    requestPending: Boolean,
    alreadyFollowing: Boolean,
    callbacks: RowCallbacks,
) {
    val sentence = notification.describe()
    val missedCall = notification.kind == NotificationKind.MissedCall
    // A pending request opens the decision screen; anything else opens what
    // the row points at. The inbox knows which because the server told it.
    val target = if (requestPending) {
        NotificationTarget.MessageRequest(notification.entityId, notification.actorName)
    } else {
        notification.target
    }
    val rowSurface = if (notification.isRead) Color.Transparent else UsTheme.extended.unreadRow

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(rowSurface)
            .clickable { callbacks.onOpen(notification, target) }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = ROW_VERTICAL),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        // A fixed slot the ring grows INTO, so read and unread rows keep the
        // same text column rather than shifting by the ring's width.
        Box(modifier = Modifier.size(AVATAR_SLOT), contentAlignment = Alignment.Center) {
            if (missedCall) {
                MissedCallBadge()
            } else {
                UsAvatar(
                    name = notification.actorName,
                    size = UsAvatarSize.Post,
                    seed = notification.actorUserId,
                    hasRing = !notification.isRead,
                )
            }
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = notification.rowText(sentence),
                style = MaterialTheme.typography.bodyMedium,
                color = if (missedCall) UsTheme.extended.liveRed else UsTheme.extended.textPrimary,
                // ONE node to a screen reader, carrying the sentence and the
                // unread state. The time is decoration; the buttons speak for
                // themselves.
                modifier = Modifier.clearAndSetSemantics {
                    contentDescription = if (notification.isRead) sentence else "Unread. $sentence"
                },
            )
            if (notification.kind == NotificationKind.MessageRequest) {
                RequestActions(notification, action, requestPending, callbacks)
            }
            if (notification.kind == NotificationKind.FollowRequest) {
                FollowRequestActions(notification, action, callbacks)
            }
            if (action == RowActionState.Failed) {
                Text(
                    text = "Couldn't do that. Try again.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.liveRed,
                )
            }
        }
        if (notification.kind == NotificationKind.Follow) {
            FollowAction(notification, action, alreadyFollowing, callbacks.onFollow)
        }
        UnreadDot(notification.isRead)
    }
}

/** Accept / Decline / Block under a message request, or the outcome once decided. */
@Composable
private fun RequestActions(
    notification: Notification,
    action: RowActionState?,
    requestPending: Boolean,
    callbacks: RowCallbacks,
) {
    val outcome = when (action) {
        RowActionState.Accepted -> "Accepted"
        RowActionState.Declined -> "Declined"
        RowActionState.Blocked -> "Blocked"
        else -> null
    }
    when {
        outcome != null -> OutcomeLabel(outcome)

        requestPending -> {
            val busy = action == RowActionState.Busy
            Row(
                modifier = Modifier.padding(top = UsTheme.spacing.s),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                UsPillButton("Accept", busy = busy, onClick = { callbacks.onAccept(notification) })
                UsPillButton("Decline", filled = false, busy = busy, onClick = { callbacks.onDecline(notification) })
                UsPillButton("Block", filled = false, busy = busy, onClick = { callbacks.onBlock(notification) })
            }
        }
    }
}

/**
 * Accept / Decline under an incoming follow request, or the outcome once
 * decided.
 *
 * No Block here, unlike [RequestActions]: a follow request is not a stranger
 * message landing in the inbox — declining it is the whole boundary this
 * control offers, and the account's existing block control lives on their
 * profile for the case where more than "no" is warranted.
 */
@Composable
private fun FollowRequestActions(
    notification: Notification,
    action: RowActionState?,
    callbacks: RowCallbacks,
) {
    val outcome = when (action) {
        RowActionState.Accepted -> "Accepted"
        RowActionState.Declined -> "Declined"
        else -> null
    }
    if (outcome != null) {
        OutcomeLabel(outcome)
    } else {
        val busy = action == RowActionState.Busy
        Row(
            modifier = Modifier.padding(top = UsTheme.spacing.s),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            UsPillButton("Accept", busy = busy, onClick = { callbacks.onAcceptFollow(notification) })
            UsPillButton(
                "Decline",
                filled = false,
                busy = busy,
                onClick = { callbacks.onDeclineFollow(notification) },
            )
        }
    }
}

@Composable
private fun OutcomeLabel(outcome: String) {
    Text(
        text = outcome,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(top = UsTheme.spacing.xs),
    )
}

/** Follow back, or "Following" once the graph says so. */
@Composable
private fun FollowAction(
    notification: Notification,
    action: RowActionState?,
    alreadyFollowing: Boolean,
    onFollow: (Notification) -> Unit,
) {
    if (alreadyFollowing || action == RowActionState.Followed) {
        UsPillButton("Following", filled = false, enabled = false, onClick = {})
    } else {
        UsPillButton("Follow back", busy = action == RowActionState.Busy, onClick = { onFollow(notification) })
    }
}

@Composable
private fun MissedCallBadge() {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(UsAvatarSize.Post.diameter)
            .clip(CircleShape)
            .background(MISSED_CALL_BAND),
    ) {
        Icon(
            imageVector = UsIcons.Phone,
            contentDescription = null,
            tint = UsTheme.extended.liveRed,
            modifier = Modifier.size(ROW_BADGE_GLYPH),
        )
    }
}

/** The unread mark is an 8dp accent-gradient dot, not bold text: weight already means "the actor". */
@Composable
private fun UnreadDot(isRead: Boolean) {
    Box(
        modifier = Modifier
            .size(UNREAD_DOT)
            .clip(CircleShape)
            .then(if (isRead) Modifier else Modifier.background(UsTheme.extended.ctaGradient)),
    )
}

@Composable
private fun LoadingBlock() {
    Box(
        modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l),
        contentAlignment = Alignment.Center,
    ) {
        CircularProgressIndicator()
    }
}

/**
 * The row's visible text: the actor in bold, the rest plain, the relative
 * time trailing in the muted colour — exactly the frame's one-line layout.
 * [sentence] is [describe]'s output, so the two can never disagree.
 */
@Composable
private fun Notification.rowText(sentence: String) = buildAnnotatedString {
    val who = actorName.ifBlank { "Someone" }
    if (sentence.startsWith(who)) {
        withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(who) }
        append(sentence.removePrefix(who))
    } else {
        append(sentence)
    }
    val time = formatRelativeTime(createdAt)
    if (time.isNotBlank()) {
        withStyle(SpanStyle(color = UsTheme.extended.textMuted)) { append("  $time") }
    }
}

/**
 * The sentence a row shows.
 *
 * The actor's name comes from the server's batch hydration; when it is
 * missing (hydration failed, or the account is gone) the row says "Someone"
 * — honest, and never an id dressed up as a name.
 *
 * [NotificationKind.Unknown] renders a generic line rather than being dropped:
 * one notification service serves every vertical in this super-app, so this
 * client WILL receive types it has no screen for, and a silently missing row is
 * worse than a vague one.
 */
@Suppress("CyclomaticComplexMethod") // One branch per kind: a lookup table, not logic.
internal fun Notification.describe(): String {
    val who = actorName.ifBlank { "Someone" }
    return when (kind) {
        NotificationKind.Reaction -> "$who reacted to your post"
        NotificationKind.Comment -> "$who commented on your post"
        NotificationKind.CommentReaction -> "$who reacted to your comment"
        NotificationKind.Follow -> "$who started following you"
        NotificationKind.Mention -> "$who mentioned you in a post"
        NotificationKind.Repost -> "$who reposted your post"
        NotificationKind.ConnectionRequest -> "$who sent you a connection request"
        NotificationKind.ConnectionAccepted -> "$who accepted your connection request"
        NotificationKind.NewSubscriber -> "$who subscribed to you"
        NotificationKind.MessageRequest -> "$who sent you a message request"
        NotificationKind.DirectMessage -> "$who sent you a message"
        NotificationKind.FollowRequest -> "$who requested to follow you"
        NotificationKind.FollowRequestAccepted -> "$who accepted your follow request"
        NotificationKind.MissedCall ->
            if (actorName.isBlank()) "Missed call" else "Missed call from $who"
        is NotificationKind.Unknown -> "You have a new notification"
    }
}

/** How many rows from the end to start fetching the next page. */
private const val LOAD_MORE_THRESHOLD = 3
private const val WEEK_DAYS = 7L

private val UNREAD_DOT = 8.dp
private val ROW_VERTICAL = 12.dp
private val ROW_BADGE_GLYPH = 18.dp
private val REQUEST_ICON_CIRCLE = 36.dp

/** The 36dp avatar plus its 2dp ring and 2dp gap on each side. */
private val AVATAR_SLOT = 44.dp
private val HEADER_LINK_SIZE = 13.sp
private val SECTION_SIZE = 14.sp
private val PANEL_TITLE_SIZE = 14.sp
private val PANEL_SUBTITLE_SIZE = 12.sp

/** The red-tinted badge fill behind a missed call. */
@Suppress("MagicNumber")
private val MISSED_CALL_BAND = Color(0x1FFF3B30)
