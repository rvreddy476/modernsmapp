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
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class PostViewModel @Inject constructor(
    private val repository: PostRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val postId: String = savedStateHandle.get<String>(POST_ID_KEY).orEmpty()

    private val _state = MutableStateFlow<PostUiState>(PostUiState.Loading)
    val state: StateFlow<PostUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = PostUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.getPost(postId)) {
                is AppResult.Success -> PostUiState.Content(post = result.data)
                is AppResult.Failure -> PostUiState.Error(
                    message = PostErrorText.forLoad(result.error),
                    retryable = PostErrorText.isRetryable(result.error),
                )
            }
        }
    }

    /**
     * Like or unlike.
     *
     * Optimistic, with the count moved alongside the flag so the number and
     * the icon never disagree mid-flight. Add and remove are distinct
     * endpoints here, so unlike a bookmark this one is safe to reason about
     * from the request that was made.
     */
    fun onReactToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (content.busy || !content.post.allowsReactions) return
        val wasReacted = content.post.viewer.hasReacted

        _state.update { state ->
            (state as? PostUiState.Content)?.withReaction(!wasReacted, busy = true) ?: state
        }

        viewModelScope.launch {
            val result = if (wasReacted) {
                repository.removeReaction(postId)
            } else {
                repository.addReaction(postId)
            }
            _state.update { state ->
                val current = state as? PostUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> current.copy(busy = false)
                    is AppResult.Failure -> {
                        current.withReaction(wasReacted, busy = false)
                            .copy(actionError = PostErrorText.forAction(result.error))
                    }
                }
            }
        }
    }

    /**
     * Bookmark.
     *
     * NOT optimistic, and that is deliberate — it is the one interaction here
     * where guessing is unsafe. The endpoint is a toggle with no separate add
     * and remove, so the outcome is a function of the server's current state
     * rather than of the request. If two taps race, or a response is lost and
     * the user taps again, an optimistic flip can end up showing the exact
     * opposite of the truth.
     *
     * So the UI waits and then adopts the boolean the server returned. The
     * cost is a brief spinner; the alternative is a saved-posts list that
     * silently disagrees with what the user sees.
     *
     * There is also no retry and no offline queue for the same reason: a
     * replayed toggle un-bookmarks what the user saved. The reversal endpoint,
     * DELETE /v1/posts/:id/bookmark, is broken server-side (500, missing
     * `bookmarks` relation), so a second POST is the only working undo.
     */
    fun onBookmarkToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (content.busy) return

        _state.update {
            (it as? PostUiState.Content)?.copy(busy = true, actionError = null) ?: it
        }

        viewModelScope.launch {
            val result = repository.toggleBookmark(postId)
            _state.update { state ->
                val current = state as? PostUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> current.copy(
                        post = current.post.copy(
                            // The server's answer, not the flip we intended.
                            viewer = current.post.viewer.copy(isBookmarked = result.data),
                        ),
                        busy = false,
                    )

                    is AppResult.Failure -> current.copy(
                        busy = false,
                        actionError = PostErrorText.forAction(result.error),
                    )
                }
            }
        }
    }

    /**
     * Repost or undo.
     *
     * [PostUiState.Content.hasReposted] tracks only what happened in this
     * session. The post payload carries a `repost_count` but no per-viewer
     * flag, so on a cold start the client genuinely cannot know whether this
     * viewer already reposted, and the control starts in the un-reposted
     * state. That is a contract gap, not a client shortcut.
     */
    fun onRepostToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (content.busy || !content.post.isRepostable) return
        val wasReposted = content.hasReposted

        _state.update {
            (it as? PostUiState.Content)?.copy(busy = true, actionError = null) ?: it
        }

        viewModelScope.launch {
            val result = if (wasReposted) {
                repository.removeRepost(postId)
            } else {
                repository.repost(postId)
            }
            _state.update { state ->
                val current = state as? PostUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> current.copy(
                        hasReposted = !wasReposted,
                        post = current.post.copy(
                            counts = current.post.counts.copy(
                                reposts = (current.post.counts.reposts + if (wasReposted) -1 else 1)
                                    .coerceAtLeast(0),
                            ),
                        ),
                        busy = false,
                    )

                    is AppResult.Failure -> current.copy(
                        busy = false,
                        actionError = PostErrorText.forAction(result.error),
                    )
                }
            }
        }
    }

    fun dismissActionError() = _state.update {
        (it as? PostUiState.Content)?.copy(actionError = null) ?: it
    }

    private companion object {
        const val POST_ID_KEY = "postId"
    }
}

/** Moves the flag and the count together so they cannot disagree. */
private fun PostUiState.Content.withReaction(
    reacted: Boolean,
    busy: Boolean,
): PostUiState.Content {
    val delta = if (reacted) 1 else -1
    return copy(
        post = post.copy(
            viewer = post.viewer.copy(hasReacted = reacted),
            counts = post.counts.copy(likes = (post.counts.likes + delta).coerceAtLeast(0)),
        ),
        busy = busy,
        actionError = null,
    )
}

/** Error text, scoped to this screen — see ProfileErrorText for the rationale. */
internal object PostErrorText {

    fun forLoad(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. Check your connection and try again."
        is AppError.Timeout -> "That took too long. Try again."
        is AppError.NotFound -> "This post isn't available. It may have been deleted."
        is AppError.AuthFailed -> "Please sign in again to see this."
        is AppError.Server -> "Something went wrong on our end. Try again shortly."
        else -> "We couldn't load this post."
    }

    fun forAction(error: AppError): String = when (error) {
        is AppError.NoNetwork -> "You're offline. That didn't go through."
        is AppError.Timeout -> "That took too long. Try again."
        is AppError.AuthFailed -> "Please sign in again."
        is AppError.RateLimited -> "You're doing that too quickly. Wait a moment."
        else -> "That didn't go through. Try again."
    }

    fun isRetryable(error: AppError): Boolean = when (error) {
        is AppError.NotFound,
        is AppError.Forbidden,
        is AppError.InvalidRequest,
        -> false
        else -> true
    }
}
