package com.us.android.core.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.engagement.data.CommentsActions
import com.us.android.core.engagement.data.MentionCandidate

/**
 * What the comments UI ([UsCommentsSheet], [UsCommentsPanel]) can ask its
 * owner to do.
 *
 * One value rather than a dozen trailing lambdas so the sheet, the panel and
 * any future host take the same shape and a new callback lands in one place.
 * The row actions (2026-10-14, audit T-17) default to doing nothing, so a
 * host that cannot perform one simply never offers it.
 */
@Immutable
data class UsCommentsCallbacks(
    val onDraftChange: (String) -> Unit,
    val onSubmit: () -> Unit,
    /** A quick-reaction emoji tap; see [QUICK_REACTIONS]. */
    val onQuickReaction: (String) -> Unit,
    val onLoadMore: () -> Unit,
    val onRetryAppend: () -> Unit,
    val onRetryRefresh: () -> Unit,
    /** "View N replies" / "Hide replies", by top-level comment id. */
    val onToggleReplies: (parentId: String) -> Unit = {},
    /** "View more replies" and the thread's "Try again". */
    val onLoadMoreReplies: (parentId: String) -> Unit = {},
    val onReply: (commentId: String) -> Unit = {},
    val onCancelReply: () -> Unit = {},
    val onToggleLike: (commentId: String) -> Unit = {},
    val onDelete: (commentId: String) -> Unit = {},
    val onTogglePin: (commentId: String) -> Unit = {},
    val onToggleHeart: (commentId: String) -> Unit = {},
    /** The reason is the wire token ([UsReportReason.wire]). */
    val onReport: (commentId: String, reason: String, details: String) -> Unit = { _, _, _ -> },
    val onReportClosed: () -> Unit = {},
    val onDismissNotice: () -> Unit = {},
    val onPickMention: (MentionCandidate) -> Unit = {},
) {
    companion object {
        /** Every verb wired to a [CommentsActions] — what each host builds. */
        fun of(actions: CommentsActions): UsCommentsCallbacks = UsCommentsCallbacks(
            onDraftChange = actions::onDraftChange,
            onSubmit = actions::submit,
            onQuickReaction = actions::quickReaction,
            onLoadMore = actions::loadMore,
            onRetryAppend = actions::loadMore,
            onRetryRefresh = actions::refresh,
            onToggleReplies = actions::toggleReplies,
            onLoadMoreReplies = actions::loadMoreReplies,
            onReply = actions::startReply,
            onCancelReply = actions::cancelReply,
            onToggleLike = actions::toggleLike,
            onDelete = actions::delete,
            onTogglePin = actions::togglePin,
            onToggleHeart = actions::toggleHeart,
            onReport = actions::report,
            onReportClosed = actions::resetReport,
            onDismissNotice = actions::dismissNotice,
            onPickMention = actions::pickMention,
        )
    }
}
