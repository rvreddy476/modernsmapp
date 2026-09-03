package com.us.android.feature.notifications.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
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
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
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
 * The notification inbox — Slice D, redesigned per the Figma notifications
 * frame (157:138): a plain list with the actor's name in bold, the time
 * inline and muted, and the one action a row can take sitting ON the row.
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
        topBar = { NotificationsTopBar(onBack = onBack, onOpenPreferences = onOpenPreferences) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            // Ask for the notification permission HERE — the user has just
            // said, by opening this screen, that they care about
            // notifications. See NotificationPermissionPrompt for why not at
            // first launch. Renders nothing unless there is something to say.
            permissionPrompt()

            if (state.hasUnread) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
                    horizontalArrangement = Arrangement.End,
                ) {
                    UsSecondaryButton(text = "Mark all read", onClick = viewModel::markAllRead)
                }
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
 * Three age bands, per the Figma frame: today's rows sit at the top with no
 * heading, then "Last 30 days", then "Older". The list arrives newest-first,
 * so grouping preserves the order within each band.
 */
private enum class AgeBand(val label: String?) { Today(null), Month("Last 30 days"), Older("Older") }

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
                band.label?.let { label -> item(key = "section-${band.name}") { SectionHeader(label) } }
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

/** Section label — the bold "Last 30 days" / "Older" dividers from the frame. */
@Composable
private fun SectionHeader(label: String) {
    Text(
        text = label,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
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
        age < Duration.ofDays(1) -> AgeBand.Today
        age < Duration.ofDays(MONTH_DAYS) -> AgeBand.Month
        else -> AgeBand.Older
    }
}

// Figma notifications row (157:138): 48dp avatar, one line of text with the
// actor in bold and the time inline and muted, the row's action trailing.
// A missed call keeps its red phone badge — the one row type whose absence
// costs the user something, so it must not look like one more like.
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

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { callbacks.onOpen(notification, target) }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = ROW_VERTICAL),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        if (missedCall) {
            MissedCallBadge()
        } else {
            UsAvatar(
                name = notification.actorName,
                size = UsAvatarSize.Chat,
                seed = notification.actorUserId,
            )
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
        outcome != null -> Text(
            text = outcome,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(top = UsTheme.spacing.xs),
        )

        requestPending -> {
            val busy = action == RowActionState.Busy
            Row(
                modifier = Modifier.padding(top = UsTheme.spacing.s),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                RowButton("Accept", filled = true, busy = busy) { callbacks.onAccept(notification) }
                RowButton("Decline", filled = false, busy = busy) { callbacks.onDecline(notification) }
                RowButton("Block", filled = false, busy = busy) { callbacks.onBlock(notification) }
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
        Text(
            text = outcome,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(top = UsTheme.spacing.xs),
        )
    } else {
        val busy = action == RowActionState.Busy
        Row(
            modifier = Modifier.padding(top = UsTheme.spacing.s),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            RowButton("Accept", filled = true, busy = busy) { callbacks.onAcceptFollow(notification) }
            RowButton("Decline", filled = false, busy = busy) { callbacks.onDeclineFollow(notification) }
        }
    }
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
        RowButton("Following", filled = false, busy = false, enabled = false) {}
    } else {
        RowButton("Follow", filled = true, busy = action == RowActionState.Busy) { onFollow(notification) }
    }
}

/**
 * The 32dp row button from the frame. Filled is the accent green — the
 * founder's call over the frame's orange — outlined is the neutral choice.
 */
@Composable
private fun RowButton(
    text: String,
    filled: Boolean,
    busy: Boolean,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    val shape = RoundedCornerShape(BUTTON_RADIUS)
    val modifier = Modifier.height(BUTTON_HEIGHT)
    val padding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = 0.dp)
    if (filled) {
        Button(
            onClick = onClick,
            modifier = modifier,
            enabled = enabled && !busy,
            shape = shape,
            contentPadding = padding,
            colors = ButtonDefaults.buttonColors(
                containerColor = UsTheme.extended.chatAccent,
                contentColor = Color.White,
                disabledContainerColor = UsTheme.extended.chatAccent.copy(alpha = DISABLED_ALPHA),
                disabledContentColor = Color.White,
            ),
        ) { ButtonLabel(text, busy) }
    } else {
        OutlinedButton(
            onClick = onClick,
            modifier = modifier,
            enabled = enabled && !busy,
            shape = shape,
            contentPadding = padding,
            border = BorderStroke(1.dp, UsTheme.extended.borderMedium),
            colors = ButtonDefaults.outlinedButtonColors(
                contentColor = UsTheme.extended.textPrimary,
                disabledContentColor = UsTheme.extended.textMuted,
            ),
        ) { ButtonLabel(text, busy) }
    }
}

@Composable
private fun ButtonLabel(text: String, busy: Boolean) {
    if (busy) {
        CircularProgressIndicator(modifier = Modifier.size(BUTTON_SPINNER), strokeWidth = 2.dp)
    } else {
        Text(text = text, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
private fun MissedCallBadge() {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(UsAvatarSize.Chat.diameter)
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

/** The unread mark is a dot, not bold text: weight already means "the actor". */
@Composable
private fun UnreadDot(isRead: Boolean) {
    Box(
        modifier = Modifier
            .size(UNREAD_DOT)
            .clip(CircleShape)
            .then(if (isRead) Modifier else Modifier.background(UsTheme.extended.chatAccent)),
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
        withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(who) }
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
private const val MONTH_DAYS = 30L
private const val DISABLED_ALPHA = 0.6f

private val UNREAD_DOT = 8.dp
private val ROW_VERTICAL = 12.dp
private val ROW_BADGE_GLYPH = 20.dp
private val BUTTON_HEIGHT = 32.dp
private val BUTTON_RADIUS = 8.dp
private val BUTTON_SPINNER = 16.dp

/** The red-tinted badge fill behind a missed call. */
@Suppress("MagicNumber")
private val MISSED_CALL_BAND = Color(0x1FFF3B30)

/**
 * The inbox top bar.
 *
 * Carries the route to notification PREFERENCES — Slice D, D-D7. They live in
 * `:feature:profile` and were previously reachable only through Settings; the
 * inbox is where someone actually forms the thought "this is too many
 * notifications", so the control belongs here too.
 */
@Composable
private fun NotificationsTopBar(onBack: () -> Unit, onOpenPreferences: () -> Unit) {
    UsTopBar(
        title = "Notifications",
        onBack = onBack,
        actions = {
            IconButton(onClick = onOpenPreferences) {
                Icon(
                    imageVector = UsIcons.Settings,
                    contentDescription = "Notification settings",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        },
    )
}
