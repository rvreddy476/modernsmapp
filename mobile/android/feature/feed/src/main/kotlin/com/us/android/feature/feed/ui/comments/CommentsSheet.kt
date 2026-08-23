package com.us.android.feature.feed.ui.comments

import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.ui.CommentsPanel
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
 * in the feed and hides the post the conversation is about.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CommentsSheet(
    postId: String,
    onDismiss: () -> Unit,
    viewModel: CommentsViewModel = hiltViewModel(key = "comments:$postId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    // Keyed on postId so opening a different post's comments reloads rather
    // than showing the previous post's conversation.
    LaunchedEffect(postId) { viewModel.bind(postId) }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        modifier = Modifier.testTag("comments_sheet"),
    ) {
        CommentsPanel(
            state = state,
            onDraftChange = viewModel::onDraftChange,
            onSubmit = viewModel::submit,
            onLoadMore = viewModel::loadMore,
            onRetryAppend = viewModel::loadMore,
            onRetryRefresh = viewModel::refresh,
        )
    }
}

/**
 * Owns one [CommentsController] and republishes its snapshots.
 *
 * The controller is deliberately not a ViewModel itself: the same logic backs
 * this sheet and a full comments screen from post detail, and a ViewModel
 * would tie it to whichever of those owns the lifecycle.
 */
@HiltViewModel
class CommentsViewModel @Inject constructor(
    private val repository: EngagementRepository,
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

    private fun withController(block: suspend (CommentsController) -> Unit) {
        val current = controller ?: return
        viewModelScope.launch { block(current) }
    }
}
