package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import coil3.compose.AsyncImage
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.AlertTone
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationPrefs
import com.us.android.core.chat.data.MessageHit
import com.us.android.core.chat.data.MessageReportReason
import com.us.android.core.chat.data.SHARED_MEDIA
import com.us.android.core.chat.data.SHARED_POSTS
import com.us.android.core.chat.data.SharedItem
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.PrivacySettingsRepository
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsRadioRow
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSwitchRow
import com.us.android.feature.chat.ui.home.rememberMediaUrl
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The own-bubble colours, in our words (`chat_bubble_style`). */
val BUBBLE_STYLES = listOf(
    "default" to "Mint",
    "ocean" to "Ocean",
    "ember" to "Ember",
    "forest" to "Forest",
    "violet" to "Violet",
    "mono" to "Graphite",
)

data class ChatSettingsUiState(
    val loading: Boolean = true,
    val conversation: Conversation? = null,
    val viewerId: String = "",
    val prefs: ConversationPrefs? = null,
    val bubbleStyle: String? = null,
    val sharedPosts: List<SharedItem>? = null,
    val sharedMedia: List<SharedItem>? = null,
    val searchResults: List<MessageHit>? = null,
    val busy: Boolean = false,
    val message: UsMessage? = null,
    /** Set once the chat was deleted or the person blocked: the page and the thread both close. */
    val closed: Boolean = false,
) {
    val peerId: String
        get() = conversation?.peer?.userId
            ?: conversation?.members?.firstOrNull { it.userId != viewerId }?.userId.orEmpty()
}

/**
 * The chat settings page behind the thread's ⋯ (CONTRACT §9, direct chats).
 * Every switch writes through the server and shows its echo.
 */
@HiltViewModel
class ChatSettingsViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val parity: ChatParityRepository,
    private val store: ChatStore,
    private val privacy: PrivacySettingsRepository,
    private val profiles: ProfileRepository,
    session: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {
    private val conversationId: String = savedStateHandle.get<String>(CONVERSATION_ID_KEY).orEmpty()
    private val _state = MutableStateFlow(
        ChatSettingsUiState(viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()),
    )
    val state: StateFlow<ChatSettingsUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val conversation = (repository.conversation(conversationId) as? AppResult.Success)?.data
            val prefs = (parity.prefs(conversationId) as? AppResult.Success)?.data
                ?: conversation?.let { ConversationPrefs(it.isMuted, it.isPinned, it.muteCalls, AlertTone.fromWire(it.alertTone)) }
            _state.update { it.copy(loading = false, conversation = conversation, prefs = prefs) }
        }
        viewModelScope.launch {
            val settings = privacy.get()
            if (settings is AppResult.Success) _state.update { it.copy(bubbleStyle = settings.data.chatBubbleStyle) }
        }
        viewModelScope.launch {
            val posts = parity.shared(conversationId, SHARED_POSTS)
            _state.update { it.copy(sharedPosts = (posts as? AppResult.Success)?.data?.items.orEmpty()) }
        }
        viewModelScope.launch {
            val media = parity.shared(conversationId, SHARED_MEDIA)
            _state.update { it.copy(sharedMedia = (media as? AppResult.Success)?.data?.items.orEmpty()) }
        }
    }

    fun setMuted(on: Boolean) = savePrefs { it.copy(isMuted = on) }
    fun setMuteCalls(on: Boolean) = savePrefs { it.copy(muteCalls = on) }
    fun setPinned(on: Boolean) = savePrefs { it.copy(isPinned = on) }
    fun setTone(tone: AlertTone) = savePrefs { it.copy(alertTone = tone) }

    private fun savePrefs(edit: (ConversationPrefs) -> ConversationPrefs) {
        val current = _state.value.prefs ?: return
        val next = edit(current)
        _state.update { it.copy(prefs = next) }
        viewModelScope.launch {
            when (val saved = parity.savePrefs(conversationId, next)) {
                is AppResult.Success -> {
                    _state.update { it.copy(prefs = saved.data) }
                    store.applyPrefsLocally(conversationId, saved.data)
                }
                is AppResult.Failure -> {
                    _state.update { it.copy(prefs = current) }
                    say("Couldn't save that change.", UsMessageType.Error)
                }
            }
        }
    }

    /** Bubble colour is GLOBAL (a user setting), not per chat. */
    fun saveBubbleStyle(style: String) {
        val previous = _state.value.bubbleStyle
        _state.update { it.copy(bubbleStyle = style) }
        viewModelScope.launch {
            val saved = when (val current = privacy.get()) {
                is AppResult.Success -> privacy.save(current.data.copy(chatBubbleStyle = style))
                is AppResult.Failure -> current
            }
            when (saved) {
                is AppResult.Success -> {
                    _state.update { it.copy(bubbleStyle = saved.data.chatBubbleStyle ?: style) }
                    say("Bubble colour saved.", UsMessageType.Success)
                }
                is AppResult.Failure -> {
                    _state.update { it.copy(bubbleStyle = previous) }
                    say("Couldn't save your bubble colour.", UsMessageType.Error)
                }
            }
        }
    }

    fun search(query: String) {
        val needle = query.trim()
        if (needle.length < 2) {
            _state.update { it.copy(searchResults = null) }
            return
        }
        viewModelScope.launch {
            val result = parity.search(needle)
            _state.update { s ->
                s.copy(
                    searchResults = (result as? AppResult.Success)?.data?.messages
                        ?.filter { it.conversationId == conversationId }.orEmpty(),
                )
            }
        }
    }

    /**
     * Report the chat: the person's newest message is what gets reviewed
     * (CONTRACT §2.6 reports messages; there is no conversation report).
     */
    fun report(reason: MessageReportReason, note: String?) {
        val viewer = _state.value.viewerId
        viewModelScope.launch {
            val latest = (repository.messages(conversationId) as? AppResult.Success)?.data?.items
                ?.firstOrNull { it.senderId != viewer && !it.deleted }
            if (latest == null) {
                say("There's nothing from them to report yet.", UsMessageType.Info)
                return@launch
            }
            when (parity.report(conversationId, latest.id, reason, note)) {
                is AppResult.Success -> say("Thanks — we'll review it. They won't know it was you.", UsMessageType.Success)
                is AppResult.Failure -> say("Couldn't send the report.", UsMessageType.Error)
            }
        }
    }

    fun block() {
        val peer = _state.value.peerId.takeIf { it.isNotBlank() } ?: return
        viewModelScope.launch {
            when (profiles.block(peer)) {
                is AppResult.Success -> _state.update { it.copy(closed = true) }
                is AppResult.Failure -> say("Couldn't block them. Try again.", UsMessageType.Error)
            }
        }
    }

    fun clearChat() = viewModelScope.launch {
        when (val result = parity.clear(conversationId)) {
            is AppResult.Success -> {
                store.clearConversationLocally(conversationId)
                say("Chat cleared.", UsMessageType.Success)
            }
            is AppResult.Failure -> say(
                if (result.error.isMissingRoute()) "Clearing chats isn't available yet." else "Couldn't clear the chat.",
                UsMessageType.Error,
            )
        }
    }

    fun deleteChat() = viewModelScope.launch {
        when (val result = parity.deleteForMe(conversationId)) {
            is AppResult.Success -> {
                store.removeConversationLocally(conversationId)
                _state.update { it.copy(closed = true) }
            }
            is AppResult.Failure -> say(
                if (result.error.isMissingRoute()) "Deleting chats isn't available yet." else "Couldn't delete the chat.",
                UsMessageType.Error,
            )
        }
    }

    private fun say(text: String, type: UsMessageType) = _state.update { it.copy(message = UsMessage(text, type)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    internal companion object {
        const val CONVERSATION_ID_KEY = "conversationId"
    }
}

private enum class SettingsSheet { BUBBLE, TONE, REPORT, BLOCK, CLEAR, DELETE, SEARCH }

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun ChatSettingsScreen(
    onBack: () -> Unit,
    onClosed: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    onOpenThread: () -> Unit,
    onOpenSharedPost: (postId: String) -> Unit,
    viewModel: ChatSettingsViewModel = hiltViewModel(),
    postCards: PostCardsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val cards by postCards.cards.collectAsStateWithLifecycle()
    var sheet by remember { mutableStateOf<SettingsSheet?>(null) }
    var tab by remember { mutableStateOf(0) }
    LaunchedEffect(state.closed) { if (state.closed) onClosed() }
    UsScaffold(topBar = { UsTopBar(title = "Chat settings", onBack = onBack) }, applyPageGutter = false) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            val conversation = state.conversation
            val prefs = state.prefs
            LazyColumn(
                modifier = Modifier.fillMaxSize().padding(horizontal = UsTheme.spacing.pageHorizontal).testTag("chat_settings"),
            ) {
                item(key = "head") {
                    val title = conversation?.displayTitle(state.viewerId).orEmpty()
                    Column(
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                        modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l),
                    ) {
                        UsAvatar(
                            name = title,
                            seed = state.peerId.ifBlank { title },
                            size = UsAvatarSize.Large,
                            imageUrl = conversation?.peer?.avatarUrl ?: conversation?.avatarUrl,
                        )
                        Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                        conversation?.peer?.username?.takeIf { it.isNotBlank() }?.let {
                            Text("@$it", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                        }
                    }
                }
                item(key = "general") {
                    UsSettingsSection("Chat") {
                        if (state.peerId.isNotBlank()) {
                            UsSettingsLinkRow(title = "View profile", onClick = { onOpenProfile(state.peerId) })
                        }
                        UsSettingsLinkRow(title = "Search in chat", onClick = { sheet = SettingsSheet.SEARCH })
                        UsSettingsLinkRow(
                            title = "Bubble colour",
                            value = BUBBLE_STYLES.firstOrNull { it.first == (state.bubbleStyle ?: "default") }?.second,
                            onClick = { sheet = SettingsSheet.BUBBLE },
                        )
                        if (prefs != null) {
                            UsSettingsLinkRow(title = "Alert tone", value = prefs.alertTone.label, onClick = { sheet = SettingsSheet.TONE })
                            UsSettingsSwitchRow(title = "Mute messages", checked = prefs.isMuted, onCheckedChange = viewModel::setMuted)
                            UsSettingsSwitchRow(
                                title = "Mute calls",
                                description = "Calls from this chat won't ring. You'll still see them as missed.",
                                checked = prefs.muteCalls,
                                onCheckedChange = viewModel::setMuteCalls,
                            )
                            UsSettingsSwitchRow(title = "Pin to top", checked = prefs.isPinned, onCheckedChange = viewModel::setPinned)
                        }
                    }
                }
                item(key = "privacy") {
                    UsSettingsSection("Privacy and support") {
                        UsSettingsLinkRow(title = "Report", onClick = { sheet = SettingsSheet.REPORT })
                        if (state.peerId.isNotBlank()) {
                            UsSettingsLinkRow(title = "Block", onClick = { sheet = SettingsSheet.BLOCK })
                        }
                        UsSettingsLinkRow(title = "Clear chat", onClick = { sheet = SettingsSheet.CLEAR })
                        UsSettingsLinkRow(title = "Delete chat", onClick = { sheet = SettingsSheet.DELETE })
                    }
                }
                item(key = "tabs") {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                        modifier = Modifier.padding(vertical = UsTheme.spacing.l),
                    ) {
                        listOf("Shared posts", "Media").forEachIndexed { index, label ->
                            Text(
                                label,
                                style = MaterialTheme.typography.titleSmall,
                                fontWeight = if (tab == index) FontWeight.Bold else FontWeight.Normal,
                                color = if (tab == index) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                                modifier = Modifier.clickable { tab = index }.testTag("chat_settings_tab:$index"),
                            )
                        }
                    }
                }
                val items = if (tab == 0) state.sharedPosts else state.sharedMedia
                when {
                    items == null -> item(key = "shared-loading") { Text("Loading…", color = UsTheme.extended.textMuted) }
                    items.isEmpty() -> item(key = "shared-empty") {
                        Text(
                            if (tab == 0) "Posts you share in this chat show here." else "Photos and videos from this chat show here.",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                    else -> items(items.chunked(GRID_COLUMNS), key = { row -> row.first().messageId }) { row ->
                        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs), modifier = Modifier.padding(bottom = UsTheme.spacing.xs)) {
                            row.forEach { item ->
                                Box(modifier = Modifier.weight(1f).aspectRatio(1f)) {
                                    if (tab == 0 && item.postId != null) {
                                        val postId = item.postId.orEmpty()
                                        LaunchedEffect(postId) { postCards.ensure(postId) }
                                        val card = (cards[postId] as? PostCardState.Ready)?.card
                                        AsyncImage(
                                            model = card?.thumbnailUrl,
                                            contentDescription = "Shared post",
                                            contentScale = ContentScale.Crop,
                                            modifier = Modifier
                                                .fillMaxSize()
                                                .background(UsTheme.extended.bgCardSolid, RoundedCornerShape(UsTheme.radii.small))
                                                .clickable { onOpenSharedPost(postId) }
                                                .testTag("chat_settings_post:$postId"),
                                        )
                                    } else {
                                        val url = rememberMediaUrl(item.mediaId)
                                        AsyncImage(
                                            model = url,
                                            contentDescription = "Shared media",
                                            contentScale = ContentScale.Crop,
                                            modifier = Modifier
                                                .fillMaxSize()
                                                .background(UsTheme.extended.bgCardSolid, RoundedCornerShape(UsTheme.radii.small))
                                                .clickable(onClick = onOpenThread),
                                        )
                                    }
                                }
                            }
                            repeat(GRID_COLUMNS - row.size) { Box(modifier = Modifier.weight(1f)) }
                        }
                    }
                }
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
    when (sheet) {
        SettingsSheet.BUBBLE -> BubbleColourSheet(
            current = state.bubbleStyle ?: "default",
            onSave = { style ->
                viewModel.saveBubbleStyle(style)
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.TONE -> ToneSheet(
            current = state.prefs?.alertTone ?: AlertTone.DEFAULT,
            onPick = { tone ->
                viewModel.setTone(tone)
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.REPORT -> ReportSheet(
            sending = false,
            onReport = { reason, note ->
                viewModel.report(reason, note)
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.BLOCK -> ConfirmDialog(
            title = "Block ${state.conversation?.displayTitle(state.viewerId).orEmpty()}?",
            body = "They won't be able to message or call you, or find your profile. They aren't told.",
            confirm = "Block",
            onConfirm = {
                viewModel.block()
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.CLEAR -> ConfirmDialog(
            title = "Clear this chat?",
            body = "Your copy of the messages is cleared. The other person keeps theirs.",
            confirm = "Clear",
            onConfirm = {
                viewModel.clearChat()
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.DELETE -> ConfirmDialog(
            title = "Delete this chat?",
            body = "It leaves your inbox and your copy of the messages is cleared. A new message brings it back.",
            confirm = "Delete",
            onConfirm = {
                viewModel.deleteChat()
                sheet = null
            },
            onDismiss = { sheet = null },
        )
        SettingsSheet.SEARCH -> SearchInChatSheet(
            results = state.searchResults,
            onQuery = viewModel::search,
            onOpen = {
                sheet = null
                onOpenThread()
            },
            onDismiss = { sheet = null },
        )
        null -> Unit
    }
}

/** Bubble colour: a live preview of your bubble in each colour, then Save. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun BubbleColourSheet(current: String, onSave: (String) -> Unit, onDismiss: () -> Unit) {
    var picked by remember { mutableStateOf(current) }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("bubble_colour_sheet"),
        ) {
            Text("Bubble colour", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "The colour of your own messages, in every chat.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            val swatch = UsTheme.extended.chatBubbles.forStyle(picked)
            Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) {
                Text(
                    "This is how your messages look",
                    color = swatch.ink,
                    modifier = Modifier
                        .background(swatch.fill, RoundedCornerShape(PREVIEW_CORNER))
                        .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m)
                        .testTag("bubble_preview"),
                )
            }
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                BUBBLE_STYLES.forEach { (style, label) ->
                    val fill = UsTheme.extended.chatBubbles.forStyle(style).fill
                    Box(
                        modifier = Modifier
                            .size(SWATCH)
                            .background(fill, CircleShape)
                            .border(
                                if (style == picked) SWATCH_RING_ON else SWATCH_RING_OFF,
                                if (style == picked) UsTheme.extended.textPrimary else UsTheme.extended.borderSubtle,
                                CircleShape,
                            )
                            .clickable { picked = style }
                            .semantics { contentDescription = label + if (style == picked) ", selected" else "" }
                            .testTag("bubble_swatch:$style"),
                    )
                }
            }
            UsButton(text = "Save", onClick = { onSave(picked) }, modifier = Modifier.fillMaxWidth().testTag("bubble_save"))
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ToneSheet(current: AlertTone, onPick: (AlertTone) -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("alert_tone_sheet"),
        ) {
            Text("Alert tone", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            AlertTone.entries.forEach { tone ->
                UsSettingsRadioRow(title = tone.label, selected = tone == current, onClick = { onPick(tone) })
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SearchInChatSheet(
    results: List<MessageHit>?,
    onQuery: (String) -> Unit,
    onOpen: () -> Unit,
    onDismiss: () -> Unit,
) {
    var query by remember { mutableStateOf("") }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("search_in_chat"),
        ) {
            UsTextField(
                value = query,
                onValueChange = {
                    query = it
                    onQuery(it)
                },
                label = "Search in chat",
                modifier = Modifier.fillMaxWidth(),
            )
            when {
                results == null -> Text(
                    "Type at least two letters.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(vertical = UsTheme.spacing.m),
                )
                results.isEmpty() -> Text(
                    "No messages match.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(vertical = UsTheme.spacing.m),
                )
                else -> results.forEach { hit ->
                    Column(modifier = Modifier.fillMaxWidth().clickable(onClick = onOpen).padding(vertical = UsTheme.spacing.s)) {
                        Text(hit.snippet, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        Text(formatRelativeTime(hit.at), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
                    }
                }
            }
        }
    }
}

@Composable
private fun ConfirmDialog(title: String, body: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = {
            TextButton(onClick = onConfirm, modifier = Modifier.testTag("confirm_action")) {
                Text(confirm, color = UsTheme.extended.statusDanger)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

private const val GRID_COLUMNS = 3
private val PREVIEW_CORNER = 18.dp
private val SWATCH = 40.dp
private val SWATCH_RING_ON = 3.dp
private val SWATCH_RING_OFF = 1.dp
