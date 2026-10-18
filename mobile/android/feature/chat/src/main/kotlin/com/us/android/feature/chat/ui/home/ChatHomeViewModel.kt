package com.us.android.feature.chat.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSearchResults
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.LABEL_ARCHIVED
import com.us.android.core.chat.data.Notes
import com.us.android.core.chat.data.OthersNote
import com.us.android.core.chat.data.PresenceInfo
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.chat.data.orderInbox
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.CommunitySuggestion
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationSettings
import com.us.android.core.chat.data.PersonSuggestion
import com.us.android.core.chat.data.StartDirectController
import com.us.android.core.chat.data.StartDirectResult
import com.us.android.core.chat.data.SuggestionAction
import com.us.android.core.chat.data.SuggestionsRepository
import com.us.android.core.chat.data.withoutDatingChats
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A conversation the screen should open, handed over once. */
data class PendingOpen(val conversationId: String, val title: String, val isGroup: Boolean)

/** Everything the one chat screen renders. */
data class ChatHomeUiState(
    val tab: ChatHomeTab = ChatHomeTab.Chats,
    val query: String = "",
    val viewerId: String = "",
    val conversations: List<Conversation> = emptyList(),
    val requests: List<Conversation> = emptyList(),
    val invitationCount: Int = 0,
    val loading: Boolean = false,
    val syncFailed: Boolean = false,
    val onlineUserIds: Set<String> = emptySet(),
    /** Group id → signed avatar URL from the latest sync. */
    val groupAvatarUrls: Map<String, String> = emptyMap(),
    val myCommunities: List<Community> = emptyList(),
    val discover: List<Community> = emptyList(),
    val discoverCursor: String? = null,
    val discoverLoading: Boolean = false,
    val communitiesLoaded: Boolean = false,
    val busyCommunityIds: Set<String> = emptySet(),
    val people: List<PersonSuggestion> = emptyList(),
    val suggestedCommunities: List<CommunitySuggestion> = emptyList(),
    val suggestionsLoaded: Boolean = false,
    val followEdges: Map<String, FollowStatus> = emptyMap(),
    val busyPeopleIds: Set<String> = emptySet(),
    val message: UsMessage? = null,
    val pendingOpen: PendingOpen? = null,
    // ── Chat parity (2026-10-18) ────────────────────────────────────────
    /** Notes for the row above the chats; null until read, or when the route is not there (row hidden). */
    val notes: Notes? = null,
    val noteSaving: Boolean = false,
    /** Server search for the Chats tab (q ≥ 2); null = not asked, or fell back to the local filter. */
    val search: ChatSearchResults? = null,
    val searching: Boolean = false,
    /** `who_can_see_online_status`, for the header's activity dot; null until read (dot hidden). */
    val activityStatus: String? = null,
    /** Presence details per user (online + last active), from `POST /v1/chat/presence`. */
    val presence: Map<String, PresenceInfo> = emptyMap(),
) {
    /**
     * The rows Messages may show. A Pulse match's chat lives in the dating
     * feature (founder, 2026-10-18); [com.us.android.core.chat.data.ChatStore]
     * already keeps it out of the cache, and every list and dot here reads
     * through this as well, whatever fed the state.
     */
    private val inbox: List<Conversation> get() = conversations.withoutDatingChats()
    private val inboxRequests: List<Conversation> get() = requests.withoutDatingChats()

    val visibleChats: List<Conversation> get() = orderInbox(ChatHomeFilters.directChats(inbox, query, viewerId))
    val visibleGroups: List<Conversation> get() = orderInbox(ChatHomeFilters.groups(inbox, query))
    val visibleMyCommunities: List<Community> get() = ChatHomeFilters.communities(myCommunities, query)
    val visibleDiscover: List<Community>
        get() = ChatHomeFilters.communities(discover, query).filter { row -> myCommunities.none { it.id == row.id } }
    val visiblePeople: List<PersonSuggestion> get() = ChatHomeFilters.people(people, query)
    val visibleSuggestedCommunities: List<CommunitySuggestion>
        get() = ChatHomeFilters.suggestedCommunities(suggestedCommunities, query)

    /** The tabs wearing an unread dot: Chats for direct, Groups for groups. */
    val unreadTabs: Set<ChatHomeTab>
        get() = buildSet {
            if (inbox.any { it.type != ChatHomeFilters.GROUP_TYPE && it.hasUnread } || inboxRequests.isNotEmpty()) {
                add(ChatHomeTab.Chats)
            }
            if (inbox.any { it.type == ChatHomeFilters.GROUP_TYPE && it.hasUnread } || invitationCount > 0) {
                add(ChatHomeTab.Groups)
            }
        }
    val requestCount: Int get() = inboxRequests.size
}

/**
 * The one chat screen (founder, 2026-09-05): Chats and Groups from the
 * durable inbox cache, Communities from `/my` + `/discover`, Suggestions
 * from the engine. The inbox half is the old inbox's behaviour unchanged —
 * rows from Room first, reconciled on every refresh; the two new tabs load
 * lazily the first time they are opened and stay for the screen's life.
 */
@HiltViewModel
class ChatHomeViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val store: ChatStore,
    private val session: ChatSessionManager,
    private val sources: ChatHomeSources,
    authRepository: AuthRepository,
) : ViewModel() {

    private val communities: CommunityRepository get() = sources.communities
    private val suggestions: SuggestionsRepository get() = sources.suggestions
    private val followGraph: FollowGraph get() = sources.followGraph
    private val profiles: ProfileRepository get() = sources.profiles

    private val _state = MutableStateFlow(
        ChatHomeUiState(
            viewerId = (authRepository.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty(),
        ),
    )
    val state: StateFlow<ChatHomeUiState> = _state.asStateFlow()

    private val tracker = SuggestionsTracker()
    private val startDirect = StartDirectController(repository)
    private var discoverJob: Job? = null

    private val parity get() = sources.parity
    private var searchJob: Job? = null

    init {
        session.start()
        store.scheduleDrain()
        observeCache()
        observePresence()
        refresh()
        loadActivityStatus()
    }

    fun selectTab(tab: ChatHomeTab) {
        _state.update { it.copy(tab = tab) }
        when (tab) {
            ChatHomeTab.Communities -> if (!_state.value.communitiesLoaded) loadCommunities()
            ChatHomeTab.Suggestions -> if (!_state.value.suggestionsLoaded) loadSuggestions()
            ChatHomeTab.Chats, ChatHomeTab.Groups -> Unit
        }
    }

    fun onQueryChange(query: String) {
        _state.update { it.copy(query = query) }
        if (_state.value.tab == ChatHomeTab.Communities) searchDiscover(query)
        if (_state.value.tab == ChatHomeTab.Chats) searchChats(query)
    }

    /**
     * CONTRACT §6: people, chats and messages from `GET /v1/chat/search`
     * once two characters are typed. The local filter keeps drawing the
     * chats it can match meanwhile, and stays the whole answer when the
     * route is missing or fails.
     */
    private fun searchChats(query: String) {
        searchJob?.cancel()
        val needle = query.trim()
        if (needle.length < SEARCH_MIN_CHARS) {
            _state.update { it.copy(search = null, searching = false) }
            return
        }
        searchJob = viewModelScope.launch {
            delay(SEARCH_DEBOUNCE_MILLIS)
            _state.update { it.copy(searching = true) }
            val result = parity.search(needle)
            _state.update { current ->
                if (current.query.trim() != needle) return@update current
                current.copy(searching = false, search = (result as? AppResult.Success)?.data)
            }
        }
    }

    fun onVoiceResults(results: List<String>) {
        onQueryChange(ChatHomeFilters.applyVoiceResult(_state.value.query, results))
    }

    fun clearQuery() = onQueryChange("")

    fun showMessage(text: String, type: UsMessageType = UsMessageType.Info) =
        _state.update { it.copy(message = UsMessage(text, type)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun consumePendingOpen() = _state.update { it.copy(pendingOpen = null) }

    fun refresh() {
        _state.update { it.copy(loading = true) }
        viewModelScope.launch {
            val ok = store.syncInbox()
            val invitations = repository.invitations()
            _state.update { current ->
                current.copy(
                    loading = false,
                    syncFailed = !ok,
                    invitationCount = (invitations as? AppResult.Success)?.data?.size ?: current.invitationCount,
                )
            }
            refreshPresence()
            loadNotes()
        }
        if (_state.value.communitiesLoaded) loadCommunities()
        if (_state.value.suggestionsLoaded) loadSuggestions()
    }

    // ── Chats & Groups ──────────────────────────────────────────────────

    fun togglePin(conversation: Conversation) = viewModelScope.launch {
        store.setConversationSettings(
            conversation.id,
            ConversationSettings(isMuted = conversation.isMuted, isPinned = !conversation.isPinned),
        )
    }

    fun toggleMute(conversation: Conversation) = viewModelScope.launch {
        store.setConversationSettings(
            conversation.id,
            ConversationSettings(isMuted = !conversation.isMuted, isPinned = conversation.isPinned),
        )
    }

    private suspend fun refreshPresence() {
        val viewerId = _state.value.viewerId
        val peers = _state.value.conversations
            .filter { it.type != ChatHomeFilters.GROUP_TYPE }
            .flatMap { conversation -> conversation.members.map { it.userId } }
            .filter { it.isNotBlank() && it != viewerId }
            .distinct()
            .take(PRESENCE_LIMIT)
        if (peers.isEmpty()) return
        // Chat parity: the detailed read (online + last active) first; the
        // old boolean map is the fallback for a server without it.
        when (val detailed = parity.presence(peers)) {
            is AppResult.Success -> _state.update {
                it.copy(
                    presence = detailed.data,
                    onlineUserIds = detailed.data.filterValues { info -> info.isOnline }.keys,
                )
            }
            is AppResult.Failure -> {
                val online = repository.bulkPresence(peers)
                if (online is AppResult.Success) _state.update { it.copy(onlineUserIds = online.data) }
            }
        }
    }

    /** `presence_update` frames move the online dots without a refresh. */
    private fun observePresence() = viewModelScope.launch {
        session.events.collect { event ->
            if (event is ChatSocketEvent.PresenceUpdate) {
                _state.update { current ->
                    current.copy(
                        presence = current.presence + (event.userId to PresenceInfo(event.isOnline, event.lastActiveAt)),
                        onlineUserIds = if (event.isOnline) {
                            current.onlineUserIds + event.userId
                        } else {
                            current.onlineUserIds - event.userId
                        },
                    )
                }
            }
        }
    }

    // ── Chat parity: row actions (CONTRACT §1.1-§1.3) ───────────────────

    /** Everything a long-press on [conversation] offers. */
    fun rowActions(conversation: Conversation) = com.us.android.feature.chat.ui.InboxRowActions(
        onTogglePin = { togglePin(conversation) },
        onToggleMute = { toggleMute(conversation) },
        onArchive = { archive(conversation) },
        onDelete = { deleteForMe(conversation) },
        onMarkRead = { markRead(conversation) },
        onMarkUnread = { markUnread(conversation) },
    )

    /** Archive: the row leaves the inbox now; the server keeps it under Archived. */
    fun archive(conversation: Conversation) = viewModelScope.launch {
        when (val result = parity.setLabel(conversation, LABEL_ARCHIVED)) {
            is AppResult.Success -> {
                store.removeConversationLocally(conversation.id)
                showMessage("Chat archived.", UsMessageType.Success)
            }
            is AppResult.Failure -> showMessage(
                if (result.error.isMissingRoute()) "Archiving isn't available yet." else "Couldn't archive that chat.",
                UsMessageType.Error,
            )
        }
    }

    /** Delete chat: hidden for me and my history cleared; a new message brings it back. */
    fun deleteForMe(conversation: Conversation) = viewModelScope.launch {
        when (val result = parity.deleteForMe(conversation.id)) {
            is AppResult.Success -> {
                store.removeConversationLocally(conversation.id)
                showMessage("Chat deleted.", UsMessageType.Success)
            }
            is AppResult.Failure -> showMessage(
                if (result.error.isMissingRoute()) "Deleting chats isn't available yet." else "Couldn't delete that chat.",
                UsMessageType.Error,
            )
        }
    }

    /** Mark read from the inbox: the server's read cursor moves to the newest message. */
    fun markRead(conversation: Conversation) = viewModelScope.launch {
        store.markReadLocally(conversation.id)
        val newest = (repository.messages(conversation.id, limit = 1) as? AppResult.Success)?.data?.items?.firstOrNull()
        if (newest != null) repository.markRead(conversation.id, newest.id)
    }

    /** Mark unread is this device's reminder only — the server has no unread write. */
    fun markUnread(conversation: Conversation) = viewModelScope.launch { store.markUnreadLocally(conversation.id) }

    // ── Chat parity: activity status (CONTRACT §4, §9) ──────────────────

    private fun loadActivityStatus() = viewModelScope.launch {
        val settings = sources.privacy.get()
        if (settings is AppResult.Success) _state.update { it.copy(activityStatus = settings.data.whoCanSeeOnlineStatus) }
    }

    /** The header's dot sheet: who sees when you're active. The server's echo is what shows. */
    fun setActivityStatus(value: String) {
        val previous = _state.value.activityStatus ?: return
        if (value == previous) return
        _state.update { it.copy(activityStatus = value) }
        viewModelScope.launch {
            val saved = when (val current = sources.privacy.get()) {
                is AppResult.Success -> sources.privacy.save(current.data.copy(whoCanSeeOnlineStatus = value))
                is AppResult.Failure -> current
            }
            when (saved) {
                is AppResult.Success -> _state.update { it.copy(activityStatus = saved.data.whoCanSeeOnlineStatus) }
                is AppResult.Failure -> {
                    _state.update { it.copy(activityStatus = previous) }
                    showMessage("Couldn't change your activity status.", UsMessageType.Error)
                }
            }
        }
    }

    // ── Chat parity: notes (CONTRACT §5) ────────────────────────────────

    private suspend fun loadNotes() {
        when (val result = parity.notes()) {
            is AppResult.Success -> _state.update { it.copy(notes = result.data) }
            // Missing route: the row stays hidden. Any other failure keeps what was shown.
            is AppResult.Failure -> if (result.error.isMissingRoute()) _state.update { it.copy(notes = null) }
        }
    }

    fun saveNote(text: String, audience: String) {
        val trimmed = text.trim()
        if (trimmed.isEmpty() || _state.value.noteSaving) return
        _state.update { it.copy(noteSaving = true) }
        viewModelScope.launch {
            when (val result = parity.putNote(trimmed, audience)) {
                is AppResult.Success -> {
                    _state.update { it.copy(noteSaving = false, notes = (it.notes ?: Notes(null, emptyList())).copy(mine = result.data)) }
                    showMessage("Note shared for 24 hours.", UsMessageType.Success)
                }
                is AppResult.Failure -> {
                    _state.update { it.copy(noteSaving = false) }
                    showMessage("Couldn't share your note.", UsMessageType.Error)
                }
            }
        }
    }

    fun deleteNote() = viewModelScope.launch {
        when (parity.deleteNote()) {
            is AppResult.Success -> _state.update { it.copy(notes = it.notes?.copy(mine = null)) }
            is AppResult.Failure -> showMessage("Couldn't remove your note.", UsMessageType.Error)
        }
    }

    /**
     * Reply to someone's note: the DM with them (the server decides which,
     * or refuses), a text carrying `reply_note_id` through the durable
     * outbox, then the thread opens.
     */
    fun replyToNote(note: OthersNote, text: String) {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) return
        viewModelScope.launch {
            when (val opened = startDirect.open(note.userId)) {
                is StartDirectResult.Opened -> {
                    val queued = store.enqueueTypedSend(opened.conversation.id, TypedSend.noteReply(trimmed, note.note.id))
                    if (queued == null) {
                        showMessage("Chat is busy. Try again in a moment.", UsMessageType.Warning)
                    } else {
                        _state.update {
                            it.copy(pendingOpen = PendingOpen(opened.conversation.id, note.displayName, isGroup = false))
                        }
                    }
                }
                is StartDirectResult.NotAllowed ->
                    showMessage("${note.displayName.ifBlank { "They" }} can't receive messages from you yet.", UsMessageType.Warning)
                is StartDirectResult.Failed -> showMessage("Couldn't send your reply. Try again.", UsMessageType.Error)
            }
        }
    }

    /** A person from search: their DM, or the server's refusal. */
    fun openPerson(userId: String, name: String) {
        viewModelScope.launch {
            when (val result = startDirect.open(userId)) {
                is StartDirectResult.Opened -> _state.update {
                    it.copy(pendingOpen = PendingOpen(result.conversation.id, name, isGroup = false))
                }
                is StartDirectResult.NotAllowed -> showMessage("$name doesn't accept messages from you yet.", UsMessageType.Warning)
                is StartDirectResult.Failed -> showMessage("Couldn't start that chat. Try again.", UsMessageType.Error)
            }
        }
    }

    private fun observeCache() {
        viewModelScope.launch {
            store.conversationsFlow().collect { rows -> _state.update { it.copy(conversations = rows) } }
        }
        viewModelScope.launch {
            store.requestsFlow().collect { rows -> _state.update { it.copy(requests = rows) } }
        }
        viewModelScope.launch {
            store.groupAvatarUrls.collect { urls -> _state.update { it.copy(groupAvatarUrls = urls) } }
        }
        viewModelScope.launch {
            followGraph.edges.collect { edges -> _state.update { it.copy(followEdges = edges) } }
        }
    }

    // ── Communities ─────────────────────────────────────────────────────

    private fun loadCommunities() = viewModelScope.launch {
        _state.update { it.copy(discoverLoading = true) }
        val mine = async { communities.mine() }
        val discover = async { communities.discover(query = _state.value.query) }
        val mineResult = mine.await()
        val discoverResult = discover.await()
        _state.update { current ->
            current.copy(
                communitiesLoaded = true,
                discoverLoading = false,
                myCommunities = (mineResult as? AppResult.Success)?.data ?: current.myCommunities,
                discover = (discoverResult as? AppResult.Success)?.data?.items ?: current.discover,
                discoverCursor = (discoverResult as? AppResult.Success)?.data?.nextCursor,
            )
        }
        if (mineResult is AppResult.Failure && discoverResult is AppResult.Failure) {
            showMessage("Couldn't load communities.", UsMessageType.Error)
        }
    }

    /** Discover asks the server with the typed text, debounced past the keystrokes. */
    private fun searchDiscover(query: String) {
        discoverJob?.cancel()
        discoverJob = viewModelScope.launch {
            delay(DISCOVER_DEBOUNCE_MILLIS)
            _state.update { it.copy(discoverLoading = true) }
            when (val page = communities.discover(query = query)) {
                is AppResult.Success -> _state.update {
                    it.copy(discover = page.data.items, discoverCursor = page.data.nextCursor, discoverLoading = false)
                }
                is AppResult.Failure -> _state.update { it.copy(discoverLoading = false) }
            }
        }
    }

    fun loadMoreDiscover() {
        val current = _state.value
        val cursor = current.discoverCursor ?: return
        if (current.discoverLoading) return
        _state.update { it.copy(discoverLoading = true) }
        viewModelScope.launch {
            when (val page = communities.discover(query = current.query, cursor = cursor)) {
                is AppResult.Success -> _state.update {
                    it.copy(
                        discover = (it.discover + page.data.items).distinctBy { row -> row.id },
                        discoverCursor = page.data.nextCursor,
                        discoverLoading = false,
                    )
                }
                is AppResult.Failure -> _state.update { it.copy(discoverLoading = false) }
            }
        }
    }

    /** Join or leave; the server's answer re-reads `/my` so the sections agree. */
    fun toggleCommunityMembership(community: Community) {
        if (community.id in _state.value.busyCommunityIds) return
        _state.update { it.copy(busyCommunityIds = it.busyCommunityIds + community.id) }
        viewModelScope.launch {
            val result = if (community.isMember) communities.leave(community.id) else communities.join(community.id)
            when (result) {
                is AppResult.Success -> {
                    val mine = communities.mine()
                    _state.update { current ->
                        current.copy(
                            myCommunities = (mine as? AppResult.Success)?.data ?: current.myCommunities,
                            busyCommunityIds = current.busyCommunityIds - community.id,
                        )
                    }
                }
                is AppResult.Failure -> {
                    _state.update { it.copy(busyCommunityIds = it.busyCommunityIds - community.id) }
                    showMessage(
                        if (community.isMember) "Couldn't leave that community." else "Couldn't join that community.",
                        UsMessageType.Error,
                    )
                }
            }
        }
    }

    // ── Suggestions ─────────────────────────────────────────────────────

    private fun loadSuggestions() = viewModelScope.launch {
        val people = async { suggestions.people() }
        val communitiesToJoin = async { suggestions.communities() }
        val peopleRows = tracker.visible((people.await() as? AppResult.Success)?.data.orEmpty()) { it.userId }
        val communityRows = tracker.visible(
            (communitiesToJoin.await() as? AppResult.Success)?.data.orEmpty(),
        ) { it.communityId }
        val named = resolveNames(peopleRows)
        _state.update {
            it.copy(suggestionsLoaded = true, people = named, suggestedCommunities = communityRows)
        }
        followGraph.ensureKnown(named.map { it.userId })
        postImpressions(named, communityRows)
    }

    /** `display_name` may be empty — the profile is the fallback, in parallel and bounded. */
    private suspend fun resolveNames(people: List<PersonSuggestion>): List<PersonSuggestion> = coroutineScope {
        people.map { person ->
            async {
                if (person.displayName.isNotBlank()) return@async person
                val name = (profiles.getProfile(person.userId) as? AppResult.Success)
                    ?.data?.displayName?.takeIf { it.isNotBlank() }
                person.copy(displayName = name ?: "Someone")
            }
        }.awaitAll()
    }

    private fun postImpressions(people: List<PersonSuggestion>, communityRows: List<CommunitySuggestion>) {
        if (tracker.shouldPostImpression(SuggestionsRepository.TYPE_FRIEND, people.map { it.userId })) {
            viewModelScope.launch { suggestions.peopleImpression(people) }
        }
        if (tracker.shouldPostImpression(SuggestionsRepository.TYPE_COMMUNITY, communityRows.map { it.communityId })) {
            viewModelScope.launch { suggestions.communityImpression(communityRows) }
        }
    }

    fun dismissPerson(userId: String) {
        tracker.dismiss(userId)
        _state.update { it.copy(people = it.people.filterNot { row -> row.userId == userId }) }
        viewModelScope.launch { suggestions.personAction(userId, SuggestionAction.Dismiss) }
    }

    fun followPerson(userId: String) {
        if (userId in _state.value.busyPeopleIds) return
        _state.update { it.copy(busyPeopleIds = it.busyPeopleIds + userId) }
        viewModelScope.launch {
            when (followGraph.follow(userId)) {
                is AppResult.Success -> suggestions.personAction(userId, SuggestionAction.Follow)
                is AppResult.Failure -> showMessage("Couldn't follow right now.", UsMessageType.Error)
            }
            _state.update { it.copy(busyPeopleIds = it.busyPeopleIds - userId) }
        }
    }

    /** "Message": the server decides which conversation that is, or refuses. */
    fun messagePerson(person: PersonSuggestion) {
        if (person.userId in _state.value.busyPeopleIds) return
        _state.update { it.copy(busyPeopleIds = it.busyPeopleIds + person.userId) }
        viewModelScope.launch {
            when (val result = startDirect.open(person.userId)) {
                is StartDirectResult.Opened -> _state.update {
                    it.copy(pendingOpen = PendingOpen(result.conversation.id, person.displayName, isGroup = false))
                }
                is StartDirectResult.NotAllowed ->
                    showMessage("${person.displayName} doesn't accept messages from you yet.", UsMessageType.Warning)
                is StartDirectResult.Failed -> showMessage("Couldn't start that chat. Try again.", UsMessageType.Error)
            }
            _state.update { it.copy(busyPeopleIds = it.busyPeopleIds - person.userId) }
        }
    }

    fun joinSuggestedCommunity(suggestion: CommunitySuggestion) {
        if (suggestion.communityId in _state.value.busyCommunityIds) return
        _state.update { it.copy(busyCommunityIds = it.busyCommunityIds + suggestion.communityId) }
        viewModelScope.launch {
            when (communities.join(suggestion.communityId)) {
                is AppResult.Success -> {
                    tracker.dismiss(suggestion.communityId)
                    suggestions.communityAction(suggestion.communityId, SuggestionAction.Follow)
                    _state.update { current ->
                        current.copy(
                            suggestedCommunities = current.suggestedCommunities
                                .filterNot { it.communityId == suggestion.communityId },
                            // The Communities tab re-reads `/my` next time it opens.
                            communitiesLoaded = false,
                        )
                    }
                    showMessage("Joined ${suggestion.name}.", UsMessageType.Success)
                }
                is AppResult.Failure -> showMessage("Couldn't join ${suggestion.name}.", UsMessageType.Error)
            }
            _state.update { it.copy(busyCommunityIds = it.busyCommunityIds - suggestion.communityId) }
        }
    }

    private companion object {
        const val PRESENCE_LIMIT = 50
        const val DISCOVER_DEBOUNCE_MILLIS = 350L
        const val SEARCH_DEBOUNCE_MILLIS = 300L
        const val SEARCH_MIN_CHARS = 2
    }
}
