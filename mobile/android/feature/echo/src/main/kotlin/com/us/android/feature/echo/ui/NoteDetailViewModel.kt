package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.model.SessionState
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.canEditText
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.isAuthor
import com.us.android.feature.echo.di.EchoClock
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Clock
import javax.inject.Inject

@Immutable
data class NoteDetailUiState(
    val note: Note? = null,
    val loading: Boolean = true,
    val error: String? = null,
    /** From the top of the conversation down to this note's parent. */
    val conversation: List<Note> = emptyList(),
    val checking: Boolean = false,
    val openComposer: ComposeRequest? = null,
    val blocked: ComposeGate.Blocked? = null,
    val message: String? = null,
    val report: UsPostReportState = UsPostReportState.Idle,
    val deleting: Boolean = false,
    /** The note was deleted: the screen leaves. */
    val deleted: Boolean = false,
)

/** What the ⋮ menu offers on one note, for this viewer, now. */
data class NoteMenu(val edit: Boolean, val delete: Boolean, val report: Boolean)

/**
 * One note: its conversation above, its replies below (paged), and the
 * reply bar. Reply opens the composer only when eligibility says `can_reply`
 * AND the note says `viewer_can_reply`.
 */
@HiltViewModel
class NoteDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: EchoRepository,
    private val media: EchoMediaUrls,
    private val session: SessionStateProvider,
    @EchoClock private val clock: Clock,
) : ViewModel() {

    val noteId: String = savedStateHandle.get<String>(NOTE_ID_KEY).orEmpty()

    private val _state = MutableStateFlow(NoteDetailUiState())
    val state: StateFlow<NoteDetailUiState> = _state.asStateFlow()

    private val replies = NoteFeedController(
        scope = viewModelScope,
        fetch = { cursor -> repository.replies(noteId, cursor) },
        onPage = { notes -> viewModelScope.launch { media.load(notes) } },
    )
    val replyFeed: StateFlow<NoteFeed> = replies.feed
    val mediaUrls: StateFlow<Map<String, String>> = media.urls

    /** A reply was opened from here: the replies are read again on return. */
    private var replyOpened = false

    private val viewerId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    init {
        load()
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            when (val result = repository.note(noteId)) {
                is AppResult.Success -> {
                    _state.update { it.copy(note = result.data, loading = false) }
                    media.load(listOf(result.data))
                    replies.refresh()
                    if (result.data.replyToId != null) loadConversation()
                }
                is AppResult.Failure -> {
                    val error = echoErrorMessage(result.error)
                    _state.update { it.copy(loading = false, error = error) }
                }
            }
        }
    }

    private suspend fun loadConversation() {
        val result = repository.conversation(noteId)
        if (result is AppResult.Success) {
            _state.update { it.copy(conversation = result.data) }
            media.load(result.data)
        }
    }

    /** Back from the composer: a reply may have landed. */
    fun onResume() {
        if (!replyOpened) return
        replyOpened = false
        replies.refresh()
        viewModelScope.launch {
            val fresh = (repository.note(noteId) as? AppResult.Success)?.data
            if (fresh != null) _state.update { it.copy(note = fresh) }
        }
    }

    fun loadMoreReplies() = replies.loadMore()

    fun retryReplies() = replies.retry()

    fun menuFor(note: Note): NoteMenu {
        val own = isAuthor(note, viewerId)
        return NoteMenu(edit = canEditText(note, viewerId, clock.instant()), delete = own, report = !own)
    }

    fun toggleLike(note: Note) {
        viewModelScope.launch { toggleLike(repository, note, ::applyEverywhere)?.let(::say) }
    }

    fun toggleRepost(note: Note) {
        viewModelScope.launch { toggleRepost(repository, note, ::applyEverywhere)?.let(::say) }
    }

    /** Reply to [note] (this note, or one of its replies). */
    fun reply(note: Note) = gate(ComposeRequest(ComposeMode.REPLY, note.id)) { replyGate(it, note) }

    fun quote(note: Note) = gate(ComposeRequest(ComposeMode.QUOTE, note.id), ::postGate)

    /** Edit opens the composer on the note; the server holds the 30-minute window. */
    fun edit(note: Note) {
        if (!menuFor(note).edit) return
        _state.update { it.copy(openComposer = ComposeRequest(ComposeMode.EDIT, note.id)) }
    }

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

    fun composerOpened() {
        if (_state.value.openComposer?.mode in RETURNING_MODES) replyOpened = true
        _state.update { it.copy(openComposer = null) }
    }

    fun dismissBlocked() = _state.update { it.copy(blocked = null) }

    fun messageShown() = _state.update { it.copy(message = null) }

    /** Deletes [note] (the author's own); this note leaves the screen, a reply leaves the list. */
    fun delete(note: Note) {
        if (!isAuthor(note, viewerId) || _state.value.deleting) return
        _state.update { it.copy(deleting = true) }
        viewModelScope.launch {
            when (val result = repository.delete(note.id)) {
                is AppResult.Success -> {
                    if (note.id == noteId) {
                        _state.update { it.copy(deleting = false, deleted = true) }
                    } else {
                        replies.remove(note.id)
                        _state.update { it.copy(deleting = false, message = "Note deleted") }
                    }
                }
                is AppResult.Failure -> {
                    val message = echoErrorMessage(result.error)
                    _state.update { it.copy(deleting = false, message = message) }
                }
            }
        }
    }

    fun report(note: Note, reason: String, details: String) {
        _state.update { it.copy(report = UsPostReportState.Sending) }
        viewModelScope.launch {
            val outcome = repository.report(note.id, reason, details)
            _state.update {
                it.copy(
                    report = when (outcome) {
                        ReportOutcome.Filed -> UsPostReportState.Sent
                        ReportOutcome.AlreadyReported -> UsPostReportState.AlreadyReported
                        is ReportOutcome.Failed -> UsPostReportState.Failed
                    },
                )
            }
        }
    }

    fun resetReport() = _state.update { it.copy(report = UsPostReportState.Idle) }

    private fun applyEverywhere(id: String, transform: (Note) -> Note) {
        _state.update { s ->
            s.copy(note = s.note?.mapNote(id, transform), conversation = s.conversation.mapNote(id, transform))
        }
        replies.mapNote(id, transform)
    }

    private fun say(message: String) = _state.update { it.copy(message = message) }

    companion object {
        /** The route's argument name ([com.us.android.feature.echo.navigation.EchoNoteRoute.noteId]). */
        const val NOTE_ID_KEY = "noteId"

        /** A reply or an edit comes back here: the note and its replies are read again. */
        private val RETURNING_MODES = setOf(ComposeMode.REPLY, ComposeMode.EDIT)
    }
}
