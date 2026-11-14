package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.Notification
import com.us.android.core.notifications.data.NotificationsRepository
import com.us.android.core.notifications.data.UnreadBadge
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoPeople
import com.us.android.feature.echo.data.EchoProfile
import com.us.android.feature.echo.data.EchoProfileTab
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.echoListErrorMessage
import com.us.android.feature.echo.data.parseInstant
import com.us.android.feature.echo.data.scheduleProblem
import com.us.android.feature.echo.di.EchoClock
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Clock
import java.time.Instant
import javax.inject.Inject

/**
 * The author's scheduled posts (`GET /v1/notes/me/scheduled`, soonest
 * first): change the time (5 minutes to 18 months ahead) or delete.
 */
@HiltViewModel
class ScheduledViewModel @Inject constructor(
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
    @EchoClock private val clock: Clock,
) : ViewModel() {

    private val controller = NoteFeedController(
        scope = viewModelScope,
        fetch = { cursor -> repository.scheduled(cursor) },
        onPage = { notes -> viewModelScope.launch { media.load(notes) } },
        errorText = ::echoListErrorMessage,
    )
    val feed: StateFlow<NoteFeed> = controller.feed
    val mediaUrls: StateFlow<Map<String, String>> = media.urls

    private val _message = MutableStateFlow<String?>(null)
    val message: StateFlow<String?> = _message.asStateFlow()

    init {
        controller.loadIfNeeded()
    }

    fun refresh() = controller.refresh()
    fun retry() = controller.retry()
    fun loadMore() = controller.loadMore()

    /** A new time for [note]; refused here first when it is outside 5 minutes to 18 months. */
    fun reschedule(note: Note, at: Instant) {
        scheduleProblem(at, clock.instant())?.let {
            _message.value = it
            return
        }
        viewModelScope.launch {
            when (val result = repository.reschedule(note.id, at.toString())) {
                is AppResult.Success -> {
                    controller.mapNote(note.id) { result.data }
                    _message.value = "Your post was rescheduled"
                }
                is AppResult.Failure -> _message.value = echoErrorMessage(result.error)
            }
        }
    }

    fun delete(note: Note) {
        viewModelScope.launch {
            when (val result = repository.deleteScheduled(note.id)) {
                is AppResult.Success -> {
                    controller.remove(note.id)
                    _message.value = "Your scheduled post was deleted"
                }
                is AppResult.Failure -> _message.value = echoErrorMessage(result.error)
            }
        }
    }

    fun messageShown() {
        _message.value = null
    }

    /** When [note] is due, for the time picker's start. */
    fun scheduledAt(note: Note): Instant = parseInstant(note.scheduledAt) ?: clock.instant()
}

@Immutable
data class EchoProfileUiState(
    val profile: EchoProfile? = null,
    val loading: Boolean = true,
    val error: String? = null,
    val tab: EchoProfileTab = EchoProfileTab.POSTS,
    val followBusy: Boolean = false,
    val message: String? = null,
)

/**
 * Echo's profile page, X's: cover, avatar, name and mark, @handle, joined
 * date, Following / Followers; Follow (or Edit profile on one's own); tabs
 * Posts, Replies, Reposts, Media.
 */
@HiltViewModel
class EchoProfileViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: EchoRepository,
    private val people: EchoPeople,
    private val media: EchoMediaUrls,
    deps: EchoActionDeps,
) : ViewModel() {

    val userId: String = savedStateHandle.get<String>(USER_ID_KEY).orEmpty()
    val isOwn: Boolean get() = userId.isNotBlank() && userId == people.viewerId

    private val _state = MutableStateFlow(EchoProfileUiState())
    val state: StateFlow<EchoProfileUiState> = _state.asStateFlow()

    private val feeds: Map<EchoProfileTab, NoteFeedController> = EchoProfileTab.entries.associateWith { tab ->
        NoteFeedController(
            scope = viewModelScope,
            fetch = { cursor ->
                when (val page = repository.byUser(userId, tab.source, cursor)) {
                    is AppResult.Success -> if (tab.mediaOnly) {
                        AppResult.Success(page.data.copy(items = page.data.items.filter { it.media.isNotEmpty() }))
                    } else {
                        page
                    }
                    is AppResult.Failure -> page
                }
            },
            onPage = { notes -> viewModelScope.launch { media.load(notes) } },
        )
    }

    val mediaUrls: StateFlow<Map<String, String>> = media.urls
    val gate = ComposeGateController(viewModelScope, repository)
    val actions = EchoNoteActions(
        scope = viewModelScope,
        deps = deps,
        apply = { id, transform -> feeds.values.forEach { it.mapNote(id, transform) } },
        remove = { predicate -> feeds.values.forEach { it.removeWhere(predicate) } },
    )

    init {
        load()
        feeds.getValue(EchoProfileTab.POSTS).loadIfNeeded()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            when (val result = people.profile(userId)) {
                is AppResult.Success -> {
                    _state.update { it.copy(profile = result.data, loading = false) }
                    media.loadIds(listOfNotNull(result.data.avatarMediaId, result.data.coverMediaId))
                }
                is AppResult.Failure -> _state.update {
                    it.copy(
                        loading = false,
                        error = echoErrorMessage(result.error)
                    )
                }
            }
        }
    }

    fun feed(tab: EchoProfileTab): StateFlow<NoteFeed> = feeds.getValue(tab).feed

    fun select(tab: EchoProfileTab) {
        _state.update { it.copy(tab = tab) }
        feeds.getValue(tab).loadIfNeeded()
    }

    fun refresh() {
        load()
        feeds.getValue(_state.value.tab).refresh()
    }

    fun retry() = feeds.getValue(_state.value.tab).retry()
    fun loadMore() = feeds.getValue(_state.value.tab).loadMore()

    /** Follow / Following, optimistic on the button and the follower count. */
    fun toggleFollow() {
        val profile = _state.value.profile ?: return
        if (_state.value.followBusy || isOwn) return
        val target = !profile.viewerFollows
        val moved = profile.copy(
            viewerFollows = target,
            followers = (profile.followers + if (target) 1 else -1).coerceAtLeast(0),
        )
        _state.update { it.copy(profile = moved, followBusy = true) }
        viewModelScope.launch {
            when (val result = people.setFollowing(userId, target)) {
                is AppResult.Success -> _state.update { it.copy(followBusy = false) }
                is AppResult.Failure -> _state.update {
                    it.copy(profile = profile, followBusy = false, message = echoErrorMessage(result.error))
                }
            }
        }
    }

    fun messageShown() = _state.update { it.copy(message = null) }

    companion object {
        const val USER_ID_KEY = "userId"
    }
}

/** All or Mentions. Mentions is notification-service's existing `?category=mentions`. */
enum class EchoNotificationsTab(val label: String, val category: String?) {
    ALL("All", null),
    MENTIONS("Mentions", "mentions"),
}

@Immutable
data class NotificationsFeed(
    val items: List<Notification> = emptyList(),
    val cursor: String? = null,
    val loaded: Boolean = false,
    val loading: Boolean = false,
    val error: String? = null,
)

/** Echo's Notifications tab: the app's inbox, All or Mentions; a tap marks the row read. */
@HiltViewModel
class EchoNotificationsViewModel @Inject constructor(
    private val notifications: NotificationsRepository,
    private val badge: UnreadBadge,
) : ViewModel() {

    private val _tab = MutableStateFlow(EchoNotificationsTab.ALL)
    val tab: StateFlow<EchoNotificationsTab> = _tab.asStateFlow()

    private val _feeds = EchoNotificationsTab.entries.associateWith { MutableStateFlow(NotificationsFeed()) }

    fun feed(tab: EchoNotificationsTab): StateFlow<NotificationsFeed> = _feeds.getValue(tab).asStateFlow()

    init {
        load(EchoNotificationsTab.ALL, more = false)
    }

    fun select(tab: EchoNotificationsTab) {
        _tab.value = tab
        if (!_feeds.getValue(tab).value.loaded) load(tab, more = false)
    }

    fun refresh() = load(_tab.value, more = false)

    fun loadMore() {
        val f = _feeds.getValue(_tab.value).value
        if (f.loaded && f.cursor != null && !f.loading) load(_tab.value, more = true)
    }

    private fun load(tab: EchoNotificationsTab, more: Boolean) {
        val flow = _feeds.getValue(tab)
        val cursor = if (more) flow.value.cursor else null
        flow.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            when (val result = notifications.page(cursor = cursor, category = tab.category)) {
                is AppResult.Success -> flow.update {
                    val items = if (more) it.items + result.data.items else result.data.items
                    NotificationsFeed(items.distinctBy { n -> n.id }, result.data.nextCursor, loaded = true)
                }
                is AppResult.Failure -> flow.update { it.copy(loading = false, error = echoErrorMessage(result.error)) }
            }
            if (!more) badge.refresh()
        }
    }

    /** Marks [notification] read (optimistic on the row and the badge). */
    fun opened(notification: Notification) {
        if (notification.isRead) return
        _feeds.values.forEach { flow ->
            flow.update { f ->
                f.copy(
                    items = f.items.map { if (it.id == notification.id) it.copy(isRead = true) else it }
                )
            }
        }
        badge.set(badge.count.value - 1)
        viewModelScope.launch { notifications.markRead(notification.address) }
    }
}
