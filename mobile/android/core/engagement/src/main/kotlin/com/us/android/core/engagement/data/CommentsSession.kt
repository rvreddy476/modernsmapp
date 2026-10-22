package com.us.android.core.engagement.data

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Everything the comments UI can ask for — the verbs [CommentsSession]
 * performs. An interface so the UI's callback bundle is built from it in
 * one place (`:core:ui`) rather than once per host.
 */
@Suppress("TooManyFunctions")
interface CommentsActions {
    fun onDraftChange(text: String)
    fun submit()
    fun quickReaction(emoji: String)
    fun loadMore()
    fun refresh()
    fun toggleReplies(parentId: String)
    fun loadMoreReplies(parentId: String)
    fun startReply(commentId: String)
    fun cancelReply()
    fun toggleLike(commentId: String)
    fun delete(commentId: String)

    /** The ⋮'s Edit (2026-10-22): the viewer's own comment goes into the composer. */
    fun startEdit(commentId: String)
    fun cancelEdit()
    fun togglePin(commentId: String)
    fun toggleHeart(commentId: String)
    fun report(commentId: String, reason: String, details: String)
    fun resetReport()
    fun dismissNotice()
    fun pickMention(candidate: MentionCandidate)
}

/**
 * The coroutine side of the comments sheet, shared by every host (the
 * feed's, reels', PostTube's and post detail's ViewModels), so the wiring
 * is written once: each verb runs the [CommentsController] in [scope] and
 * republishes its snapshot.
 *
 * [attach] swaps the controller when a host is re-bound to another post.
 */
@Suppress("TooManyFunctions")
class CommentsSession(private val scope: CoroutineScope) : CommentsActions {

    private val _state = MutableStateFlow(CommentsUiState())
    val state: StateFlow<CommentsUiState> = _state.asStateFlow()

    private var controller: CommentsController? = null
    private var mentionJob: Job? = null

    fun attach(controller: CommentsController) {
        this.controller?.onChange = null
        this.controller = controller
        mentionJob?.cancel()
        // Every change, optimistic ones included, reaches the screen as it
        // happens — not only when the request behind it has been answered.
        controller.onChange = { next -> if (this.controller === controller) _state.value = next }
        _state.value = controller.snapshot()
    }

    /**
     * First open: the list, and the viewer alongside it rather than before
     * it, so a slow profile call never delays the comments.
     */
    fun open(postAuthorId: String, viewerSource: CommentsViewerSource) {
        val current = controller ?: return
        _state.value = current.setPostAuthor(postAuthorId)
        refresh()
        launchOn { it.setViewer(viewerSource.current()) }
    }

    /**
     * Synchronous: a coroutine hop here would let a fast typist outrun the
     * state and drop characters. The `@` lookup is debounced behind it.
     */
    override fun onDraftChange(text: String) {
        val current = controller ?: return
        val next = current.onDraftChange(text)
        _state.value = next
        mentionJob?.cancel()
        if (next.mentionQuery != null) {
            mentionJob = scope.launch {
                delay(MENTION_DEBOUNCE_MILLIS)
                _state.value = current.searchMentions()
            }
        }
    }

    override fun submit() = launchOn { it.submit() }
    override fun quickReaction(emoji: String) = launchOn { it.quickReaction(emoji) }
    override fun loadMore() = launchOn { it.loadMore() }
    override fun refresh() = launchOn { it.refresh() }
    override fun toggleReplies(parentId: String) = launchOn { it.toggleReplies(parentId) }
    override fun loadMoreReplies(parentId: String) = launchOn { it.loadReplies(parentId) }
    override fun startReply(commentId: String) = syncOn { it.startReply(commentId) }
    override fun cancelReply() = syncOn { it.cancelReply() }
    override fun toggleLike(commentId: String) = launchOn { it.toggleLike(commentId) }
    override fun delete(commentId: String) = launchOn { it.delete(commentId) }
    override fun startEdit(commentId: String) = syncOn { it.startEdit(commentId) }
    override fun cancelEdit() = syncOn { it.cancelEdit() }
    override fun togglePin(commentId: String) = launchOn { it.togglePin(commentId) }
    override fun toggleHeart(commentId: String) = launchOn { it.toggleHeart(commentId) }
    override fun report(commentId: String, reason: String, details: String) =
        launchOn { it.report(commentId, reason, details) }

    override fun resetReport() = syncOn { it.resetReport() }
    override fun dismissNotice() = syncOn { it.dismissNotice() }
    override fun pickMention(candidate: MentionCandidate) {
        mentionJob?.cancel()
        syncOn { it.pickMention(candidate) }
    }

    private fun launchOn(block: suspend (CommentsController) -> CommentsUiState) {
        val current = controller ?: return
        scope.launch {
            val next = block(current)
            // A controller replaced meanwhile (another post) must not overwrite
            // the new conversation with the old one's answer.
            if (controller === current) _state.value = next
        }
    }

    private fun syncOn(block: (CommentsController) -> CommentsUiState) {
        controller?.let { _state.value = block(it) }
    }

    private companion object {
        /** The web's mention debounce. */
        const val MENTION_DEBOUNCE_MILLIS = 250L
    }
}
