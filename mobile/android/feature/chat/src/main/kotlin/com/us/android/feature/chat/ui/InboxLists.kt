package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.LABEL_NONE
import com.us.android.core.chat.data.LANE_ARCHIVED
import com.us.android.core.chat.data.PeopleLookupRepository
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import com.us.android.core.chat.data.SuggestionsRepository
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.chat.data.withoutDatingChats
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.home.ChatSectionLabel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

// ── Archived chats (CONTRACT §1, lane=archived) ──────────────────────────

data class ArchivedUiState(
    val loading: Boolean = true,
    val rows: List<Conversation> = emptyList(),
    val failed: Boolean = false,
    val unavailable: Boolean = false,
    val viewerId: String = "",
    val message: UsMessage? = null,
)

@HiltViewModel
class ArchivedChatsViewModel @Inject constructor(
    private val parity: ChatParityRepository,
    private val store: ChatStore,
    session: SessionStateProvider,
) : ViewModel() {
    private val _state = MutableStateFlow(
        ArchivedUiState(viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()),
    )
    val state: StateFlow<ArchivedUiState> = _state.asStateFlow()

    init { load() }

    fun load() = viewModelScope.launch {
        _state.update { it.copy(loading = true, failed = false) }
        when (val result = parity.lane(LANE_ARCHIVED)) {
            is AppResult.Success -> _state.update { it.copy(loading = false, rows = result.data.items.withoutDatingChats()) }
            is AppResult.Failure -> _state.update {
                it.copy(loading = false, failed = true, unavailable = result.error.isMissingRoute())
            }
        }
    }

    /** Back to the inbox; the next sync brings the row home. */
    fun unarchive(conversation: Conversation) = viewModelScope.launch {
        when (parity.setLabel(conversation, LABEL_NONE)) {
            is AppResult.Success -> {
                _state.update { s -> s.copy(rows = s.rows.filterNot { it.id == conversation.id }) }
                store.syncInbox()
                _state.update { it.copy(message = UsMessage("Moved back to your chats.", UsMessageType.Success)) }
            }
            is AppResult.Failure ->
                _state.update { it.copy(message = UsMessage("Couldn't unarchive that chat.", UsMessageType.Error)) }
        }
    }

    fun delete(conversation: Conversation) = viewModelScope.launch {
        when (parity.deleteForMe(conversation.id)) {
            is AppResult.Success -> {
                store.removeConversationLocally(conversation.id)
                _state.update { s -> s.copy(rows = s.rows.filterNot { it.id == conversation.id }) }
            }
            is AppResult.Failure ->
                _state.update { it.copy(message = UsMessage("Couldn't delete that chat.", UsMessageType.Error)) }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}

@Composable
fun ArchivedChatsScreen(
    onBack: () -> Unit,
    onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    viewModel: ArchivedChatsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "Archived chats", onBack = onBack) }, applyPageGutter = false) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            when {
                state.loading -> UsLoadingState(label = "Loading archived chats")
                state.unavailable -> UsEmptyState(
                    title = "Archive isn't available yet",
                    detail = "Archived chats will show here once your account has them.",
                )
                state.failed -> UsErrorState(message = "We couldn't load archived chats.", onRetry = { viewModel.load() })
                state.rows.isEmpty() -> UsEmptyState(
                    title = "No archived chats",
                    detail = "Chats you archive leave your inbox and wait here. A new message brings one back.",
                )
                else -> LazyColumn(modifier = Modifier.fillMaxSize().testTag("chat_archived")) {
                    items(state.rows, key = { it.id }) { conversation ->
                        val title = conversation.displayTitle(state.viewerId)
                        ConversationRow(
                            conversation = conversation,
                            title = title,
                            viewerId = state.viewerId,
                            onClick = { onOpenThread(conversation.id, title, conversation.type == "group") },
                            actions = InboxRowActions(
                                onTogglePin = {},
                                onToggleMute = {},
                                onArchive = { viewModel.unarchive(conversation) },
                                onDelete = { viewModel.delete(conversation) },
                                archived = true,
                            ),
                        )
                    }
                }
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
}

// ── New message: the people picker (CONTRACT §9) ─────────────────────────

/** One person the picker offers. */
data class PickablePerson(val userId: String, val name: String, val username: String, val avatarUrl: String?)

data class NewMessageUiState(
    val query: String = "",
    val recents: List<PickablePerson> = emptyList(),
    val suggested: List<PickablePerson> = emptyList(),
    val results: List<PickablePerson> = emptyList(),
    val searching: Boolean = false,
    val selected: List<PickablePerson> = emptyList(),
    val groupName: String = "",
    val busy: Boolean = false,
    val message: UsMessage? = null,
    /** Set once a chat is ready; the screen opens it. */
    val opened: PendingThread? = null,
) {
    val isGroup: Boolean get() = selected.size > 1
}

data class PendingThread(val conversationId: String, val title: String, val isGroup: Boolean)

/** Selecting toggles a person; the cap is a group's add limit, not a guess at one. */
fun List<PickablePerson>.toggled(person: PickablePerson, cap: Int = MAX_NEW_MESSAGE_PEOPLE): List<PickablePerson> =
    when {
        any { it.userId == person.userId } -> filterNot { it.userId == person.userId }
        size >= cap -> this
        else -> this + person
    }

@HiltViewModel
class NewMessageViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val store: ChatStore,
    private val people: PeopleLookupRepository,
    private val suggestions: SuggestionsRepository,
    session: SessionStateProvider,
) : ViewModel() {
    private val viewerId = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()
    private val startDirect = StartDirectController(repository)
    private val _state = MutableStateFlow(NewMessageUiState())
    val state: StateFlow<NewMessageUiState> = _state.asStateFlow()
    private var searchJob: Job? = null

    init {
        viewModelScope.launch {
            // Recents: the people of the viewer's direct chats, most recent first.
            val rows = store.conversationsFlow().first()
            val recents = com.us.android.core.chat.data.orderInbox(rows.withoutDatingChats())
                .filter { it.type != "group" && !it.isRequest }
                .mapNotNull { conversation ->
                    val peer = conversation.peer
                    if (peer != null) {
                        PickablePerson(peer.userId, peer.displayName.ifBlank { peer.username }, peer.username, peer.avatarUrl)
                    } else {
                        conversation.members.firstOrNull { it.userId != viewerId }?.let {
                            PickablePerson(it.userId, it.displayName, "", null)
                        }
                    }
                }
                .filter { it.userId.isNotBlank() }
                .distinctBy { it.userId }
                .take(RECENTS)
            _state.update { it.copy(recents = recents) }
        }
        viewModelScope.launch {
            val suggested = (suggestions.people() as? AppResult.Success)?.data.orEmpty()
                .filter { it.displayName.isNotBlank() }
                .map { PickablePerson(it.userId, it.displayName, "", null) }
                .take(RECENTS)
            _state.update { it.copy(suggested = suggested) }
        }
    }

    fun onQueryChange(query: String) {
        _state.update { it.copy(query = query) }
        searchJob?.cancel()
        val needle = query.trim()
        if (needle.length < 2) {
            _state.update { it.copy(results = emptyList(), searching = false) }
            return
        }
        searchJob = viewModelScope.launch {
            delay(SEARCH_DEBOUNCE_MILLIS)
            _state.update { it.copy(searching = true) }
            val found = (people.search(needle) as? AppResult.Success)?.data.orEmpty()
                .filter { it.userId != viewerId }
                .map { PickablePerson(it.userId, it.nameForDisplay, it.username, it.avatarUrl) }
            _state.update { if (it.query.trim() == needle) it.copy(results = found, searching = false) else it }
        }
    }

    fun toggle(person: PickablePerson) = _state.update { it.copy(selected = it.selected.toggled(person)) }

    fun onGroupNameChange(name: String) = _state.update { it.copy(groupName = name.take(GROUP_NAME_MAX)) }

    /** One person: their DM. Several: a new group with them (the server reports who joined). */
    fun start() {
        val current = _state.value
        if (current.busy || current.selected.isEmpty()) return
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            if (!current.isGroup) {
                val person = current.selected.first()
                when (val result = startDirect.open(person.userId)) {
                    is StartDirectResult.Opened -> _state.update {
                        it.copy(busy = false, opened = PendingThread(result.conversation.id, person.name, false))
                    }
                    is StartDirectResult.NotAllowed -> fail("${person.name} doesn't accept messages from you yet.")
                    is StartDirectResult.Failed -> fail("Couldn't start that chat. Try again.")
                }
            } else {
                val title = current.groupName.trim().ifBlank { current.selected.joinToString(", ") { it.name }.take(GROUP_NAME_MAX) }
                when (
                    val result = repository.createGroupGoverned(
                        title,
                        current.selected.map { it.userId },
                        ChatRepository.newIdempotencyKey(),
                    )
                ) {
                    is AppResult.Success -> {
                        store.syncInbox()
                        _state.update { it.copy(busy = false, opened = PendingThread(result.data.id, title, true)) }
                    }
                    is AppResult.Failure -> fail("Couldn't create the group. Try again.")
                }
            }
        }
    }

    private fun fail(text: String) =
        _state.update { it.copy(busy = false, message = UsMessage(text, UsMessageType.Error)) }

    fun consumeOpened() = _state.update { it.copy(opened = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private companion object {
        const val RECENTS = 20
        const val SEARCH_DEBOUNCE_MILLIS = 300L
        const val GROUP_NAME_MAX = 80
    }
}

@Composable
@Suppress("LongMethod")
fun NewMessageScreen(
    onBack: () -> Unit,
    onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    viewModel: NewMessageViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.opened) {
        val opened = state.opened ?: return@LaunchedEffect
        viewModel.consumeOpened()
        onOpenThread(opened.conversationId, opened.title, opened.isGroup)
    }
    UsScaffold(topBar = { UsTopBar(title = "New message", onBack = onBack) }, applyPageGutter = false) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize()) {
                UsTextField(
                    value = state.query,
                    onValueChange = viewModel::onQueryChange,
                    label = "To",
                    placeholder = "Search people",
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = UsTheme.spacing.pageHorizontal)
                        .testTag("new_message_search"),
                )
                if (state.selected.isNotEmpty()) {
                    LazyRow(
                        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
                        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                        modifier = Modifier.testTag("new_message_selected"),
                    ) {
                        items(state.selected, key = { it.userId }) { person ->
                            Column(
                                horizontalAlignment = Alignment.CenterHorizontally,
                                modifier = Modifier.clickable { viewModel.toggle(person) },
                            ) {
                                UsAvatar(name = person.name, seed = person.userId, size = UsAvatarSize.Medium, imageUrl = person.avatarUrl)
                                Text(person.name, style = MaterialTheme.typography.labelSmall, maxLines = 1)
                            }
                        }
                    }
                }
                if (state.isGroup) {
                    UsTextField(
                        value = state.groupName,
                        onValueChange = viewModel::onGroupNameChange,
                        label = "Group name (optional)",
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = UsTheme.spacing.pageHorizontal)
                            .testTag("new_message_group_name"),
                    )
                }
                LazyColumn(modifier = Modifier.weight(1f).testTag("new_message_people")) {
                    val selectedIds = state.selected.map { it.userId }.toSet()
                    val sections = if (state.query.trim().length >= 2) {
                        listOf((if (state.searching) "Searching…" else "Results") to state.results)
                    } else {
                        listOf("Recent" to state.recents, "Suggested" to state.suggested.filterNot { s ->
                            state.recents.any { it.userId == s.userId }
                        })
                    }
                    sections.forEach { (label, people) ->
                        if (people.isNotEmpty() || label == "Searching…") {
                            item(key = "label:$label") { ChatSectionLabel(label) }
                        }
                        items(people, key = { "$label:" + it.userId }) { person ->
                            PickRow(person = person, selected = person.userId in selectedIds) { viewModel.toggle(person) }
                        }
                    }
                }
                UsButton(
                    text = when {
                        state.busy -> "Opening…"
                        state.isGroup -> "Create group chat"
                        else -> "Chat"
                    },
                    onClick = viewModel::start,
                    enabled = state.selected.isNotEmpty() && !state.busy,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(UsTheme.spacing.pageHorizontal)
                        .testTag("new_message_start"),
                )
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
}

@Composable
private fun PickRow(person: PickablePerson, selected: Boolean, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .semantics {
                role = Role.Checkbox
                this.selected = selected
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s)
            .testTag("new_message_person:${person.userId}"),
    ) {
        UsAvatar(name = person.name, seed = person.userId, size = UsAvatarSize.Medium, imageUrl = person.avatarUrl)
        Column(modifier = Modifier.weight(1f)) {
            Text(person.name, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, color = UsTheme.extended.textPrimary)
            if (person.username.isNotBlank()) {
                Text("@${person.username}", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CHECK)
                .background(if (selected) UsTheme.extended.chatAccent else UsTheme.extended.bgCanvas, CircleShape)
                .border(HAIRLINE, if (selected) UsTheme.extended.chatAccent else UsTheme.extended.borderMedium, CircleShape),
        ) {
            if (selected) {
                Icon(UsIcons.Check, contentDescription = null, tint = UsTheme.extended.onChatAccent, modifier = Modifier.size(CHECK_GLYPH))
            }
        }
    }
}

/** A new group's people at once: the picker's cap. */
const val MAX_NEW_MESSAGE_PEOPLE = 31

private val HAIRLINE = 1.dp
private val CHECK = 22.dp
private val CHECK_GLYPH = 14.dp
