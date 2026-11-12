package com.us.android.feature.echo.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.ProfileNotesTab
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * A profile's Echo tab (`GET /v1/notes/users/:userId?tab=`): Notes, Replies,
 * Reposts, each read the first time it is shown. [start] names the profile.
 */
@HiltViewModel
class ProfileNotesViewModel @Inject constructor(
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
) : ViewModel() {

    private var userId: String = ""

    private val _tab = MutableStateFlow(ProfileNotesTab.NOTES)
    val tab: StateFlow<ProfileNotesTab> = _tab.asStateFlow()

    private val _message = MutableStateFlow<String?>(null)
    val message: StateFlow<String?> = _message.asStateFlow()

    private val feeds: Map<ProfileNotesTab, NoteFeedController> = ProfileNotesTab.entries.associateWith { tab ->
        NoteFeedController(
            scope = viewModelScope,
            fetch = { cursor -> repository.byUser(userId, tab, cursor) },
            onPage = { notes -> viewModelScope.launch { media.load(notes) } },
        )
    }

    val mediaUrls: StateFlow<Map<String, String>> = media.urls

    fun feed(tab: ProfileNotesTab): StateFlow<NoteFeed> = feeds.getValue(tab).feed

    fun start(userId: String) {
        if (userId.isBlank() || userId == this.userId) return
        this.userId = userId
        feeds.getValue(_tab.value).loadIfNeeded()
    }

    fun select(tab: ProfileNotesTab) {
        _tab.value = tab
        if (userId.isNotBlank()) feeds.getValue(tab).loadIfNeeded()
    }

    fun refresh() = feeds.getValue(_tab.value).refresh()

    fun retry() = feeds.getValue(_tab.value).retry()

    fun loadMore() = feeds.getValue(_tab.value).loadMore()

    fun toggleLike(note: Note) {
        viewModelScope.launch { toggleLike(repository, note, ::applyEverywhere)?.let { _message.value = it } }
    }

    fun toggleRepost(note: Note) {
        viewModelScope.launch { toggleRepost(repository, note, ::applyEverywhere)?.let { _message.value = it } }
    }

    fun messageShown() {
        _message.value = null
    }

    private fun applyEverywhere(id: String, transform: (Note) -> Note) =
        feeds.values.forEach { it.mapNote(id, transform) }
}
