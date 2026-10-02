package com.us.android.feature.dating.privacy

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.InputChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.CommentFilterDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.SectionLabel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.Locale
import javax.inject.Inject

/*
 * The comment filter (mechanic M13): what is tucked away in the notes people
 * send with a spark. "Hide unkind comments" and up to 50 hidden words of 2 to
 * 30 characters, lower-cased and distinct. The server applies it: an incoming
 * spark or a Liked you card whose note it hides carries `note_hidden`, and the
 * note shows folded with its reason.
 *
 * `GET`/`PUT /comment-filter`; every change saves on its own, like every
 * switch in Privacy, and the PUT replaces the whole filter. `400
 * INVALID_COMMENT_FILTER` is said in words; `404 MECHANIC_NOT_ENABLED` hides
 * the section.
 */

/** LOADING and HIDDEN draw nothing; FAILED offers a retry. */
enum class CommentFilterPhase { LOADING, HIDDEN, FAILED, READY }

data class CommentFilterUi(
    val phase: CommentFilterPhase = CommentFilterPhase.LOADING,
    val filterUnkind: Boolean = false,
    /** The words as the server last returned them. */
    val words: List<String> = emptyList(),
    /** The word being typed. */
    val input: String = "",
    /** Why [input] cannot be added, said under the field. */
    val inputError: String? = null,
    val busy: Boolean = false,
    val message: UsMessage? = null,
) {
    val full: Boolean get() = words.size >= CommentFilterRules.MAX_WORDS
}

/** The words. Our own. */
object CommentFilterCopy {
    const val SECTION = "Comment filter"
    const val INTRO = "Choose which notes on your sparks are folded away. You can still tap to read them."
    const val UNKIND_TITLE = "Hide unkind comments"
    const val UNKIND_BODY = "Notes that might come across as unkind are folded away."
    const val WORDS_TITLE = "Hidden words"
    const val WORDS_BODY = "Notes that use any of these words are folded away too."
    const val NO_WORDS = "No hidden words yet."
    const val ADD_LABEL = "Add a word"
    const val ADD = "Add"
    const val TOO_SHORT = "Use at least 2 characters."
    const val TOO_LONG = "Keep each word to 30 characters or fewer."
    const val ALREADY = "That word is already on your list."
    const val FULL = "You can hide up to 50 words. Remove one to add another."
    const val INVALID = "Use up to 50 different words, each 2 to 30 characters long."
    const val LOAD_FAILED = "Your comment filter didn't load."

    fun count(words: Int): String = "$words of ${CommentFilterRules.MAX_WORDS}"

    fun remove(word: String): String = "Remove $word"
}

/** The server's word rules, checked before asking. Pure. */
object CommentFilterRules {
    const val MAX_WORDS = 50
    const val MIN_LEN = 2
    const val MAX_LEN = 30

    /** As the server stores a word: trimmed and lower-cased. */
    fun normalize(word: String): String = word.trim().lowercase(Locale.ROOT)

    /** Why [word] cannot join [words]; null when it can. A blank word is not a problem, it is nothing. */
    fun problem(word: String, words: List<String>): String? {
        val w = normalize(word)
        val length = w.codePointCount(0, w.length)
        return when {
            w.isEmpty() -> null
            length < MIN_LEN -> CommentFilterCopy.TOO_SHORT
            length > MAX_LEN -> CommentFilterCopy.TOO_LONG
            w in words -> CommentFilterCopy.ALREADY
            words.size >= MAX_WORDS -> CommentFilterCopy.FULL
            else -> null
        }
    }
}

@HiltViewModel
class CommentFilterViewModel @Inject constructor(
    private val repository: DatingRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CommentFilterUi())
    val state: StateFlow<CommentFilterUi> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.commentFilter()) {
                is DatingResult.Success -> _state.update { it.withServer(result.value) }
                is DatingResult.Failure -> _state.update {
                    when {
                        isOff(result.error) -> CommentFilterUi(phase = CommentFilterPhase.HIDDEN)
                        it.phase == CommentFilterPhase.READY -> it
                        else -> it.copy(phase = CommentFilterPhase.FAILED)
                    }
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun setInput(text: String) = _state.update { it.copy(input = text, inputError = null) }

    fun setFilterUnkind(on: Boolean) {
        val s = _state.value
        save(on, s.words)
    }

    /** Adds the typed word. Said under the field when it cannot be added; nothing is sent then. */
    fun addWord() {
        val s = _state.value
        if (s.busy || s.phase != CommentFilterPhase.READY) return
        val word = CommentFilterRules.normalize(s.input)
        if (word.isEmpty()) return
        CommentFilterRules.problem(word, s.words)?.let { problem ->
            _state.update { it.copy(inputError = problem) }
            return
        }
        save(s.filterUnkind, s.words + word, clearInput = true)
    }

    fun removeWord(word: String) {
        val s = _state.value
        if (word !in s.words) return
        save(s.filterUnkind, s.words - word)
    }

    private fun save(filterUnkind: Boolean, words: List<String>, clearInput: Boolean = false) {
        val s = _state.value
        if (s.busy || s.phase != CommentFilterPhase.READY) return
        _state.update { it.copy(busy = true, inputError = null) }
        viewModelScope.launch {
            when (val result = repository.updateCommentFilter(filterUnkind, words)) {
                is DatingResult.Success -> _state.update {
                    it.withServer(result.value).copy(busy = false, input = if (clearInput) "" else it.input)
                }
                is DatingResult.Failure -> refused(result.error)
            }
        }
    }

    private fun refused(error: DatingError) {
        _state.update {
            when {
                isOff(error) -> CommentFilterUi(phase = CommentFilterPhase.HIDDEN)
                // The list as the server holds it stays; the words say what it allows.
                error.code == CODE_INVALID -> it.copy(busy = false, inputError = CommentFilterCopy.INVALID)
                else -> it.copy(busy = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    private fun CommentFilterUi.withServer(dto: CommentFilterDto) = copy(
        phase = CommentFilterPhase.READY,
        filterUnkind = dto.filterUnkind,
        words = dto.words.orEmpty().map(CommentFilterRules::normalize).filter { it.isNotEmpty() }.distinct(),
    )

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private companion object {
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        const val CODE_INVALID = "INVALID_COMMENT_FILTER"
    }
}

/** The "Comment filter" section of Privacy. Nothing while loading or while the server's flag is off. */
@OptIn(ExperimentalLayoutApi::class)
internal fun LazyListScope.commentFilterSection(state: CommentFilterUi, viewModel: CommentFilterViewModel) {
    when (state.phase) {
        CommentFilterPhase.LOADING, CommentFilterPhase.HIDDEN -> return
        CommentFilterPhase.FAILED -> {
            item { SectionLabel(CommentFilterCopy.SECTION) }
            item {
                DatingCard {
                    Text(CommentFilterCopy.LOAD_FAILED, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    TextButton(onClick = viewModel::refresh) { Text("Try again", color = UsTheme.extended.accentSolid) }
                }
            }
        }
        CommentFilterPhase.READY -> {
            item { SectionLabel(CommentFilterCopy.SECTION) }
            item {
                DatingCard {
                    InfoNote(CommentFilterCopy.INTRO)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(CommentFilterCopy.UNKIND_TITLE, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            Text(CommentFilterCopy.UNKIND_BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        Switch(
                            checked = state.filterUnkind,
                            onCheckedChange = viewModel::setFilterUnkind,
                            enabled = !state.busy,
                            colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
                        )
                    }
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(CommentFilterCopy.WORDS_TITLE, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            Text(CommentFilterCopy.WORDS_BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        Text(CommentFilterCopy.count(state.words.size), style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
                    }
                    if (state.words.isEmpty()) {
                        Text(CommentFilterCopy.NO_WORDS, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                    } else {
                        FlowRow(
                            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                        ) {
                            state.words.forEach { word ->
                                InputChip(
                                    selected = false,
                                    onClick = { viewModel.removeWord(word) },
                                    enabled = !state.busy,
                                    label = { Text(word) },
                                    shape = RoundedCornerShape(UsTheme.radii.full),
                                    trailingIcon = {
                                        Icon(
                                            UsIcons.Close,
                                            contentDescription = CommentFilterCopy.remove(word),
                                            modifier = Modifier.size(16.dp),
                                        )
                                    },
                                )
                            }
                        }
                    }
                    if (!state.full) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                            UsTextField(
                                value = state.input,
                                onValueChange = viewModel::setInput,
                                label = CommentFilterCopy.ADD_LABEL,
                                errorText = state.inputError,
                                enabled = !state.busy,
                                modifier = Modifier.weight(1f),
                            )
                            TextButton(onClick = viewModel::addWord, enabled = !state.busy && state.input.isNotBlank()) {
                                Text(CommentFilterCopy.ADD, color = UsTheme.extended.accentSolid)
                            }
                        }
                    } else {
                        InfoNote(state.inputError ?: CommentFilterCopy.FULL)
                    }
                }
            }
        }
    }
}
