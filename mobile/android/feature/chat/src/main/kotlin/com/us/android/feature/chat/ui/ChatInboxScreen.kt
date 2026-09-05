package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Badge
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.Conversation
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState

/**
 * The chat inbox: Chats, Requests and group Invites (directive §5.5).
 *
 * Rows render from the durable cache, so the list appears instantly and
 * offline; a sync failure keeps the rows and shows a quiet banner instead of
 * blanking someone's inbox.
 */
@Suppress("LongParameterList")
@Composable
fun ChatInboxScreen(
    onOpenConversation: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onCreateGroup: () -> Unit,
    onOpenLockSettings: () -> Unit = {},
    onOpenCallHistory: () -> Unit = {},
    onBack: (() -> Unit)? = null,
    viewModel: ChatInboxViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    val onChatsTab = state.tab == InboxTab.Chats
    UsScaffold(
        topBar = {
            InboxHeader(
                state = state,
                onBack = if (onChatsTab) onBack else ({ viewModel.selectTab(InboxTab.Chats) }),
                onOpenRequests = { viewModel.selectTab(InboxTab.Requests) },
                onOpenCallHistory = onOpenCallHistory,
                onOpenLockSettings = onOpenLockSettings,
                onCreateGroup = onCreateGroup,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            val nothingCached = state.conversations.isEmpty() && state.requests.isEmpty()
            if (state.syncFailed && nothingCached && state.tab != InboxTab.Invites) {
                OfflineBanner(onRetry = viewModel::refresh)
            }

            when (state.tab) {
                InboxTab.Chats -> ChatsTab(
                    state = state,
                    onQueryChange = viewModel::onQueryChange,
                    onOpenConversation = onOpenConversation,
                    onTogglePin = viewModel::togglePin,
                    onToggleMute = viewModel::toggleMute,
                )
                InboxTab.Requests, InboxTab.Invites -> RequestsAndInvites(
                    state = state,
                    onSelect = viewModel::selectTab,
                    onOpenRequest = onOpenRequest,
                    onAccept = viewModel::acceptInvitation,
                    onDecline = viewModel::declineInvitation,
                )
            }
        }
    }
}

/**
 * The inbox header, per the Figma messages frame (98:21): a big left title
 * and ONE icon — user-plus with the pending count, the door to requests and
 * invites. Calls, chat lock and new-group survive in the overflow rather
 * than as three text buttons fighting the title.
 */
@Suppress("LongParameterList")
@Composable
private fun InboxHeader(
    state: InboxUiState,
    onBack: (() -> Unit)?,
    onOpenRequests: () -> Unit,
    onOpenCallHistory: () -> Unit,
    onOpenLockSettings: () -> Unit,
    onCreateGroup: () -> Unit,
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.m,
                vertical = UsTheme.spacing.s,
            ),
    ) {
        // The Messages TAB has no arrow; the Requests view keeps one back to Chats.
        if (onBack != null) {
            IconButton(onClick = onBack) {
                Icon(
                    imageVector = UsIcons.Back,
                    contentDescription = "Back",
                    tint = UsTheme.extended.textPrimary,
                )
            }
        }
        Text(
            text = if (state.tab == InboxTab.Chats) "Messages" else "Requests",
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        if (state.tab == InboxTab.Chats) {
            val pending = state.requestCount + state.inviteCount
            Box {
                IconButton(
                    onClick = onOpenRequests,
                    modifier = Modifier.testTag("chat-tab-requests"),
                ) {
                    Icon(
                        imageVector = UsIcons.UserPlus,
                        contentDescription = "Requests and invites" +
                            if (pending > 0) ", $pending pending" else "",
                        tint = UsTheme.extended.textPrimary,
                    )
                }
                if (pending > 0) {
                    Badge(
                        containerColor = UsTheme.extended.chatAccent,
                        modifier = Modifier.align(Alignment.TopEnd),
                    ) { Text(pending.toString()) }
                }
            }
            InboxOverflowMenu(
                open = menuOpen,
                onOpenChange = { menuOpen = it },
                onOpenCallHistory = onOpenCallHistory,
                onOpenLockSettings = onOpenLockSettings,
                onCreateGroup = onCreateGroup,
            )
        }
    }
}

@Composable
private fun InboxOverflowMenu(
    open: Boolean,
    onOpenChange: (Boolean) -> Unit,
    onOpenCallHistory: () -> Unit,
    onOpenLockSettings: () -> Unit,
    onCreateGroup: () -> Unit,
) {
    Box {
        IconButton(onClick = { onOpenChange(true) }) {
            Icon(
                imageVector = UsIcons.More,
                contentDescription = "More options",
                tint = UsTheme.extended.textPrimary,
            )
        }
        DropdownMenu(expanded = open, onDismissRequest = { onOpenChange(false) }) {
            DropdownMenuItem(
                text = { Text("Calls") },
                onClick = {
                    onOpenChange(false)
                    onOpenCallHistory()
                },
                modifier = Modifier.testTag("chat-call-history"),
            )
            DropdownMenuItem(
                text = { Text("Chat lock") },
                onClick = {
                    onOpenChange(false)
                    onOpenLockSettings()
                },
                modifier = Modifier.testTag("chat-lock-settings"),
            )
            DropdownMenuItem(
                text = { Text("New group") },
                onClick = {
                    onOpenChange(false)
                    onCreateGroup()
                },
                modifier = Modifier.testTag("chat-new-group"),
            )
        }
    }
}

/**
 * Requests and invites behind the user-plus — one surface, segmented, per
 * the design's dedicated Requests screen (98:132).
 */
@Composable
private fun RequestsAndInvites(
    state: InboxUiState,
    onSelect: (InboxTab) -> Unit,
    onOpenRequest: (String, String) -> Unit,
    onAccept: (String) -> Unit,
    onDecline: (String) -> Unit,
) {
    Column {
        // The segmented bar from the Figma requests screen (98:151): white
        // for the selected segment with a short indicator, muted otherwise —
        // monochrome on purpose, no accent colour fighting the content.
        Row(modifier = Modifier.fillMaxWidth()) {
            SegmentTab(
                label = "Messages",
                count = state.requestCount,
                selected = state.tab == InboxTab.Requests,
                onClick = { onSelect(InboxTab.Requests) },
                modifier = Modifier.weight(1f),
            )
            SegmentTab(
                label = "Group invites",
                count = state.inviteCount,
                selected = state.tab == InboxTab.Invites,
                onClick = { onSelect(InboxTab.Invites) },
                modifier = Modifier
                    .weight(1f)
                    .testTag("chat-tab-invites"),
            )
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(SEGMENT_HAIRLINE)
                .background(UsTheme.extended.borderSubtle),
        )
        when (state.tab) {
            InboxTab.Requests -> RequestsTab(state, onOpenRequest)
            else -> InvitesTab(state = state, onAccept = onAccept, onDecline = onDecline)
        }
    }
}

@Composable
private fun SegmentTab(
    label: String,
    count: Int,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = modifier
            .clickable(onClick = onClick)
            .padding(top = UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = label,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = if (selected) FontWeight.Bold else FontWeight.SemiBold,
                color = if (selected) {
                    UsTheme.extended.textPrimary
                } else {
                    UsTheme.extended.textMuted
                },
            )
            if (count > 0) {
                Badge(containerColor = UsTheme.extended.chatAccent) {
                    Text(count.toString())
                }
            }
        }
        Box(
            modifier = Modifier
                .padding(top = UsTheme.spacing.m)
                .size(width = SEGMENT_INDICATOR_WIDTH, height = SEGMENT_INDICATOR_HEIGHT)
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .background(
                    if (selected) UsTheme.extended.textPrimary else Color.Transparent,
                ),
        )
    }
}

@Composable
internal fun OfflineBanner(onRetry: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Couldn't refresh. Showing what's saved on this device.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onRetry) { Text("Retry") }
    }
}

@Composable
private fun ChatsTab(
    state: InboxUiState,
    onQueryChange: (String) -> Unit,
    onOpenConversation: (String, String, Boolean) -> Unit,
    onTogglePin: (Conversation) -> Unit,
    onToggleMute: (Conversation) -> Unit,
) {
    Column {
        SearchPill(query = state.query, onQueryChange = onQueryChange)

        when {
            state.loading && state.conversations.isEmpty() ->
                UsLoadingState(label = "Loading conversations")

            state.conversations.isEmpty() -> UsEmptyState(
                title = "No conversations yet",
                detail = "Messages from people you can chat with show up here.",
            )

            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                verticalArrangement = Arrangement.spacedBy(ROW_GAP),
            ) {
                val online = state.conversations.filter { conversation ->
                    conversation.type != "group" && conversation.members.any {
                        it.userId != state.viewerId && it.userId in state.onlineUserIds
                    }
                }
                if (online.isNotEmpty() && state.query.isBlank()) {
                    item(key = "online-now") {
                        OnlineNowRail(
                            online = online,
                            viewerId = state.viewerId,
                            onOpen = onOpenConversation,
                        )
                    }
                }
                item(key = "recent-label") { SectionLabel("Recent chats") }
                items(state.visibleConversations, key = { it.id }) { conversation ->
                    val title = conversation.displayTitle(state.viewerId)
                    ConversationRow(
                        conversation = conversation,
                        title = title,
                        viewerId = state.viewerId,
                        online = conversation.type != "group" && conversation.members.any {
                            it.userId != state.viewerId && it.userId in state.onlineUserIds
                        },
                        onClick = {
                            onOpenConversation(conversation.id, title, conversation.type == "group")
                        },
                        onTogglePin = { onTogglePin(conversation) },
                        onToggleMute = { onToggleMute(conversation) },
                    )
                }
            }
        }
    }
}

/** The search pill (98:31): filters the cached rows as you type. */
@Composable
private fun SearchPill(query: String, onQueryChange: (String) -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            )
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(UsTheme.extended.glassBg)
            .padding(
                horizontal = UsTheme.spacing.xl,
                vertical = UsTheme.spacing.l,
            ),
    ) {
        Icon(
            imageVector = UsIcons.Search,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(SEARCH_GLYPH),
        )
        Box(modifier = Modifier.weight(1f)) {
            if (query.isEmpty()) {
                Text(
                    text = "Search messages…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
            }
            BasicTextField(
                value = query,
                onValueChange = onQueryChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = UsTheme.extended.textPrimary,
                ),
                cursorBrush = SolidColor(UsTheme.extended.chatAccent),
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Search messages" }
                    .testTag("chat-search"),
            )
        }
    }
}

/** ONLINE NOW (98:36): green-ringed avatars of people online right now. */
@Composable
internal fun OnlineNowRail(
    online: List<Conversation>,
    viewerId: String,
    onOpen: (String, String, Boolean) -> Unit,
) {
    Column {
        SectionLabel("Online now")
        LazyRow(
            contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            items(online, key = { it.id }) { conversation ->
                val title = conversation.displayTitle(viewerId)
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                    modifier = Modifier
                        .clip(RoundedCornerShape(UsTheme.radii.medium))
                        .clickable { onOpen(conversation.id, title, false) }
                        .padding(UsTheme.spacing.xs),
                ) {
                    Box(
                        modifier = Modifier
                            .border(
                                width = ONLINE_RING,
                                color = UsTheme.extended.chatOnline,
                                shape = CircleShape,
                            )
                            .padding(ONLINE_RING_GAP),
                    ) {
                        UsAvatar(name = title, size = UsAvatarSize.Medium, seed = conversation.id)
                    }
                    Text(
                        text = title,
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.textMuted,
                        maxLines = 1,
                    )
                }
            }
        }
    }
}

@Composable
private fun SectionLabel(label: String) {
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

@Composable
private fun RequestsTab(
    state: InboxUiState,
    onOpenRequest: (String, String) -> Unit,
) {
    if (state.requests.isEmpty()) {
        UsEmptyState(
            title = "No message requests",
            detail = "When someone outside your connections messages you, it waits here for your decision.",
        )
        return
    }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(state.requests, key = { it.id }) { request ->
            val title = request.displayTitle(state.viewerId)
            ConversationRow(
                conversation = request,
                title = title,
                viewerId = state.viewerId,
                onClick = { onOpenRequest(request.id, title) },
            )
        }
    }
}

@Composable
private fun InvitesTab(
    state: InboxUiState,
    onAccept: (String) -> Unit,
    onDecline: (String) -> Unit,
) {
    if (state.invitations.isEmpty()) {
        UsEmptyState(
            title = "No group invites",
            detail = "Groups that need your consent to join appear here.",
        )
        return
    }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(state.invitations, key = { it.id }) { invitation ->
            InvitationRow(
                busy = invitation.id in state.busyInvitationIds,
                onAccept = { onAccept(invitation.id) },
                onDecline = { onDecline(invitation.id) },
            )
        }
    }
}

@Composable
internal fun InvitationRow(
    busy: Boolean,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.m,
            ),
    ) {
        Text(
            "You've been invited to a group",
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            "Joining shares your messages in this group with its members.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.size(UsTheme.spacing.s))
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(
                text = "Decline",
                onClick = onDecline,
                enabled = !busy,
                modifier = Modifier.weight(1f),
            )
            com.us.android.core.designsystem.component.UsButton(
                text = if (busy) "Working…" else "Join group",
                onClick = onAccept,
                enabled = !busy,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
@Suppress("LongMethod")
internal fun ConversationRow(
    conversation: Conversation,
    title: String,
    viewerId: String,
    onClick: () -> Unit,
    online: Boolean = false,
    onTogglePin: () -> Unit = {},
    onToggleMute: () -> Unit = {},
) {
    var menuOpen by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = ROW_MARGIN)
            .clip(RoundedCornerShape(ROW_CORNER))
            // A CARD, and only when the row wants something (136:144).
            //
            // The frame raises exactly one row onto #1A1A1A. Our conversation
            // payload carries `has_unread` and nothing finer, so the raise is
            // keyed on that: the rows needing attention lift off the page and
            // the settled ones lie flat. Keying it on list position instead
            // would leave the top row highlighted after everything was read.
            .background(if (conversation.hasUnread) UsTheme.extended.bgCardSolid else Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = { menuOpen = true })
            .padding(ROW_PADDING),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_PADDING),
    ) {
        Box {
            UsAvatar(name = title, size = UsAvatarSize.Chat, seed = conversation.id)
            if (online) {
                Box(
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .size(ONLINE_DOT)
                        .clip(CircleShape)
                        .background(UsTheme.extended.chatOnline)
                        .border(ONLINE_DOT_RING, UsTheme.extended.bgCanvas, CircleShape),
                )
            }
        }
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(NAME_PREVIEW_GAP),
        ) {
            val markers = buildString {
                if (conversation.isPinned) append("📌 ")
                if (conversation.isMuted) append("🔕 ")
            }
            NameAndTimeLine(
                name = markers + title,
                timeIso = conversation.lastMessageAt ?: conversation.updatedAt,
                unread = conversation.hasUnread,
            )
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                DropdownMenuItem(
                    text = { Text(if (conversation.isPinned) "Unpin" else "Pin") },
                    onClick = {
                        menuOpen = false
                        onTogglePin()
                    },
                )
                DropdownMenuItem(
                    text = { Text(if (conversation.isMuted) "Unmute" else "Mute") },
                    onClick = {
                        menuOpen = false
                        onToggleMute()
                    },
                )
            }
            ConversationPreview(conversation = conversation, viewerId = viewerId)
        }
        if (conversation.hasUnread) {
            // The badge's SIZE, COLOUR and PLACE are the design's (136:153);
            // the number is not, because there is no number to show. The
            // conversation payload carries `has_unread` and no count — the
            // server never sends one — so the circle stays empty rather than
            // displaying a figure the client made up.
            Box(
                modifier = Modifier
                    .size(UNREAD_BADGE)
                    .clip(CircleShape)
                    .background(UsTheme.extended.chatAccent)
                    .testTag("chat-unread-dot"),
            )
        }
    }
}

/**
 * The second line: what was last said, and by whom when that is not obvious.
 *
 * A group prefixes the sender in white against the muted body (136:162) —
 * in a direct thread the sender is the person already named above, so the
 * prefix would be noise. A missed call is the design's one red line (136:193).
 */
@Composable
private fun ConversationPreview(conversation: Conversation, viewerId: String) {
    val body = when {
        conversation.isRequest -> "Message request"
        conversation.lastMessagePreview.isNotBlank() -> conversation.lastMessagePreview
        // A message exists but carries no text — that is a media send, whose
        // preview the server leaves empty. Saying so beats the half-drawn row
        // a blank second line produces.
        conversation.lastMessageAt != null -> "Photo"
        else -> return
    }
    val senderName = conversation.lastMessageSender
        ?.takeIf { conversation.type == "group" && it != viewerId }
        ?.let { id -> conversation.members.firstOrNull { it.userId == id }?.displayName }
        ?.takeIf { it.isNotBlank() }

    val text = buildAnnotatedString {
        if (senderName != null) {
            withStyle(
                SpanStyle(
                    color = UsTheme.extended.textPrimary,
                    fontWeight = FontWeight.Bold,
                ),
            ) { append("$senderName: ") }
        }
        append(body)
    }
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = if (conversation.isMissedCall) MISSED_CALL_RED else UsTheme.extended.textMuted,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
    )
}

/** message-service writes the missed-call marker into the preview itself. */
private val Conversation.isMissedCall: Boolean
    get() = lastMessagePreview.startsWith("Missed", ignoreCase = true)

/**
 * Figma inbox row (98:65): bold name with the relative time on the trailing
 * edge — chat green while the row is unread, so the freshest thing on screen
 * is also the loudest.
 */
@Composable
private fun NameAndTimeLine(name: String, timeIso: String, unread: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = name,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .weight(1f)
                .padding(end = UsTheme.spacing.s),
        )
        val time = formatRelativeTime(timeIso)
        if (time.isNotBlank()) {
            Text(
                text = time,
                style = MaterialTheme.typography.labelSmall,
                // Chat green while unread, muted once read — the same accent
                // the badge uses, so the two halves of "needs you" agree.
                color = if (unread) UsTheme.extended.chatAccent else UsTheme.extended.textMuted,
                maxLines = 1,
            )
        }
    }
}

// ── The Figma messages-inbox frame (136:86) ────────────────────────────

/** The design's one red line: a call that was missed (136:193). */
@Suppress("MagicNumber")
private val MISSED_CALL_RED = Color(0xFFEF5350)

private val ROW_MARGIN = 12.dp
private val ROW_PADDING = 12.dp
private val ROW_CORNER = 16.dp
private val ROW_GAP = 8.dp
private val NAME_PREVIEW_GAP = 2.dp
private val UNREAD_BADGE = 18.dp
private val ONLINE_DOT_RING = 2.dp
private val ONLINE_DOT = 12.dp
private val SEGMENT_INDICATOR_WIDTH = 60.dp
private val SEGMENT_INDICATOR_HEIGHT = 2.dp
private val SEGMENT_HAIRLINE = 1.dp
private val ONLINE_RING = 2.dp
private val ONLINE_RING_GAP = 2.dp
private val SEARCH_GLYPH = 20.dp
