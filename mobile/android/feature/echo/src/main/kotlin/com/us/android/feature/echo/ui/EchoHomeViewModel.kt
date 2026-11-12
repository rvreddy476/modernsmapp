package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.Timeline
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the composer is opened for. [wire] rides in the route. */
enum class ComposeMode(val wire: String) {
    NEW("new"),
    REPLY("reply"),
    QUOTE("quote"),
    EDIT("edit"),
    ;

    companion object {
        fun fromWire(wire: String?): ComposeMode = entries.firstOrNull { it.wire == wire } ?: NEW
    }
}

/** A composer the gate let through: the screen navigates, then calls the consume function. */
data class ComposeRequest(val mode: ComposeMode, val targetId: String = "")

@Immutable
data class EchoHomeUiState(
    val timeline: Timeline = Timeline.FOLLOWING,
    /** The eligibility read is on its way. */
    val checking: Boolean = false,
    val openComposer: ComposeRequest? = null,
    val blocked: ComposeGate.Blocked? = null,
    val message: String? = null,
)

/**
 * Echo's home: Following (the default) and For You, each its own paged list,
 * read the first time its tab is shown. The compose button and a card's
 * Quote ask eligibility first; a viewer who may not post gets the sheet, not
 * the composer.
 */
@HiltViewModel
class EchoHomeViewModel @Inject constructor(
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
) : ViewModel() {

    private val _state = MutableStateFlow(EchoHomeUiState())
    val state: StateFlow<EchoHomeUiState> = _state.asStateFlow()

    private val feeds: Map<Timeline, NoteFeedController> = Timeline.entries.associateWith { timeline ->
        NoteFeedController(
            scope = viewModelScope,
            fetch = { cursor -> repository.timeline(timeline, cursor) },
            onPage = { notes -> viewModelScope.launch { media.load(notes) } },
        )
    }

    val following: StateFlow<NoteFeed> = feeds.getValue(Timeline.FOLLOWING).feed
    val forYou: StateFlow<NoteFeed> = feeds.getValue(Timeline.FOR_YOU).feed
    val mediaUrls: StateFlow<Map<String, String>> = media.urls

    init {
        feeds.getValue(Timeline.FOLLOWING).loadIfNeeded()
    }

    fun select(timeline: Timeline) {
        _state.update { it.copy(timeline = timeline) }
        feeds.getValue(timeline).loadIfNeeded()
    }

    fun refresh() = feeds.getValue(_state.value.timeline).refresh()

    fun retry() = feeds.getValue(_state.value.timeline).retry()

    fun loadMore() = feeds.getValue(_state.value.timeline).loadMore()

    fun toggleLike(note: Note) {
        viewModelScope.launch { toggleLike(repository, note, ::applyEverywhere)?.let(::say) }
    }

    fun toggleRepost(note: Note) {
        viewModelScope.launch { toggleRepost(repository, note, ::applyEverywhere)?.let(::say) }
    }

    /** The compose button: Premium only. */
    fun compose() = gate(ComposeRequest(ComposeMode.NEW), ::postGate)

    /** A card's Quote: a quote is a note, so Premium only too. */
    fun quote(note: Note) = gate(ComposeRequest(ComposeMode.QUOTE, note.id), ::postGate)

    private fun gate(request: ComposeRequest, rule: (com.us.android.feature.echo.data.Eligibility) -> ComposeGate) {
        if (_state.value.checking) return
        _state.update { it.copy(checking = true) }
        viewModelScope.launch {
            val gate = readGate(repository, rule)
            _state.update {
                when (gate) {
                    ComposeGate.Open -> it.copy(checking = false, openComposer = request)
                    is ComposeGate.Blocked -> it.copy(checking = false, blocked = gate)
                    is ComposeGate.Failed -> it.copy(checking = false, message = gate.message)
                }
            }
        }
    }

    fun composerOpened() = _state.update { it.copy(openComposer = null) }

    fun dismissBlocked() = _state.update { it.copy(blocked = null) }

    fun messageShown() = _state.update { it.copy(message = null) }

    /** A note deleted from its page leaves both timelines. */
    fun removed(noteId: String) = feeds.values.forEach { it.remove(noteId) }

    private fun applyEverywhere(id: String, transform: (Note) -> Note) =
        feeds.values.forEach { it.mapNote(id, transform) }

    private fun say(message: String) = _state.update { it.copy(message = message) }
}
