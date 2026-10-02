package com.us.android.feature.tube.ui.watch

import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.VideoLibraryStore
import com.us.android.core.model.FeedItem
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * What the action row under a long video does when a control is tapped
 * (2026-10-02): Like, Dislike, Watch later and Save.
 *
 * Apart from the ViewModel because the ViewModel owns a player and cannot be
 * built in a unit test; this holds no Android object, so the rules below are
 * tested directly.
 *
 * ## EVERY TAP IS OPTIMISTIC, AND EVERY REFUSAL IS SAID
 *
 * The control changes on the tap (through the two shared stores), the write
 * follows, and a refused write puts the control back AND leaves a line in
 * [message]. Before this, a failed Save on the watch screen rolled back with
 * no word at all, which reads as a button that does nothing.
 *
 * ## LIKE AND DISLIKE ARE NEVER LIT TOGETHER
 *
 * The app likes through `/reactions` (idempotent, so a retry is safe), and
 * post-service clears a dislike only when a like is made through its own
 * toggle; it removes a like on a dislike only when that like is in Redis,
 * which a `/reactions` like is not. So the two halves are both sent from
 * here: a like on a disliked video also removes the dislike, and a dislike
 * on a liked video also removes the like. Both are sent at once, so the
 * row never shows the two lit while one call waits for the other.
 */
class WatchEngagement @Inject constructor(
    private val engagement: EngagementStore,
    private val library: VideoLibraryStore,
) {
    private val _message = MutableStateFlow<UsMessage?>(null)

    /** The refusal to show over the screen; null when there is none. */
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    /** Watch later and dislike, this session's taps layered in. */
    val state: StateFlow<VideoLibraryState> = library.state

    fun dismissMessage() {
        _message.value = null
    }

    /** The post detail arrived: its Watch later and dislike values are the truth from here. */
    fun adopt(item: FeedItem) {
        library.adopt(item.id, serverQueued = item.viewer.isQueued, serverDisliked = item.viewer.hasDisliked)
        engagement.reconcile(
            postId = item.id,
            serverReacted = item.viewer.hasReacted,
            serverBookmarked = item.viewer.isBookmarked,
            serverReposted = item.viewer.hasReposted,
        )
    }

    suspend fun toggleLike(item: FeedItem) = coroutineScope {
        val liking = !isLiked(item)
        // A like on a disliked video takes the dislike off as it goes on.
        if (liking && isDisliked(item)) launch { writeDisliked(item, disliked = false) }
        engagement.toggleReaction(item.id, item.viewer.hasReacted)
        refusedEngagement(item.id, EngagementAction.REACTION, "Couldn't save your like. Try again.")
    }

    suspend fun toggleDislike(item: FeedItem) = coroutineScope {
        val disliking = !isDisliked(item)
        // A dislike on a liked video takes the like off as it goes on.
        if (disliking && isLiked(item)) {
            launch {
                engagement.toggleReaction(item.id, item.viewer.hasReacted)
                refusedEngagement(item.id, EngagementAction.REACTION, COULD_NOT_SAVE)
            }
        }
        writeDisliked(item, disliked = disliking)
    }

    suspend fun toggleWatchLater(item: FeedItem) {
        val queued = !library.state.value.queuedOr(item.id, item.viewer.isQueued)
        val result = library.setQueued(item.id, queued, serverQueued = item.viewer.isQueued)
        if (result is AppResult.Failure) {
            say(VideoLibraryRepository.errorMessage(result.error, "Couldn't update Watch later. Try again."))
        }
    }

    suspend fun toggleSave(item: FeedItem) {
        engagement.toggleBookmark(item.id, item.viewer.isBookmarked)
        refusedEngagement(item.id, EngagementAction.BOOKMARK, "Couldn't save this video. Try again.")
    }

    private fun isLiked(item: FeedItem): Boolean = engagement.overlayFor(item.id).reactedOr(item.viewer.hasReacted)

    private fun isDisliked(item: FeedItem): Boolean = library.state.value.dislikedOr(item.id, item.viewer.hasDisliked)

    private suspend fun writeDisliked(item: FeedItem, disliked: Boolean) {
        val result = library.setDisliked(item.id, disliked, serverDisliked = item.viewer.hasDisliked)
        if (result is AppResult.Failure) say(VideoLibraryRepository.errorMessage(result.error, COULD_NOT_SAVE))
    }

    /**
     * The engagement store has already rolled the control back and published
     * the failure; this says it and takes it off the shared list, so the
     * feed's failure bar does not show a watch-screen refusal later.
     */
    private fun refusedEngagement(postId: String, action: EngagementAction, text: String) {
        val failure = engagement.failures.value.firstOrNull { it.postId == postId && it.action == action } ?: return
        say(VideoLibraryRepository.errorMessage(failure.error, text))
        engagement.clearFailure(postId, action)
    }

    private fun say(text: String) {
        _message.value = UsMessage(text = text, type = UsMessageType.Error)
    }

    private companion object {
        const val COULD_NOT_SAVE = "Couldn't save that. Try again."
    }
}

/**
 * The list row the screen is playing, with the post detail's answers about
 * the VIEWER laid over it: saved, liked, in Watch later, disliked, the
 * counts and the author's switches. The row's media and everything the
 * player was prepared from are left alone, so the refresh never restarts
 * playback.
 */
internal fun FeedItem.withViewerStateOf(detail: FeedItem): FeedItem =
    copy(viewer = detail.viewer, counts = detail.counts, controls = detail.controls)
