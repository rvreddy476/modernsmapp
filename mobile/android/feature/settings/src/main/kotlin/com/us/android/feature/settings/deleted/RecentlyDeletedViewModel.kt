package com.us.android.feature.settings.deleted

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.DeletedPost
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.engagement.data.PostLifecycleRepository
import com.us.android.core.engagement.data.RestoreOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface RecentlyDeletedUiState {
    data object Loading : RecentlyDeletedUiState
    data class Error(val message: String) : RecentlyDeletedUiState

    /**
     * The list, empty or not — an empty list is the screen's "Nothing here",
     * not an error. [restoring] holds the ids whose restore is on the wire so
     * their buttons spin; [message] is the last refusal, cleared on the next tap.
     */
    data class Content(
        val posts: List<DeletedPost>,
        val nextCursor: String? = null,
        val loadingMore: Boolean = false,
        val restoring: Set<String> = emptySet(),
        val message: String? = null,
    ) : RecentlyDeletedUiState {
        val isEmpty: Boolean get() = posts.isEmpty()
        val hasMore: Boolean get() = nextCursor != null
    }
}

/**
 * Settings › Recently deleted: the viewer's soft-deleted posts and the
 * Restore that brings one back.
 *
 * A restore that lands removes the row AND clears the id from the shared
 * [HiddenPosts] set, which is what makes the post reappear in every feed
 * without a refresh — the same set the delete hid it through. "The window
 * has passed" also removes the row: the server will not bring that post
 * back, and a row that offers a Restore that cannot work is worse than
 * none. Only a transport failure keeps the row, with the reason above it.
 */
@HiltViewModel
class RecentlyDeletedViewModel @Inject constructor(
    private val lifecycle: PostLifecycleRepository,
    private val hidden: HiddenPosts,
) : ViewModel() {

    private val _state = MutableStateFlow<RecentlyDeletedUiState>(RecentlyDeletedUiState.Loading)
    val state: StateFlow<RecentlyDeletedUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = RecentlyDeletedUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = lifecycle.listDeleted(cursor = null)) {
                is AppResult.Success ->
                    RecentlyDeletedUiState.Content(posts = result.data.items, nextCursor = result.data.nextCursor)
                is AppResult.Failure -> RecentlyDeletedUiState.Error(result.error.loadMessage())
            }
        }
    }

    /** The next page, when the last row scrolls into view. Idempotent while one is in flight. */
    fun loadMore() {
        val current = _state.value as? RecentlyDeletedUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.loadingMore) return
        _state.value = current.copy(loadingMore = true)
        viewModelScope.launch {
            val result = lifecycle.listDeleted(cursor)
            _state.update { state ->
                val content = state as? RecentlyDeletedUiState.Content ?: return@update state
                when (result) {
                    is AppResult.Success -> content.copy(
                        // A row the server repeats across a page boundary is not doubled.
                        posts = content.posts + result.data.items.filterNot { new -> content.has(new.id) },
                        nextCursor = result.data.nextCursor,
                        loadingMore = false,
                    )
                    // A failed page keeps the cursor so the next scroll tries again.
                    is AppResult.Failure -> content.copy(loadingMore = false, message = result.error.loadMessage())
                }
            }
        }
    }

    fun restore(postId: String) {
        val current = _state.value as? RecentlyDeletedUiState.Content ?: return
        if (postId in current.restoring) return
        _state.value = current.copy(restoring = current.restoring + postId, message = null)
        viewModelScope.launch {
            val outcome = lifecycle.restorePost(postId)
            _state.update { state ->
                val content = state as? RecentlyDeletedUiState.Content ?: return@update state
                when (outcome) {
                    is RestoreOutcome.Restored -> {
                        hidden.unhidePost(postId)
                        content.without(postId)
                    }
                    RestoreOutcome.WindowPassed ->
                        content.without(postId).copy(message = "That post can no longer be restored.")
                    is RestoreOutcome.Failed ->
                        content.copy(restoring = content.restoring - postId, message = outcome.error.restoreMessage())
                }
            }
        }
    }

    fun dismissMessage() = _state.update { state ->
        (state as? RecentlyDeletedUiState.Content)?.copy(message = null) ?: state
    }

    private fun RecentlyDeletedUiState.Content.has(postId: String) = posts.any { it.id == postId }

    private fun RecentlyDeletedUiState.Content.without(postId: String) =
        copy(posts = posts.filterNot { it.id == postId }, restoring = restoring - postId)

    private fun AppError.loadMessage(): String = when (this) {
        is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Recently deleted needs a connection."
        else -> "Recently deleted couldn't be loaded."
    }

    private fun AppError.restoreMessage(): String = when (this) {
        is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Try again when you're connected."
        else -> "Couldn't restore this post. Try again."
    }
}
