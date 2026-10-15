package com.us.android.core.feed.ui.comments

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.hilt.navigation.compose.hiltViewModel
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
 * Comments for one post, over the post that is being discussed.
 *
 * A sheet rather than a destination: navigating away loses the reader's place
 * in the feed and hides the post the conversation is about. The feed card,
 * the reels rail, PostTube's watch page and the in-place media viewer all
 * open this; the UI itself is [UsCommentsSheet], shared with post detail, so
 * they cannot drift.
 *
 * [postAuthorId] is the post's author when the host knows it: it is what
 * gives the creator Pin and Heart on their own post's comments. Blank keeps
 * every other action and simply offers no creator rows.
 */
@Composable
fun CommentsSheet(
    postId: String,
    onDismiss: () -> Unit,
    postAuthorId: String = "",
    viewModel: CommentsViewModel = hiltViewModel(key = "comments:$postId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // Keyed on postId so opening a different post's comments reloads rather
    // than showing the previous post's conversation.
    LaunchedEffect(postId) { viewModel.bind(postId, postAuthorId) }

    val callbacks = remember(viewModel) { UsCommentsCallbacks.of(viewModel.actions) }
    UsCommentsSheet(
        postId = postId,
        state = state,
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

/**
 * Owns one [CommentsSession] — the controller for the bound post and the
 * coroutine wiring every comments host shares.
 *
 * The controller is deliberately not a ViewModel itself: the same logic backs
 * this sheet and post detail's sheet, and a ViewModel would tie it to
 * whichever of those owns the lifecycle.
 */
@HiltViewModel
class CommentsViewModel @Inject constructor(
    private val repository: EngagementRepository,
    private val commentActions: CommentActionsRepository,
    private val reports: ReportRepository,
    private val viewerSource: CommentsViewerSource,
    private val mentionSource: CommentMentionSource,
) : ViewModel() {

    private val session = CommentsSession(viewModelScope)

    val state: StateFlow<CommentsUiState> = session.state

    /** The verbs the sheet's callbacks call. */
    val actions: CommentsActions get() = session

    private var boundPostId: String? = null

    fun bind(postId: String, postAuthorId: String = "") {
        if (boundPostId == postId) return
        boundPostId = postId
        session.attach(CommentsController(postId, repository, commentActions, reports, mentionSource))
        session.open(postAuthorId, viewerSource)
    }
}
