package com.us.android.feature.dating.privacy

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.home.FirstMoveCopy
import com.us.android.feature.dating.network.FirstMoveSettingsDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.SectionLabel
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where the first-move section stands. HIDDEN: the server's flag is off, and nothing is drawn. */
enum class FirstMovePhase { LOADING, HIDDEN, FAILED, READY }

/**
 * The first-move setting (mechanic M5): the opt-in and up to [maxQuestions]
 * opening questions. [saved] is what the server holds; [drafts] is the editor.
 */
data class FirstMoveSettingsUi(
    val phase: FirstMovePhase = FirstMovePhase.LOADING,
    val enabled: Boolean = false,
    val saved: List<String> = emptyList(),
    val drafts: List<String> = emptyList(),
    val maxQuestions: Int = DEFAULT_MAX_QUESTIONS,
    val maxLength: Int = DEFAULT_MAX_LENGTH,
    val busy: Boolean = false,
    /** A refusal of the questions, shown under the editor. */
    val error: String? = null,
    val message: UsMessage? = null,
) {
    /** What a save would send: trimmed, blanks left out. */
    val cleaned: List<String> get() = drafts.map { it.trim() }.filter { it.isNotEmpty() }

    /** The editor differs from what the server holds. */
    val dirty: Boolean get() = cleaned != saved

    val canAdd: Boolean get() = drafts.size < maxQuestions

    companion object {
        const val DEFAULT_MAX_QUESTIONS = 3
        const val DEFAULT_MAX_LENGTH = 140
    }
}

/**
 * `GET`/`PUT /first-move`. The switch saves on its own, like every switch in
 * Privacy; the questions are saved together with one button. A
 * `MECHANIC_NOT_ENABLED` answer from either route hides the whole section.
 */
@HiltViewModel
class FirstMoveSettingsViewModel @Inject constructor(
    private val repository: DatingRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(FirstMoveSettingsUi())
    val state: StateFlow<FirstMoveSettingsUi> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.firstMove()) {
                is DatingResult.Success -> _state.update { it.withServer(result.value, drafts = true) }
                is DatingResult.Failure -> _state.update {
                    if (isOff(result.error)) {
                        FirstMoveSettingsUi(phase = FirstMovePhase.HIDDEN)
                    } else {
                        it.copy(phase = if (it.phase == FirstMovePhase.READY) it.phase else FirstMovePhase.FAILED)
                    }
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun setEnabled(enabled: Boolean) = save(enabled = enabled, questions = null)

    fun editQuestion(index: Int, text: String) = _state.update { s ->
        if (index !in s.drafts.indices) return@update s
        s.copy(drafts = s.drafts.toMutableList().also { it[index] = text.take(s.maxLength) }, error = null)
    }

    fun addQuestion() = _state.update { if (it.canAdd) it.copy(drafts = it.drafts + "", error = null) else it }

    fun removeQuestion(index: Int) = _state.update { s ->
        if (index !in s.drafts.indices) s else s.copy(drafts = s.drafts.filterIndexed { i, _ -> i != index }, error = null)
    }

    /** Saves the editor as the whole list; an empty editor removes every question. */
    fun saveQuestions() {
        val current = _state.value
        val questions = current.cleaned
        when {
            questions.size > current.maxQuestions ->
                _state.update { it.copy(error = "You can have up to ${current.maxQuestions} opening questions.") }
            questions.any { it.length > current.maxLength } ->
                _state.update { it.copy(error = "Each question needs 1 to ${current.maxLength} characters.") }
            else -> save(enabled = null, questions = questions)
        }
    }

    private fun save(enabled: Boolean?, questions: List<String>?) {
        if (_state.value.busy || _state.value.phase != FirstMovePhase.READY) return
        _state.update { it.copy(busy = true, error = null) }
        viewModelScope.launch {
            when (val result = repository.updateFirstMove(enabled = enabled, questions = questions)) {
                is DatingResult.Success -> _state.update {
                    it.withServer(result.value, drafts = questions != null).copy(
                        busy = false,
                        message = if (questions != null) successMessage("Opening questions saved.") else null,
                    )
                }
                is DatingResult.Failure -> refused(result.error, questionsSave = questions != null)
            }
        }
    }

    private fun refused(error: DatingError, questionsSave: Boolean) {
        val words = DatingCopy.forError(error, repository.json)
        _state.update {
            when {
                isOff(error) -> FirstMoveSettingsUi(phase = FirstMovePhase.HIDDEN)
                // A refusal of the questions belongs under the editor, and the drafts stay.
                questionsSave && error.code in QUESTION_CODES -> it.copy(busy = false, error = words)
                else -> it.copy(busy = false, message = DatingCopy.message(error, repository.json))
            }
        }
    }

    /** The server's answer onto the state; [drafts] replaces the editor too (a load, or a questions save). */
    private fun FirstMoveSettingsUi.withServer(dto: FirstMoveSettingsDto, drafts: Boolean): FirstMoveSettingsUi {
        val saved = dto.questions.map { it.text.trim() }.filter { it.isNotEmpty() }
        return copy(
            phase = FirstMovePhase.READY,
            enabled = dto.enabled,
            saved = saved,
            drafts = if (drafts) saved else this.drafts,
            maxQuestions = dto.maxQuestions.takeIf { it > 0 } ?: FirstMoveSettingsUi.DEFAULT_MAX_QUESTIONS,
            maxLength = dto.maxLength.takeIf { it > 0 } ?: FirstMoveSettingsUi.DEFAULT_MAX_LENGTH,
        )
    }

    private fun isOff(error: DatingError): Boolean = error.code == CODE_MECHANIC_NOT_ENABLED

    private companion object {
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        val QUESTION_CODES = setOf("OPENING_QUESTIONS_TOO_MANY", "OPENING_QUESTION_INVALID", "OPENING_QUESTION_REFUSED")
    }
}

/** The "First move" section of Privacy. Draws nothing while loading or while the server's flag is off. */
internal fun LazyListScope.firstMoveSection(state: FirstMoveSettingsUi, viewModel: FirstMoveSettingsViewModel) {
    when (state.phase) {
        FirstMovePhase.LOADING, FirstMovePhase.HIDDEN -> return
        FirstMovePhase.FAILED -> {
            item { SectionLabel("First move") }
            item {
                DatingCard {
                    Text("Your first-move setting didn't load.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                    TextButton(onClick = viewModel::refresh) { Text("Try again", color = UsTheme.extended.accentSolid) }
                }
            }
        }
        FirstMovePhase.READY -> {
            item { SectionLabel("First move") }
            item {
                DatingCard {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(FirstMoveCopy.SETTING_TITLE, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
                            Text(FirstMoveCopy.SETTING_BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        Switch(
                            checked = state.enabled,
                            onCheckedChange = viewModel::setEnabled,
                            enabled = !state.busy,
                            colors = SwitchDefaults.colors(checkedTrackColor = UsTheme.extended.accentSolid),
                        )
                    }
                }
            }
            item { QuestionsEditor(state, viewModel) }
        }
    }
}

@Composable
private fun QuestionsEditor(state: FirstMoveSettingsUi, viewModel: FirstMoveSettingsViewModel) {
    DatingCard {
        Text(FirstMoveCopy.QUESTIONS_TITLE, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        Text(FirstMoveCopy.QUESTIONS_BODY, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        state.drafts.forEachIndexed { index, text ->
            Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Column(Modifier.weight(1f)) {
                    UsTextField(
                        value = text,
                        onValueChange = { viewModel.editQuestion(index, it) },
                        label = "Question ${index + 1}",
                        placeholder = "Something you'd like them to answer",
                        singleLine = false,
                        enabled = !state.busy,
                    )
                    Text(
                        "${text.length}/${state.maxLength}",
                        style = MaterialTheme.typography.labelSmall,
                        color = UsTheme.extended.textDim,
                        modifier = Modifier.align(Alignment.End),
                    )
                }
                IconButton(onClick = { viewModel.removeQuestion(index) }, enabled = !state.busy) {
                    Icon(UsIcons.Trash, contentDescription = "Remove question ${index + 1}", tint = UsTheme.extended.textMuted)
                }
            }
        }
        if (state.canAdd) {
            TextButton(onClick = viewModel::addQuestion, enabled = !state.busy) {
                Text(if (state.drafts.isEmpty()) "Add a question" else "Add another question", color = UsTheme.extended.accentSolid)
            }
        } else {
            InfoNote("That's the most you can have: ${state.maxQuestions}.")
        }
        state.error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.statusDanger) }
        if (state.dirty) {
            UsButton(text = "Save questions", onClick = viewModel::saveQuestions, loading = state.busy, modifier = Modifier.fillMaxWidth())
        }
    }
}
