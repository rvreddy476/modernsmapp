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
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
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

/**
 * The notification inbox — Slice D.
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
                    UsSecondaryButton(
                        text = "Mark all read",
                        onClick = viewModel::markAllRead,
                    )
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
                    detail = "Reactions, comments and follows will show up here.",
                    modifier = Modifier.fillMaxWidth(),
                )

                else -> SectionedNotificationList(
                    items = state.items,
                    isLoadingMore = state.isLoadingMore,
                    listState = listState,
                    onOpen = { notification ->
                        viewModel.onNotificationOpened(notification)
                        // NOT gated on the mark-read request. Making someone
                        // wait for a write they can already see succeed is the
                        // wrong trade.
                        if (notification.target != NotificationTarget.None) {
                            onOpenTarget(notification.target)
                        }
                    },
                )
            }
        }
    }
}

/**
 * TODAY / EARLIER sections, per the Figma notifications frame (4:854). The
 * list arrives newest-first, so a single partition keeps that order within
 * each section.
 */
@Composable
private fun SectionedNotificationList(
    items: List<Notification>,
    isLoadingMore: Boolean,
    listState: androidx.compose.foundation.lazy.LazyListState,
    onOpen: (Notification) -> Unit,
) {
    val (today, earlier) = items.partition { isToday(it.createdAt) }
    LazyColumn(
        state = listState,
        modifier = Modifier.fillMaxSize(),
    ) {
        if (today.isNotEmpty()) {
            item(key = "section-today") { SectionHeader("Today") }
        }
        items(today, key = { it.id }) { notification ->
            NotificationRow(notification = notification, onClick = { onOpen(notification) })
        }
        if (earlier.isNotEmpty()) {
            item(key = "section-earlier") { SectionHeader("Earlier") }
        }
        items(earlier, key = { it.id }) { notification ->
            NotificationRow(notification = notification, onClick = { onOpen(notification) })
        }

        if (isLoadingMore) {
            item { LoadingBlock() }
        }
    }
}

/** Section label — the muted TODAY/EARLIER dividers from the Figma frame. */
@Composable
private fun SectionHeader(label: String) {
    Text(
        text = label.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.padding(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.m,
        ),
    )
}

/** Whether an ISO timestamp falls on the device's current calendar day. */
private fun isToday(isoInstant: String): Boolean = runCatching {
    java.time.Instant.parse(isoInstant)
        .atZone(java.time.ZoneId.systemDefault())
        .toLocalDate() == java.time.LocalDate.now()
}.getOrDefault(false)

// Figma notifications row (4:854): actor avatar, sentence + relative time,
// unread dot on the trailing edge. A missed call sits on a red-tinted band
// with a phone badge instead of an avatar — the one row type whose absence
// costs the user something, so it must not look like one more like.
@Composable
private fun NotificationRow(notification: Notification, onClick: () -> Unit) {
    val text = notification.describe()
    val missedCall = notification.kind == NotificationKind.MissedCall

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .then(
                if (missedCall) Modifier.background(MISSED_CALL_BAND) else Modifier,
            )
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.l,
            )
            // ONE node to a screen reader, carrying the sentence and the
            // unread state. Without merging, the dot and the text are read as
            // two unrelated items and "unread" arrives detached from what is
            // unread.
            .clearAndSetSemantics {
                contentDescription = if (notification.isRead) text else "Unread. $text"
            },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        if (missedCall) {
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .size(ROW_BADGE)
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
        } else {
            // No name to render yet (actor hydration is Slice D follow-up),
            // but the seed keeps one stable colour per actor.
            UsAvatar(name = "", size = UsAvatarSize.Medium, seed = notification.actorUserId)
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = text,
                style = MaterialTheme.typography.bodyMedium,
                color = if (missedCall) UsTheme.extended.liveRed else UsTheme.extended.textPrimary,
            )
            Text(
                text = formatRelativeTime(notification.createdAt),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        // The unread mark is a dot, not bold text: weight is already used for
        // the sentence and a second meaning for it would be ambiguous.
        Box(
            modifier = Modifier
                .size(UNREAD_DOT)
                .clip(CircleShape)
                .then(
                    if (notification.isRead) {
                        Modifier
                    } else {
                        Modifier.background(MaterialTheme.colorScheme.primary)
                    },
                ),
        )
    }
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
 * The sentence a row shows.
 *
 * Deliberately actor-less for now: resolving display names would mean a profile
 * lookup per row, and an inbox that fires N requests on open is worse than one
 * that says "Someone". Names are tracked as Slice D follow-up work, not faked
 * here with an id.
 *
 * [NotificationKind.Unknown] renders a generic line rather than being dropped:
 * one notification service serves every vertical in this super-app, so this
 * client WILL receive types it has no screen for, and a silently missing row is
 * worse than a vague one.
 */
internal fun Notification.describe(): String = when (kind) {
    NotificationKind.Reaction -> "Someone reacted to your post"
    NotificationKind.Comment -> "Someone commented on your post"
    NotificationKind.CommentReaction -> "Someone reacted to your comment"
    NotificationKind.Follow -> "You have a new follower"
    NotificationKind.Mention -> "You were mentioned in a post"
    NotificationKind.Repost -> "Someone reposted your post"
    NotificationKind.ConnectionRequest -> "You have a new connection request"
    NotificationKind.ConnectionAccepted -> "Your connection request was accepted"
    NotificationKind.NewSubscriber -> "You have a new subscriber"
    NotificationKind.MissedCall -> "Missed call"
    is NotificationKind.Unknown -> "You have a new notification"
}

/** How many rows from the end to start fetching the next page. */
private const val LOAD_MORE_THRESHOLD = 3

private val UNREAD_DOT = 8.dp
private val ROW_BADGE = 44.dp
private val ROW_BADGE_GLYPH = 20.dp

/** The red-tinted band and badge fill behind a missed call. */
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
