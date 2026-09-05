package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.chat.ui.OfflineBanner
import kotlinx.coroutines.launch

/**
 * The one chat screen (founder, 2026-09-05): "a search bar with voice
 * search, then tabs Chats | Groups | Communities | Suggestions".
 *
 * The Momentum header — "Messages" left, the pen (new group) and ≡ right —
 * then a full-width glass search pill with the mic at its right end, then
 * the tab row with the home feed's white underline, then a pager the tabs
 * and a swipe both drive. Typing filters the CURRENT tab locally; on
 * Communities it also asks Discover.
 */
@Composable
fun ChatHomeScreen(
    destinations: ChatHomeDestinations,
    viewModel: ChatHomeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val pagerState = rememberPagerState(initialPage = state.tab.ordinal) { ChatHomeTab.entries.size }
    val scope = rememberCoroutineScope()

    // The pager and the row are one selection: a swipe moves the underline,
    // a tap scrolls the page.
    LaunchedEffect(pagerState.settledPage) {
        viewModel.selectTab(ChatHomeTab.entries[pagerState.settledPage])
    }
    LaunchedEffect(state.pendingOpen) {
        val open = state.pendingOpen ?: return@LaunchedEffect
        viewModel.consumePendingOpen()
        destinations.onOpenThread(open.conversationId, open.title, open.isGroup)
    }

    UsScaffold(
        topBar = { ChatHomeHeader(state = state, destinations = destinations) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize()) {
                ChatSearchPill(
                    query = state.query,
                    placeholder = state.tab.searchPlaceholder(),
                    onQueryChange = viewModel::onQueryChange,
                    onClear = viewModel::clearQuery,
                    onVoiceResults = viewModel::onVoiceResults,
                    onVoiceUnavailable = { viewModel.showMessage(it) },
                )
                ChatHomeTabsRow(
                    selected = state.tab,
                    unread = state.unreadTabs,
                    onSelect = { tab -> scope.launch { pagerState.animateScrollToPage(tab.ordinal) } },
                )
                if (state.syncFailed && state.conversations.isEmpty() && state.requests.isEmpty()) {
                    OfflineBanner(onRetry = viewModel::refresh)
                }
                ChatHomePager(
                    pagerState = pagerState,
                    state = state,
                    viewModel = viewModel,
                    destinations = destinations,
                )
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
}

/** The four pages; each composes only while on screen. */
@Composable
private fun ChatHomePager(
    pagerState: PagerState,
    state: ChatHomeUiState,
    viewModel: ChatHomeViewModel,
    destinations: ChatHomeDestinations,
) {
    HorizontalPager(
        state = pagerState,
        modifier = Modifier.fillMaxSize().testTag("chat_home_pager"),
        beyondViewportPageCount = 0,
    ) { page ->
        when (ChatHomeTab.entries[page]) {
            ChatHomeTab.Chats -> ChatsTab(
                state = state,
                onOpenConversation = destinations.onOpenThread,
                onOpenRequests = destinations.onOpenRequests,
                onTogglePin = viewModel::togglePin,
                onToggleMute = viewModel::toggleMute,
            )
            ChatHomeTab.Groups -> GroupsTab(
                state = state,
                destinations = destinations,
                onTogglePin = viewModel::togglePin,
                onToggleMute = viewModel::toggleMute,
            )
            ChatHomeTab.Communities -> CommunitiesTab(
                state = state,
                onCreate = destinations.onCreateCommunity,
                onOpen = destinations.onOpenCommunity,
                onToggleMembership = viewModel::toggleCommunityMembership,
                onLoadMore = viewModel::loadMoreDiscover,
            )
            ChatHomeTab.Suggestions -> SuggestionsTab(
                state = state,
                onFollow = viewModel::followPerson,
                onMessage = viewModel::messagePerson,
                onDismiss = viewModel::dismissPerson,
                onJoinCommunity = viewModel::joinSuggestedCommunity,
                onOpenCommunity = destinations.onOpenCommunity,
            )
        }
    }
}

private fun ChatHomeTab.searchPlaceholder(): String = when (this) {
    ChatHomeTab.Chats -> "Search chats"
    ChatHomeTab.Groups -> "Search groups"
    ChatHomeTab.Communities -> "Search communities"
    ChatHomeTab.Suggestions -> "Search suggestions"
}

/**
 * The header: "Messages" on the left, then the pen (new group) and ≡ on the
 * right. The ≡ holds the doors the old inbox kept in its overflow —
 * Requests with its count, Group invites with theirs, Join with link,
 * Calls and Chat lock. A tab root has no back arrow; the Explore launcher's
 * push keeps one.
 */
@Composable
private fun ChatHomeHeader(state: ChatHomeUiState, destinations: ChatHomeDestinations) {
    val onBack = destinations.onBack
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = UsTheme.spacing.s, end = UsTheme.spacing.s, top = UsTheme.spacing.s)
            .testTag("chat_home_header"),
    ) {
        if (onBack != null) {
            HeaderGlyph(icon = UsIcons.Back, description = "Back", onClick = onBack, tag = "chat_home_back")
        }
        Text(
            text = "Messages",
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .weight(1f)
                .padding(start = if (onBack == null) UsTheme.spacing.l else 0.dp)
                .semantics { heading() },
        )
        HeaderGlyph(
            icon = UsIcons.SquarePen,
            description = "New group",
            onClick = destinations.onCreateGroup,
            tag = "chat_home_new",
        )
        HeaderMenu(state = state, destinations = destinations)
    }
}

/** The ≡ glyph with its pending badge and the menu behind it. */
@Composable
private fun HeaderMenu(state: ChatHomeUiState, destinations: ChatHomeDestinations) {
    var menuOpen by remember { mutableStateOf(false) }
    val pending = state.requestCount + state.invitationCount
    Box {
        HeaderGlyph(
            icon = UsIcons.Menu,
            description = "More" + if (pending > 0) ", $pending pending" else "",
            onClick = { menuOpen = true },
            tag = "chat_home_menu",
        )
        if (pending > 0) PendingBadge(pending = pending, modifier = Modifier.align(Alignment.TopEnd))
        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
            val open: (() -> Unit) -> Unit = { action ->
                menuOpen = false
                action()
            }
            MenuRow("Requests", state.requestCount, "chat_home_requests") { open(destinations.onOpenRequests) }
            MenuRow("Group invites", state.invitationCount, "chat_home_invites") {
                open(destinations.onOpenInvitations)
            }
            MenuRow("Join with link", 0, "chat_home_join_link") { open(destinations.onJoinWithLink) }
            MenuRow("Calls", 0, "chat-call-history") { open(destinations.onOpenCallHistory) }
            MenuRow("Chat lock", 0, "chat-lock-settings") { open(destinations.onOpenLockSettings) }
        }
    }
}

/** Momentum's count badge: a white disc, the count in the deep accent. */
@Composable
private fun PendingBadge(pending: Int, modifier: Modifier = Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .padding(top = BADGE_INSET, end = BADGE_INSET)
            .size(BADGE_SIZE)
            .background(Color.White, CircleShape)
            .testTag("chat_home_pending_badge"),
    ) {
        Text(
            text = if (pending > BADGE_MAX) "$BADGE_MAX+" else "$pending",
            fontSize = BADGE_TEXT,
            lineHeight = BADGE_TEXT,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.accentDeep,
        )
    }
}

@Composable
private fun MenuRow(label: String, count: Int, tag: String, onClick: () -> Unit) {
    DropdownMenuItem(
        text = {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                Text(label)
                if (count > 0) {
                    Text(
                        text = count.toString(),
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.accentSolid,
                    )
                }
            }
        },
        onClick = onClick,
        modifier = Modifier.testTag(tag),
    )
}

/**
 * The search pill: full width, glass, a hairline border, the search glyph,
 * the text, × once there is text, and the mic at the right end. Enter on
 * the keyboard just closes it — the field filters as it is typed.
 */
@Composable
private fun ChatSearchPill(
    query: String,
    placeholder: String,
    onQueryChange: (String) -> Unit,
    onClear: () -> Unit,
    onVoiceResults: (List<String>) -> Unit,
    onVoiceUnavailable: (String) -> Unit,
) {
    val keyboard = LocalSoftwareKeyboardController.current
    val voice = rememberVoiceSearch(onResult = onVoiceResults, onUnavailable = onVoiceUnavailable)
    val shape = RoundedCornerShape(UsTheme.radii.full)
    BasicTextField(
        value = query,
        onValueChange = onQueryChange,
        singleLine = true,
        textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
        cursorBrush = SolidColor(UsTheme.extended.accentSolid),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
        keyboardActions = KeyboardActions(onSearch = { keyboard?.hide() }),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .height(FIELD_HEIGHT)
            .background(UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(start = FIELD_PADDING_START, end = UsTheme.spacing.xs)
            .semantics { contentDescription = placeholder }
            .testTag("chat-search"),
        decorationBox = { inner ->
            SearchPillContent(
                query = query,
                placeholder = placeholder,
                onClear = onClear,
                onVoice = { voice.start() },
                inner = inner,
            )
        },
    )
}

@Composable
private fun SearchPillContent(
    query: String,
    placeholder: String,
    onClear: () -> Unit,
    onVoice: () -> Unit,
    inner: @Composable () -> Unit,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Icon(
            imageVector = UsIcons.Search,
            contentDescription = null,
            tint = UsTheme.extended.textDim,
            modifier = Modifier.size(FIELD_GLYPH),
        )
        Box(modifier = Modifier.weight(1f), contentAlignment = Alignment.CenterStart) {
            if (query.isEmpty()) {
                Text(
                    text = placeholder,
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textDim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            inner()
        }
        if (query.isNotEmpty()) {
            HeaderGlyph(
                icon = UsIcons.Close,
                description = "Clear",
                onClick = onClear,
                tag = "chat_search_clear",
                size = SMALL_TARGET,
                glyph = SMALL_GLYPH,
            )
        }
        HeaderGlyph(
            icon = UsIcons.Mic,
            description = "Search by voice",
            onClick = onVoice,
            tag = "chat_search_voice",
            size = SMALL_TARGET,
            glyph = MIC_GLYPH,
        )
    }
}

private val FIELD_HEIGHT = 46.dp
private val FIELD_PADDING_START = 16.dp
private val FIELD_GLYPH = 20.dp
private val SMALL_TARGET = 36.dp
private val SMALL_GLYPH = 18.dp
private val MIC_GLYPH = 20.dp
private val HAIRLINE = 1.dp
private val BADGE_INSET = 4.dp
private val BADGE_SIZE = 16.dp
private val BADGE_TEXT = 9.sp
private const val BADGE_MAX = 99
