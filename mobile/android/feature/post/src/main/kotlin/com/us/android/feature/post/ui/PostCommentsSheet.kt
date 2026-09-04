package com.us.android.feature.post.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
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
 * Post detail's comments — the same [UsCommentsSheet] the feed opens.
 *
 * Previously a pushed destination with its own top bar; now the sheet, so
 * the four places comments open (feed card, reels rail, media viewer, post
 * detail) look and behave identically. The post id comes from the SAME
 * `postId` argument the post route already carries, so this needs no route
 * of its own.
 */
@Composable
internal fun PostCommentsSheet(
    onDismiss: () -> Unit,
    viewModel: PostCommentsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.open() }

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
        postId = viewModel.postId,
        state = state,
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

@HiltViewModel
class PostCommentsViewModel @Inject constructor(
    repository: EngagementRepository,
    private val viewerSource: CommentsViewerSource,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    val postId: String = savedStateHandle.get<String>(POST_ID_KEY).orEmpty()

    private val controller = CommentsController(postId = postId, repository = repository)

    private val _state = MutableStateFlow(CommentsUiState())
    val state: StateFlow<CommentsUiState> = _state.asStateFlow()

    /** First open: the list, and the viewer's avatar alongside it, not before it. */
    fun open() {
        refresh()
        viewModelScope.launch { _state.value = controller.setViewer(viewerSource.current()) }
    }

    fun refresh() = viewModelScope.launch { _state.value = controller.refresh() }

    fun loadMore() = viewModelScope.launch { _state.value = controller.loadMore() }

    /** Synchronous: a coroutine hop here drops characters from a fast typist. */
    fun onDraftChange(text: String) {
        _state.value = controller.onDraftChange(text)
    }

    fun submit() = viewModelScope.launch { _state.value = controller.submit() }

    fun quickReaction(emoji: String) = viewModelScope.launch { _state.value = controller.quickReaction(emoji) }

    private companion object {
        const val POST_ID_KEY = "postId"
    }
}
