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
import com.us.android.core.chat.data.DisappearTimer
import com.us.android.core.chat.data.MessageHit
import com.us.android.core.chat.data.MessageReportReason
import com.us.android.core.chat.data.SHARED_MEDIA
import com.us.android.core.chat.data.SHARED_POSTS
import com.us.android.core.chat.data.SharedItem
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.common.error.AppError
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
import com.us.android.core.chat.hidden.HiddenChats
import com.us.android.core.chat.hidden.HiddenError
import com.us.android.core.chat.hidden.HiddenResult
import com.us.android.core.chat.hidden.settingsErrorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The own-bubble colours, in our words (`chat_bubble_style`). */
val BUBBLE_STYLES = listOf(
    "default" to "Classic",
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
    /** Hidden chats: the viewer hid this chat (it is open now, or this page would not be showing). */
    val hidden: Boolean = false,
    /** "Hide chat" before the secrets exist: open setup for this chat, once. */
    val setupHiddenFor: String? = null,
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
    private val hiddenChats: HiddenChats,
) : ViewModel() {
    private val conversationId: String = savedStateHandle.get<String>(CONVERSATION_ID_KEY).orEmpty()
    private val _state = MutableStateFlow(
        ChatSettingsUiState(viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()),
    )
    val state: StateFlow<ChatSettingsUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            hiddenChats.tokens.lockedIds.collect { ids -> _state.update { it.copy(hidden = conversationId in ids) } }
        }
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
        // The shared timer, live: another member changing it reaches this page
        // through the thread beneath it (`disappearing_timer_updated`).
        viewModelScope.launch {
            parity.disappearing.map { it[conversationId] }.distinctUntilChanged().collect { timer ->
                if (timer != null) _state.update { s -> s.copy(prefs = s.prefs?.copy(disappearing = timer)) }
            }
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

    /**
     * Disappearing messages, for everyone in the chat. Shown at once and put
     * back if the server refuses; the saved value is what the thread's timer
     * icon then reads.
     */
    fun setDisappearing(timer: DisappearTimer) {
        val current = _state.value.prefs ?: return
        val before = current.disappearing ?: return
        if (timer == before) return
        _state.update { s -> s.copy(prefs = s.prefs?.copy(disappearing = timer)) }
        viewModelScope.launch {
            when (val saved = parity.setDisappearing(conversationId, timer)) {
                is AppResult.Success -> {
                    _state.update { s -> s.copy(prefs = s.prefs?.copy(disappearing = saved.data)) }
                    say(
                        if (saved.data.isOn) "Messages now disappear after ${saved.data.label}." else "Disappearing messages are off.",
                        UsMessageType.Success,
                    )
                }
                is AppResult.Failure -> {
                    _state.update { s -> s.copy(prefs = s.prefs?.copy(disappearing = before)) }
                    say(disappearingRefusal(saved.error), UsMessageType.Error)
                }
            }
        }
    }

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
     * Report the chat (fix pass 2): the conversation report route — a DM
     * reports the person with their recent messages as evidence, a group
     * reports the group; the server picks the evidence. A server that has
     * not shipped the route yet (404) gets the old path: the person's newest
     * message, reported on its own.
     */
    fun report(reason: MessageReportReason, note: String?) {
        viewModelScope.launch {
            val result = reportChat(
                whole = { parity.reportConversation(conversationId, reason, note) },
                oldPath = { reportNewestMessage(reason, note) },
            ) ?: return@launch
            when (result) {
                is AppResult.Success -> say("Thanks — we'll review it. They won't know it was you.", UsMessageType.Success)
                is AppResult.Failure -> say("Couldn't send the report.", UsMessageType.Error)
            }
        }
    }

    /** The pre-route fallback; null (and the person told) when they sent nothing to report. */
    private suspend fun reportNewestMessage(reason: MessageReportReason, note: String?): AppResult<Unit>? {
        val viewer = _state.value.viewerId
        val latest = (repository.messages(conversationId) as? AppResult.Success)?.data?.items
            ?.firstOrNull { it.senderId != viewer && !it.deleted }
        if (latest == null) {
            say("There's nothing from them to report yet.", UsMessageType.Info)
            return null
        }
        return parity.report(conversationId, latest.id, reason, note)
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

    /** Hide chat (24 Oct 2026): it leaves Messages; the page and the thread close. */
    fun hideChat() = viewModelScope.launch {
        when (val result = hiddenChats.hide(conversationId)) {
            is HiddenResult.Ok -> _state.update { it.copy(closed = true) }
            is HiddenResult.Err -> if (result.error == HiddenError.NotConfigured) {
                _state.update { it.copy(setupHiddenFor = conversationId) }
            } else {
                say(settingsErrorMessage(result.error), UsMessageType.Error)
            }
        }
    }

    /** Unhide, from inside the open hidden chat: it is back in Messages. */
    fun unhideChat() = viewModelScope.launch {
        when (val result = hiddenChats.unhide(conversationId)) {
            is HiddenResult.Ok -> say("Chat unhidden. It's back in Messages.", UsMessageType.Success)
            is HiddenResult.Err -> say(settingsErrorMessage(result.error), UsMessageType.Error)
        }
    }

    fun consumeHiddenSetup() = _state.update { it.copy(setupHiddenFor = null) }

    private fun say(text: String, type: UsMessageType) = _state.update { it.copy(message = UsMessage(text, type)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    internal companion object {
        const val CONVERSATION_ID_KEY = "conversationId"
    }
}

private enum class SettingsSheet { BUBBLE, READ_TICK, TONE, DISAPPEARING, REPORT, BLOCK, CLEAR, DELETE, SEARCH }

@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod")
fun ChatSettingsScreen(
    onBack: () -> Unit,
    onClosed: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    onOpenThread: () -> Unit,
    onOpenSharedPost: (postId: String) -> Unit,
    /** Hidden chats: "Hide chat" before setup opens the setup page for this chat. */
    onSetUpHiddenChats: (conversationId: String) -> Unit = {},
    viewModel: ChatSettingsViewModel = hiltViewModel(),
    postCards: PostCardsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val cards by postCards.cards.collectAsStateWithLifecycle()
    var sheet by remember { mutableStateOf<SettingsSheet?>(null) }
    var tab by remember { mutableStateOf(0) }
    val context = androidx.compose.ui.platform.LocalContext.current
    val readTick = ReadTickColourPrefs.current()
    LaunchedEffect(state.closed) { if (state.closed) onClosed() }
    LaunchedEffect(state.setupHiddenFor) {
        state.setupHiddenFor?.let { id ->
            viewModel.consumeHiddenSetup()
            onSetUpHiddenChats(id)
        }
    }
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
                        // Message ticks (2026-10-22): a local, every-chat preference.
                        UsSettingsLinkRow(
                            title = "Read tick colour",
                            value = readTick.label,
                            onClick = { sheet = SettingsSheet.READ_TICK },
                            modifier = Modifier.testTag("chat_settings_read_tick"),
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
                            // Only once the server said what the timer is: an
                            // inbox-row fallback does not know it.
                            prefs.disappearing?.let { timer ->
                                UsSettingsLinkRow(
                                    title = "Disappearing messages",
                                    value = timer.label,
                                    onClick = { sheet = SettingsSheet.DISAPPEARING },
                                    modifier = Modifier.testTag("chat_settings_disappearing"),
                                )
                            }
                        }
                    }
                }
                item(key = "privacy") {
                    UsSettingsSection("Privacy and support") {
                        UsSettingsLinkRow(title = "Report", onClick = { sheet = SettingsSheet.REPORT })
                        if (state.peerId.isNotBlank()) {
                            UsSettingsLinkRow(title = "Block", onClick = { sheet = SettingsSheet.BLOCK })
                        }
                        // Hidden chats: hide it from Messages, or (open hidden chat) bring it back.
                        if (state.hidden) {
                            UsSettingsLinkRow(
                                title = "Unhide chat",
                                description = "Show this chat in Messages again",
                                onClick = viewModel::unhideChat,
                                modifier = Modifier.testTag("chat_settings_unhide"),
                            )
                        } else {
                            UsSettingsLinkRow(
                                title = "Hide chat",
                                description = "Only your secret key shows it, and your chat password opens it",
                                onClick = viewModel::hideChat,
                                modifier = Modifier.testTag("chat_settings_hide"),
                            )
                        }
                        UsSettingsLinkRow(title = "Clear chat", onClick = { sheet = SettingsSheet.CLEAR })
                        UsSettingsLinkRow(title = "Delete chat", onClick = { sheet = SettingsSheet.DELETE })
                    }
                }
                // A hidden chat's shared media is resolved by URL routes that
                // carry no open token; it is seen in the chat itself instead.
                if (!state.hidden) item(key = "tabs") {
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
                if (!state.hidden) when {
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
        SettingsSheet.READ_TICK -> ReadTickColourSheet(
            current = readTick,
            onSave = { choice ->
                ReadTickColourPrefs.set(context, choice)
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
        SettingsSheet.DISAPPEARING -> DisappearingSheet(
            current = state.prefs?.disappearing ?: DisappearTimer.OFF,
            onPick = { timer ->
                viewModel.setDisappearing(timer)
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

/** Off / 24 hours / 7 days / 90 days, for everyone in the chat. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DisappearingSheet(current: DisappearTimer, onPick: (DisappearTimer) -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("disappearing_sheet"),
        ) {
            Text("Disappearing messages", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                "New messages are removed for everyone in this chat once the time is up. " +
                    "Messages already sent keep the timer they were sent with.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(vertical = UsTheme.spacing.s),
            )
            DisappearTimer.entries.forEach { timer ->
                UsSettingsRadioRow(
                    title = timer.label,
                    selected = timer == current,
                    onClick = { onPick(timer) },
                    modifier = Modifier.testTag("disappearing_option:${timer.name.lowercase()}"),
                )
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

/**
 * What a refused timer change says. In a group only the owner and admins may
 * change it (403); anything else is a plain failure to save.
 */
internal fun disappearingRefusal(error: AppError): String = when (error) {
    is AppError.Forbidden -> "Only the group's owner or admins can change disappearing messages."
    else -> "Couldn't change disappearing messages. Try again."
}

/**
 * Chat settings' Report (fix pass 2): the conversation report route; only a
 * server without it (404) falls back to the old one-message report. Any
 * other refusal is the answer — it is never retried down the old path.
 * Null when the old path had nothing to report.
 */
internal suspend fun reportChat(
    whole: suspend () -> AppResult<Unit>,
    oldPath: suspend () -> AppResult<Unit>?,
): AppResult<Unit>? = when (val result = whole()) {
    is AppResult.Success -> result
    is AppResult.Failure -> if (result.error.isMissingRoute()) oldPath() else result
}
