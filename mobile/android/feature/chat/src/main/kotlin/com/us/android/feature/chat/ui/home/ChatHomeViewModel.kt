package com.us.android.feature.chat.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
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
) {
    val visibleChats: List<Conversation> get() = ChatHomeFilters.directChats(conversations, query, viewerId)
    val visibleGroups: List<Conversation> get() = ChatHomeFilters.groups(conversations, query)
    val visibleMyCommunities: List<Community> get() = ChatHomeFilters.communities(myCommunities, query)
    val visibleDiscover: List<Community>
        get() = ChatHomeFilters.communities(discover, query).filter { row -> myCommunities.none { it.id == row.id } }
    val visiblePeople: List<PersonSuggestion> get() = ChatHomeFilters.people(people, query)
    val visibleSuggestedCommunities: List<CommunitySuggestion>
        get() = ChatHomeFilters.suggestedCommunities(suggestedCommunities, query)

    /** The tabs wearing an unread dot: Chats for direct, Groups for groups. */
    val unreadTabs: Set<ChatHomeTab>
        get() = buildSet {
            if (conversations.any { it.type != ChatHomeFilters.GROUP_TYPE && it.hasUnread } || requests.isNotEmpty()) {
                add(ChatHomeTab.Chats)
            }
            if (conversations.any { it.type == ChatHomeFilters.GROUP_TYPE && it.hasUnread } || invitationCount > 0) {
                add(ChatHomeTab.Groups)
            }
        }
    val requestCount: Int get() = requests.size
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

    init {
        session.start()
        store.scheduleDrain()
        observeCache()
        refresh()
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
        val online = repository.bulkPresence(peers)
        if (online is AppResult.Success) _state.update { it.copy(onlineUserIds = online.data) }
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
    }
}
