package com.us.android.feature.post.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
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
 * The full-screen comments destination.
 *
 * Renders the same [CommentsPanel] as the bottom sheet over the feed. Two
 * presentations, one implementation: paging, validation, idempotency and
 * retry cannot drift between "comments from the feed" and "comments from post
 * detail", because there is only one of each.
 */
@Composable
fun CommentsScreen(
    onBack: () -> Unit,
    viewModel: PostCommentsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.refresh() }

    UsScaffold(
        topBar = { UsTopBar(title = "Comments", onBack = onBack) },
        applyPageGutter = false,
    ) {
        CommentsPanel(
            state = state,
            onDraftChange = viewModel::onDraftChange,
            onSubmit = viewModel::submit,
            onLoadMore = viewModel::loadMore,
            onRetryAppend = viewModel::loadMore,
            onRetryRefresh = viewModel::refresh,
            modifier = Modifier,
        )
    }
}

@HiltViewModel
class PostCommentsViewModel @Inject constructor(
    repository: EngagementRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val controller = CommentsController(
        postId = savedStateHandle.get<String>(POST_ID_KEY).orEmpty(),
        repository = repository,
    )

    private val _state = MutableStateFlow(CommentsUiState())
    val state: StateFlow<CommentsUiState> = _state.asStateFlow()

    fun refresh() = viewModelScope.launch { _state.value = controller.refresh() }

    fun loadMore() = viewModelScope.launch { _state.value = controller.loadMore() }

    /** Synchronous: a coroutine hop here drops characters from a fast typist. */
    fun onDraftChange(text: String) {
        _state.value = controller.onDraftChange(text)
    }

    fun submit() = viewModelScope.launch { _state.value = controller.submit() }

    private companion object {
        const val POST_ID_KEY = "postId"
    }
}
