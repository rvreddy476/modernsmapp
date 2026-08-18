package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.ProfileRepository
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
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
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
            (_state.value as? PostUiState.Content)?.let { content ->
                loadAuthor(content.post.authorId)
                loadMedia(content.post.media.firstOrNull()?.mediaId)
            }
        }
    }

    /**
     * Fills in the author header after the post is already on screen.
     *
     * A failure is swallowed on purpose. The name is decoration around content
     * the viewer already has; turning a missing profile into an error state
     * would replace a readable post with a retry button.
     */
    private suspend fun loadAuthor(authorId: String) {
        if (authorId.isBlank()) return
        val profile = (profiles.getProfile(authorId) as? AppResult.Success)?.data ?: return
        _state.update { state ->
            (state as? PostUiState.Content)?.copy(author = profile) ?: state
        }
    }

    /**
     * Resolves the first attachment.
     *
     * Separate from the post load, and its failure is swallowed, for the same
     * reason as the author: the text is already on screen and a missing image
     * must not replace it with a retry button.
     *
     * Only the FIRST asset. A carousel needs a pager and a per-page resolve;
     * fetching all of them up front would spend the reader's data on images
     * they may never swipe to.
     */
    private suspend fun loadMedia(mediaId: String?) {
        if (mediaId.isNullOrBlank()) return
        val delivery = (media.delivery(mediaId) as? AppResult.Success)?.data ?: return
        _state.update { state ->
            (state as? PostUiState.Content)?.copy(media = delivery) ?: state
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
     * Save or unsave.
     *
     * Optimistic since the 2026-08-17 repair. This was previously the one
     * interaction here that deliberately waited: the endpoint was a toggle
     * whose outcome depended on the server's current state rather than on the
     * request, so an optimistic flip could show the exact opposite of the
     * truth after a lost response. It is now a SET/CLEAR pair — the client
     * states the state it wants, repetition is harmless, and the reversal
     * endpoint works — so the flip is safe and the spinner is no longer worth
     * its cost.
     */
    fun onBookmarkToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (content.busy) return
        val wasBookmarked = content.post.viewer.isBookmarked

        _state.update { state ->
            (state as? PostUiState.Content)?.withBookmark(!wasBookmarked, busy = true) ?: state
        }

        viewModelScope.launch {
            val result = repository.setBookmarked(postId, !wasBookmarked)
            _state.update { state ->
                val current = state as? PostUiState.Content ?: return@update state
                when (result) {
                    // Adopt the server's value rather than the requested one.
                    // With set/clear the two agree, and asserting that here
                    // means a future divergence surfaces instead of hiding.
                    is AppResult.Success -> current.withBookmark(result.data, busy = false)

                    is AppResult.Failure -> {
                        current.withBookmark(wasBookmarked, busy = false)
                            .copy(actionError = PostErrorText.forAction(result.error))
                    }
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

private fun PostUiState.Content.withBookmark(
    bookmarked: Boolean,
    busy: Boolean,
): PostUiState.Content = copy(
    post = post.copy(viewer = post.viewer.copy(isBookmarked = bookmarked)),
    busy = busy,
    actionError = null,
)

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
