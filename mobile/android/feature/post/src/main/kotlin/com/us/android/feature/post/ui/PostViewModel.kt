package com.us.android.feature.post.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.feature.post.data.PostRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class PostViewModel @Inject constructor(
    private val repository: PostRepository,
    private val profiles: ProfileRepository,
    private val media: MediaRepository,
    private val engagement: EngagementStore,
    private val engagementRepository: EngagementRepository,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val postId: String = savedStateHandle.get<String>(POST_ID_KEY).orEmpty()

    private val _state = MutableStateFlow<PostUiState>(PostUiState.Loading)
    val state: StateFlow<PostUiState> = _state.asStateFlow()

    init {
        // Republish the shared overlay into this screen's state so post
        // detail and the feed can never disagree about the same post.
        viewModelScope.launch {
            engagement.overlays.collect { all ->
                val overlay = all[postId] ?: EngagementOverlay()
                _state.update { state ->
                    (state as? PostUiState.Content)?.copy(overlay = overlay) ?: state
                }
            }
        }
    }

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
                // Retire local intent this response has now confirmed. Without
                // a production caller the overlay would outlive its purpose
                // and keep re-applying a value the server already agrees with,
                // pinning it against later changes made elsewhere.
                engagement.reconcile(
                    postId = postId,
                    serverReacted = content.post.viewer.hasReacted,
                    serverBookmarked = content.post.viewer.isBookmarked,
                    serverReposted = content.post.viewer.hasReposted,
                )
                loadAuthor(content.post.authorId)
                // Resolve the first page eagerly so the post is not blank while
                // the reader decides whether to swipe. The rest arrive as they
                // are reached; see onPageSettled.
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
        val alreadyResolved = (_state.value as? PostUiState.Content)?.media?.containsKey(mediaId)
        if (alreadyResolved == true) return
        val delivery = (media.delivery(mediaId) as? AppResult.Success)?.data ?: return
        _state.update { state ->
            (state as? PostUiState.Content)?.let { it.copy(media = it.media + (mediaId to delivery)) }
                ?: state
        }
    }

    /**
     * Resolve the page the reader just swiped to.
     *
     * Called by the pager rather than on load, so a ten-page carousel costs one
     * image up front instead of ten. [loadMedia] is idempotent, so swiping back
     * and forth re-resolves nothing.
     */
    fun onPageSettled(index: Int) {
        val content = _state.value as? PostUiState.Content ?: return
        val mediaId = content.post.media.getOrNull(index)?.mediaId ?: return
        viewModelScope.launch { loadMedia(mediaId) }
    }

    /**
     * Like or unlike, save, and repost.
     *
     * All three delegate to the shared [EngagementStore] rather than keeping
     * their own optimistic copy. Two reasons:
     *
     *  - ONE ANSWER PER POST. The feed and this screen showed the same post
     *    with independent local state, so liking in one and opening the other
     *    displayed the opposite value until a refresh.
     *  - ORDERING. The previous implementation rolled back on any failure,
     *    including a stale one, which meant a failed unlike arriving after a
     *    successful like restored "liked" while the server held "not liked".
     *    The store discards responses that a newer tap has superseded.
     *
     * The `busy` flag is gone with them: it blocked the second tap of a rapid
     * double-tap, which is a real thing users do to undo a mistap.
     */
    fun onReactToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (!content.post.allowsReactions) return
        viewModelScope.launch {
            engagement.toggleReaction(postId, content.post.viewer.hasReacted)
        }
    }

    fun onBookmarkToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        viewModelScope.launch {
            engagement.toggleBookmark(postId, content.post.viewer.isBookmarked)
        }
    }

    /**
     * Repost or undo.
     *
     * Passes the SERVER's `has_reposted`. It used to pass a hardcoded `false`,
     * so the first tap on a post this viewer had already reposted sent another
     * POST, took `409 ALREADY_REPOSTED`, rolled back, and left no way to undo
     * the repost at all.
     */
    fun onRepostToggle() {
        val content = _state.value as? PostUiState.Content ?: return
        if (!content.post.isRepostable) return
        viewModelScope.launch {
            engagement.toggleRepost(postId, content.post.viewer.hasReposted)
        }
    }

    /**
     * Failed mutations for THIS post.
     *
     * Filtered to the post on screen: the store is shared, and a failure from
     * a row the viewer liked in the feed must not appear as an error on an
     * unrelated post they then opened.
     */
    val failures: StateFlow<List<EngagementFailure>> = engagement.failures
        .map { all -> all.filter { it.postId == postId } }
        .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    fun retryFailure(postId: String, action: EngagementAction) = viewModelScope.launch {
        engagement.retry(postId, action)
    }

    fun dismissFailure(postId: String, action: EngagementAction) =
        engagement.clearFailure(postId, action)

    /** Records an external share once, after the chooser was launched. */
    fun onExternalShared() = viewModelScope.launch {
        engagementRepository.recordExternalShare(postId)
    }

    fun dismissActionError() = _state.update {
        (it as? PostUiState.Content)?.copy(actionError = null) ?: it
    }

    private companion object {
        const val POST_ID_KEY = "postId"
    }
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
