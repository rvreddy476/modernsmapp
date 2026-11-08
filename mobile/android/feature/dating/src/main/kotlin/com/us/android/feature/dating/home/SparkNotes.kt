package com.us.android.feature.dating.home

import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** A folded spark note being read on purpose (mechanic M13). */
sealed interface NoteReveal {
    data object Loading : NoteReveal

    data class Shown(val text: String) : NoteReveal

    data class Failed(val message: String) : NoteReveal
}

/**
 * Reads a folded spark note's text only when the viewer taps it.
 *
 * The server no longer sends a hidden note's words in any list (its audit L4):
 * a spark the viewer's comment filter folds arrives with `note_hidden` and no
 * `note`, and the words come from `GET /v1/dating/sparks/:id/note` — so the
 * filter holds even if this app fails to fold. Each note is read at most once
 * while it is loading or shown; a failed read can be tried again.
 */
class SparkNoteReveals(
    private val repository: DatingRepository,
    private val scope: CoroutineScope,
) {
    private val _state = MutableStateFlow<Map<String, NoteReveal>>(emptyMap())
    val state: StateFlow<Map<String, NoteReveal>> = _state.asStateFlow()

    fun reveal(sparkId: String) {
        when (_state.value[sparkId]) {
            NoteReveal.Loading, is NoteReveal.Shown -> return
            is NoteReveal.Failed, null -> Unit
        }
        _state.update { it + (sparkId to NoteReveal.Loading) }
        scope.launch {
            val next = when (val result = repository.sparkNote(sparkId)) {
                is DatingResult.Success -> result.value.note.takeIf { it.isNotBlank() }
                    ?.let { NoteReveal.Shown(it) }
                    ?: NoteReveal.Failed(SparkNoteCopy.GONE)
                // 404: the spark was declined, blocked or locked since; nothing to read.
                is DatingResult.Failure -> NoteReveal.Failed(
                    if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) {
                        SparkNoteCopy.GONE
                    } else {
                        DatingCopy.forError(result.error)
                    },
                )
            }
            _state.update { it + (sparkId to next) }
        }
    }
}

private const val HTTP_NOT_FOUND = 404
