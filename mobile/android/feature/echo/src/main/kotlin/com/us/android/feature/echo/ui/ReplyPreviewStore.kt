package com.us.android.feature.echo.ui

import com.us.android.core.common.result.AppResult
import com.us.android.feature.echo.data.Note
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * The first replies of the notes on screen, for the timeline cards' reply
 * bubbles (echo-look-v3). Timeline notes carry no replies, so the list asks
 * for them as cards come into view: once per note, [LIMIT] at a time, through
 * the existing replies route, and only for a note whose `reply_count` says it
 * has any. Nothing waits on it: a card shows no bubbles until its replies are
 * here. A read that fails is forgotten, so the card is asked for again the
 * next time it comes into view.
 */
class ReplyPreviewStore(
    private val scope: CoroutineScope,
    private val load: suspend (noteId: String) -> AppResult<List<Note>>,
) {
    private val _replies = MutableStateFlow<Map<String, List<Note>>>(emptyMap())

    /** Each asked note's first replies, by note id; a note not here has not arrived (or was not asked). */
    val replies: StateFlow<Map<String, List<Note>>> = _replies.asStateFlow()

    private val asked = HashSet<String>()

    /** The cards now on screen: reads the replies of those with any that are not read or on their way. */
    fun request(notes: List<Note>) {
        notes.filter { it.replyCount > 0 && asked.add(it.id) }.forEach { note ->
            scope.launch {
                when (val result = load(note.id)) {
                    is AppResult.Success -> _replies.update { it + (note.id to result.data.take(LIMIT)) }
                    is AppResult.Failure -> asked.remove(note.id)
                }
            }
        }
    }

    companion object {
        /** Two bubbles at most on a card. */
        const val LIMIT = MAX_BUBBLES
    }
}
