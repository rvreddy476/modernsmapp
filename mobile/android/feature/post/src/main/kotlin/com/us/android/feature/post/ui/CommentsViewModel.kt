// MatchingDeclarationName: this file exports the ViewModel plus the error-text
// object it is the only caller of. Splitting the strings into their own file
// would put the copy further from the state transition that chooses it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.feature.post.data.PostRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The comments on one post.
 *
 * Read-only, and that is a contract decision rather than a phasing one. The
 * captured comment body says a native create path was exercised, but no create
 * request or response was ever recorded, so this screen offers no composer. A
 * text field wired to a guessed request body loses the user's words to a 400.
 */
@HiltViewModel
class CommentsViewModel @Inject constructor(
    private val repository: PostRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val postId: String = savedStateHandle.get<String>(POST_ID_KEY).orEmpty()

    private val _state = MutableStateFlow<CommentsUiState>(CommentsUiState.Loading)
    val state: StateFlow<CommentsUiState> = _state.asStateFlow()

    init {
        load()
    }

    /**
     * Fetches the page. Singular — there is no `loadMore` counterpart.
     *
     * The server returns no cursor with a comment list, so this is also the
     * only refresh path: re-running it replaces the whole list rather than
     * appending, which is the only merge strategy that is safe without a
     * stable pagination anchor.
     */
    fun load() {
        _state.value = CommentsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.getComments(postId)) {
                is AppResult.Success -> CommentsUiState.Content(result.data)
                is AppResult.Failure -> CommentsUiState.Error(
                    message = CommentsErrorText.forLoad(result.error),
                    // Retryability is a property of the failure, not of the
                    // screen, so it is answered in one place for the module.
                    retryable = PostErrorText.isRetryable(result.error),
                )
            }
        }
    }

    private companion object {
        const val POST_ID_KEY = "postId"
    }
}

/**
 * Load copy for this screen only.
 *
 * Separate from [PostErrorText] because the nouns differ and a shared string
 * table would have to say "content" to serve both — the generic word that makes
 * an error message useless. `isRetryable` is NOT duplicated here: that one is a
 * policy about failures rather than copy about a screen.
 */
internal object CommentsErrorText {

    fun forLoad(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        // The comments route is scoped to a post, so a 404 here means the post
        // went away, not that it simply has no comments — an empty list is a
        // 200 and never reaches this branch.
        is AppError.NotFound -> "This post isn't available. It may have been deleted."
        is AppError.AuthFailed -> "Please sign in again to see this."
        is AppError.Server -> "Something went wrong on our end. Try again shortly."
        else -> "We couldn't load the comments."
    }
}
