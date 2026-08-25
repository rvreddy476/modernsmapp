package com.us.android.feature.post.createhub

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CONTENT_TYPE_POLL
import com.us.android.feature.post.data.dto.CreatePollRequest
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

/**
 * The Create hub's Poll surface.
 *
 * ## THE CONTRACT IT SPEAKS
 *
 * `content_type: "poll"` with the server's own `CreatePollRequest` (question,
 * 2–6 options, optional multi-select) through the SAME
 * [ComposerRepository.createPost] call site every other post uses (guard G-7).
 * The post's `text` is the question, so every surface that cannot yet render a
 * poll block still shows something true.
 *
 * ## IDEMPOTENCY
 *
 * One creation key per poll draft, held until the create SUCCEEDS or is
 * terminal — a retry after a lost response replays, it does not double-post.
 */
@HiltViewModel
class PollComposerViewModel @Inject constructor(
    private val repository: ComposerRepository,
) : ViewModel() {

    sealed interface Phase {
        data object Editing : Phase
        data object Posting : Phase
        data class Published(val postId: String) : Phase
        data class RetryableFailure(val message: String) : Phase
        data class TerminalFailure(val message: String) : Phase
    }

    data class PollUiState(
        val question: String = "",
        val options: List<String> = listOf("", ""),
        val allowsMultiple: Boolean = false,
        val phase: Phase = Phase.Editing,
    ) {
        val filledOptions: List<String> get() = options.map { it.trim() }.filter { it.isNotEmpty() }
        val canAddOption: Boolean get() = options.size < MAX_OPTIONS
        val canPost: Boolean
            get() = question.isNotBlank() &&
                filledOptions.size >= MIN_OPTIONS &&
                phase !is Phase.Posting
    }

    private val _state = MutableStateFlow(PollUiState())
    val state: StateFlow<PollUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    fun onQuestionChanged(value: String) = _state.update { it.copy(question = value) }

    fun onOptionChanged(index: Int, value: String) = _state.update { state ->
        state.copy(
            options = state.options.mapIndexed { i, option ->
                if (i == index) value else option
            },
        )
    }

    fun onAddOption() = _state.update { state ->
        if (state.canAddOption) state.copy(options = state.options + "") else state
    }

    fun onRemoveOption(index: Int) = _state.update { state ->
        if (state.options.size > MIN_OPTIONS) {
            state.copy(options = state.options.filterIndexed { i, _ -> i != index })
        } else {
            state
        }
    }

    fun onAllowsMultipleChanged(value: Boolean) = _state.update { it.copy(allowsMultiple = value) }

    fun onPost() {
        val current = _state.value
        if (!current.canPost) return
        _state.update { it.copy(phase = Phase.Posting) }
        viewModelScope.launch {
            val request = CreatePostRequest(
                text = current.question.trim(),
                contentType = CONTENT_TYPE_POLL,
                language = DEFAULT_LANGUAGE,
                distribution = DistributionRequest(),
                poll = CreatePollRequest(
                    question = current.question.trim(),
                    options = current.filledOptions,
                    allowsMultiple = current.allowsMultiple,
                ),
            )
            when (val result = repository.createPost(creationKey, request)) {
                is AppResult.Success -> {
                    // The key burned with a success; a NEW poll needs a new one.
                    creationKey = UUID.randomUUID().toString()
                    _state.update { it.copy(phase = Phase.Published(result.data)) }
                }
                is AppResult.Failure ->
                    _state.update {
                        val message = repository.message(result.error)
                        it.copy(
                            phase = if (repository.isTerminal(result.error)) {
                                Phase.TerminalFailure(message)
                            } else {
                                Phase.RetryableFailure(message)
                            },
                        )
                    }
            }
        }
    }

    fun onRetry() {
        _state.update { it.copy(phase = Phase.Editing) }
        onPost()
    }

    private companion object {
        const val MIN_OPTIONS = 2
        const val MAX_OPTIONS = 6
        const val DEFAULT_LANGUAGE = "en"
    }
}
