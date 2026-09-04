package com.us.android.feature.feed.ui.more

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.engagement.data.PostLifecycleRepository
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.ui.UsPostDeleteState
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReportReason
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.FollowGraph
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * What the post "more" sheet does when a row is tapped — one ViewModel
 * behind the sheet on every surface (Home, Friends, a tag's posts, the
 * in-place viewer, Reels), so "Not interested" on a reel and on a card are
 * the same call and the same rule.
 *
 * ## OPTIMISTIC, LIKE EVERYTHING ELSE HERE
 *
 * "Not interested" and Block remove rows at once through [HiddenPosts] and
 * tell the server afterwards; a refusal puts the rows back and says so. A
 * viewer who taps "Not interested" and watches the post sit there for a
 * round trip has been given a button that does nothing.
 *
 * Save goes through the shared [EngagementStore] — the same lane the card's
 * bookmark glyph uses, so the sheet and the glyph can never disagree.
 * Follow and Unfollow go through [FollowGraph], which the card header and
 * the reel overlay already read. Report and Delete are the two actions that
 * WAIT: the sheet shows the outcome, and "you already reported this" is an
 * outcome, not an error. Delete waits because a post that vanished and then
 * came back "couldn't be deleted" reads as a lie.
 */
@HiltViewModel
// Constructor injection of every collaborator the sheet's rows need; a
// wrapper would add indirection, not clarity.
@Suppress("LongParameterList")
class PostMoreViewModel @Inject constructor(
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val follows: FollowGraph,
    private val profiles: ProfileRepository,
    private val feed: FeedRepository,
    private val reports: ReportRepository,
    private val hidden: HiddenPosts,
    private val lifecycle: PostLifecycleRepository,
) : ViewModel() {

    private val _report = MutableStateFlow<UsPostReportState>(UsPostReportState.Idle)

    /** The report step's progress for the post the sheet is open on. */
    val report: StateFlow<UsPostReportState> = _report.asStateFlow()

    private val _delete = MutableStateFlow<UsPostDeleteState>(UsPostDeleteState.Idle)

    /** The delete's progress for the viewer's own post the sheet is open on. */
    val delete: StateFlow<UsPostDeleteState> = _delete.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)

    /** What the HOST shows after the sheet has closed: a confirmation, or a refusal. */
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    /** Author id → the viewer's edge, for the Follow / Unfollow row. */
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges

    val ownUserId: String get() = follows.ownId

    /** The sheet opened on a post: nothing from an earlier report or delete carries over. */
    fun opened() {
        _report.value = UsPostReportState.Idle
        _delete.value = UsPostDeleteState.Idle
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun toggleSave(item: FeedItem) = viewModelScope.launch {
        engagement.toggleBookmark(item.id, item.viewer.isBookmarked)
    }

    /** Recorded AFTER the chooser was launched; a failed count is not the viewer's problem. */
    fun externalShared(postId: String) = viewModelScope.launch {
        shares.recordExternalShare(postId)
    }

    fun interested(item: FeedItem) {
        // The undo of an earlier "Not interested" this session, if there was one.
        hidden.unhidePost(item.id)
        say("We'll show you more posts like this", UsMessageType.Success)
        viewModelScope.launch {
            if (feed.sendFeedback(item.id, interested = true) is AppResult.Failure) {
                say(COULD_NOT_SAVE, UsMessageType.Error)
            }
        }
    }

    fun notInterested(item: FeedItem) {
        hidden.hidePost(item.id)
        say("We'll show you fewer posts like this", UsMessageType.Success)
        viewModelScope.launch {
            if (feed.sendFeedback(item.id, interested = false) is AppResult.Failure) {
                hidden.unhidePost(item.id)
                say(COULD_NOT_SAVE, UsMessageType.Error)
            }
        }
    }

    fun follow(authorId: String) = viewModelScope.launch {
        if (follows.follow(authorId) is AppResult.Failure) say("Couldn't follow. Try again.", UsMessageType.Error)
    }

    fun unfollow(authorId: String) = viewModelScope.launch {
        if (follows.unfollow(authorId) is AppResult.Failure) say("Couldn't unfollow. Try again.", UsMessageType.Error)
    }

    /**
     * Confirmed in the sheet. Every post by the author leaves every list at
     * once; the server's block also severs the follow edge, which the graph
     * will learn on its next lookup.
     */
    fun block(item: FeedItem) {
        val authorId = item.author.id
        hidden.hideAuthor(authorId)
        say("Blocked @${item.author.username ?: item.author.nameForDisplay}", UsMessageType.Success)
        viewModelScope.launch {
            if (profiles.block(authorId) is AppResult.Failure) {
                hidden.unhideAuthor(authorId)
                say("Couldn't block. Try again.", UsMessageType.Error)
            }
        }
    }

    fun report(item: FeedItem, reason: UsReportReason, details: String) {
        if (_report.value == UsPostReportState.Sending) return
        _report.value = UsPostReportState.Sending
        viewModelScope.launch {
            _report.value = when (reports.reportPost(item.id, reason.wire, details)) {
                ReportOutcome.Filed -> UsPostReportState.Sent
                ReportOutcome.AlreadyReported -> UsPostReportState.AlreadyReported
                is ReportOutcome.Failed -> UsPostReportState.Failed
            }
        }
    }

    /**
     * Confirmed in the sheet. The ONE action here that waits for the server
     * before touching the list: a delete that is refused must not have made
     * the post vanish first, because the viewer would read the vanish as the
     * deed done. On 204 the post leaves every list at once through
     * [HiddenPosts] — the same set the feeds already filter on — and the
     * sheet shows "Post deleted". A 404 is a post that is already gone, which
     * is the same outcome from where the viewer stands.
     */
    fun delete(item: FeedItem) {
        if (_delete.value == UsPostDeleteState.Deleting) return
        _delete.value = UsPostDeleteState.Deleting
        viewModelScope.launch {
            _delete.value = when (val result = lifecycle.deletePost(item.id)) {
                is AppResult.Success -> deleted(item.id)
                is AppResult.Failure -> when (result.error) {
                    is AppError.NotFound -> deleted(item.id)
                    is AppError.Forbidden -> UsPostDeleteState.Failed("You can only delete your own posts.")
                    is AppError.NoNetwork, is AppError.Timeout ->
                        UsPostDeleteState.Failed("You're offline. Try again when you're connected.")
                    else -> UsPostDeleteState.Failed(COULD_NOT_DELETE)
                }
            }
        }
    }

    private fun deleted(postId: String): UsPostDeleteState {
        hidden.hidePost(postId)
        return UsPostDeleteState.Deleted
    }

    private fun say(text: String, type: UsMessageType) {
        _message.value = UsMessage(text = text, type = type)
    }

    private companion object {
        const val COULD_NOT_SAVE = "Couldn't save that. Try again."
        const val COULD_NOT_DELETE = "Couldn't delete this post. Try again."
    }
}
