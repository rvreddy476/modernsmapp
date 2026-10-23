package com.us.android.core.feed.ui.copyright

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.copyright.CopyrightReviewRepository
import com.us.android.core.feed.data.copyright.CopyrightReviews
import com.us.android.core.feed.data.copyright.copyrightRequestRefusal
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightReview
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The "Request review" sheet, as it draws. */
data class CopyrightRequestUiState(
    val note: String = "",
    val sending: Boolean = false,
    /** Why the note cannot go, or why the server said no; null when there is nothing to say. */
    val problem: String? = null,
    /** The server has the request; the sheet leaves. */
    val sent: CopyrightReview? = null,
) {
    /** Characters left, by the server's count (code points). */
    val remaining: Int get() = CopyrightCopy.NOTE_MAX - note.trim().let { it.codePointCount(0, it.length) }

    val canSend: Boolean get() = !sending && CopyrightCopy.noteProblem(note) == null
}

/**
 * Behind the copyright notice and its "Request review" sheet (copyright
 * title review, 2026-10-24): the author's note, sent once with
 * `POST /v1/posts/:id/copyright-review/request`. A landed request is in
 * [CopyrightReviews] at once, so every surface showing the video says
 * "Review requested" without a refetch.
 */
@HiltViewModel
class CopyrightRequestViewModel @Inject constructor(
    private val repository: CopyrightReviewRepository,
    store: CopyrightReviews,
) : ViewModel() {

    /** Every review this session has learned, by post id, for the notice to layer over what it was handed. */
    val reviews: StateFlow<Map<String, CopyrightReview>> = store.reviews

    private val _state = MutableStateFlow(CopyrightRequestUiState())
    val state: StateFlow<CopyrightRequestUiState> = _state.asStateFlow()

    /** The sheet opened: a fresh note each time. */
    fun open() = _state.update { CopyrightRequestUiState() }

    fun changeNote(note: String) = _state.update { it.copy(note = note, problem = null) }

    fun send(postId: String) {
        val now = _state.value
        if (now.sending) return
        CopyrightCopy.noteProblem(now.note)?.let { problem ->
            _state.update { it.copy(problem = problem) }
            return
        }
        _state.update { it.copy(sending = true, problem = null) }
        viewModelScope.launch {
            val result = repository.request(postId, now.note)
            _state.update { current ->
                when (result) {
                    is AppResult.Success -> current.copy(sending = false, sent = result.data)
                    is AppResult.Failure -> current.copy(sending = false, problem = copyrightRequestRefusal(result.error))
                }
            }
        }
    }
}
