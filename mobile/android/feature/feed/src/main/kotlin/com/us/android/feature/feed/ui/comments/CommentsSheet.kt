package com.us.android.feature.feed.ui.comments

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.CommentsViewerSource
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.ui.UsCommentsCallbacks
import com.us.android.core.ui.UsCommentsSheet
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Comments for one post, over the post that is being discussed.
 *
 * A sheet rather than a destination: navigating away loses the reader's place
 * in the feed and hides the post the conversation is about. The feed card,
 * the reels rail and the in-place media viewer all open this; the UI itself
 * is [UsCommentsSheet], shared with post detail, so the four cannot drift.
 */
@Composable
fun CommentsSheet(
    postId: String,
    onDismiss: () -> Unit,
    viewModel: CommentsViewModel = hiltViewModel(key = "comments:$postId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // Keyed on postId so opening a different post's comments reloads rather
    // than showing the previous post's conversation.
    LaunchedEffect(postId) { viewModel.bind(postId) }

    val callbacks = remember(viewModel) {
        UsCommentsCallbacks(
            onDraftChange = viewModel::onDraftChange,
            onSubmit = viewModel::submit,
            onQuickReaction = viewModel::quickReaction,
            onLoadMore = viewModel::loadMore,
            onRetryAppend = viewModel::loadMore,
            onRetryRefresh = viewModel::refresh,
        )
    }
    UsCommentsSheet(
        postId = postId,
        state = state,
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

/**
 * Owns one [CommentsController] and republishes its snapshots.
 *
 * The controller is deliberately not a ViewModel itself: the same logic backs
 * this sheet and post detail's sheet, and a ViewModel would tie it to
 * whichever of those owns the lifecycle.
 */
@HiltViewModel
class CommentsViewModel @Inject constructor(
    private val repository: EngagementRepository,
    private val viewerSource: CommentsViewerSource,
) : ViewModel() {

    private val _state = MutableStateFlow(CommentsUiState())
    val state: StateFlow<CommentsUiState> = _state.asStateFlow()

    private var controller: CommentsController? = null
    private var boundPostId: String? = null

    fun bind(postId: String) {
        if (boundPostId == postId) return
        boundPostId = postId
        controller = CommentsController(postId, repository)
        refresh()
        // The viewer's avatar is cosmetic: it is fetched alongside the list
        // rather than before it, so a slow profile call never delays comments.
        withController { _state.value = it.setViewer(viewerSource.current()) }
    }

    fun refresh() = withController { _state.value = it.refresh() }

    fun loadMore() = withController { _state.value = it.loadMore() }

    /**
     * Draft changes are synchronous — routing them through a coroutine would
     * let a fast typist outrun the state and drop characters.
     */
    fun onDraftChange(text: String) {
        controller?.let { _state.value = it.onDraftChange(text) }
    }

    fun submit() = withController { _state.value = it.submit() }

    fun quickReaction(emoji: String) = withController { _state.value = it.quickReaction(emoji) }

    private fun withController(block: suspend (CommentsController) -> Unit) {
        val current = controller ?: return
        viewModelScope.launch { block(current) }
    }
}
