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
import com.us.android.core.engagement.data.CommentActionsRepository
import com.us.android.core.engagement.data.CommentMentionSource
import com.us.android.core.engagement.data.CommentsActions
import com.us.android.core.engagement.data.CommentsController
import com.us.android.core.engagement.data.CommentsSession
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.CommentsViewerSource
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.ui.UsCommentsCallbacks
import com.us.android.core.ui.UsCommentsSheet
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * Post detail's comments — the same [UsCommentsSheet] the feed opens.
 *
 * Previously a pushed destination with its own top bar; now the sheet, so
 * the four places comments open (feed card, reels rail, media viewer, post
 * detail) look and behave identically. The post id comes from the SAME
 * `postId` argument the post route already carries, so this needs no route
 * of its own. [postAuthorId] (the loaded post's author) gives the creator
 * Pin and Heart.
 */
@Composable
internal fun PostCommentsSheet(
    onDismiss: () -> Unit,
    postAuthorId: String = "",
    viewModel: PostCommentsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.open(postAuthorId) }

    val callbacks = remember(viewModel) { UsCommentsCallbacks.of(viewModel.actions) }
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
    commentActions: CommentActionsRepository,
    reports: ReportRepository,
    mentionSource: CommentMentionSource,
    private val viewerSource: CommentsViewerSource,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    val postId: String = savedStateHandle.get<String>(POST_ID_KEY).orEmpty()

    private val session = CommentsSession(viewModelScope).apply {
        attach(CommentsController(postId, repository, commentActions, reports, mentionSource))
    }

    val state: StateFlow<CommentsUiState> = session.state

    /** The verbs the sheet's callbacks call. */
    val actions: CommentsActions get() = session

    /** First open: the list, and the viewer's avatar alongside it, not before it. */
    fun open(postAuthorId: String = "") = session.open(postAuthorId, viewerSource)

    private companion object {
        const val POST_ID_KEY = "postId"
    }
}
