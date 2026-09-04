package com.us.android.core.ui

import androidx.compose.runtime.Immutable

/**
 * What the comments UI ([UsCommentsSheet], [UsCommentsPanel]) can ask its
 * owner to do.
 *
 * One value rather than six trailing lambdas so the sheet, the panel and any
 * future host (PostTube's long-form player) take the same shape and a new
 * callback lands in one place.
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
)
