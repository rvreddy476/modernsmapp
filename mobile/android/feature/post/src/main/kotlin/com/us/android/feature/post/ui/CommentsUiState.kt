package com.us.android.feature.post.ui

import androidx.compose.runtime.Immutable
import com.us.android.feature.post.data.Comment

/**
 * Everything the comments screen renders.
 *
 * Sealed for the same reason as [PostUiState]: a flat struct of nullables plus
 * an `isLoading` flag permits combinations that cannot occur, and every screen
 * then invents its own precedence order for resolving them.
 *
 * Notably absent is any paging state — no `loadingMore`, no cursor, no
 * `hasMore`. The endpoint returns one page and offers no cursor, so those
 * fields would be permanently constant and would still have to be read and
 * reasoned about at every call site.
 */
@Immutable
sealed interface CommentsUiState {

    data object Loading : CommentsUiState

    @Immutable
    data class Error(
        val message: String,
        val retryable: Boolean,
    ) : CommentsUiState

    /**
     * A successful load.
     *
     * An empty list is Content, not a fourth `Empty` case. `{"data":[]}` is a
     * success on the wire, and splitting it out would make callers handle two
     * states that behave identically everywhere except in one rendering
     * branch — while quietly inviting the empty case to be styled as a failure.
     */
    @Immutable
    data class Content(
        val comments: List<Comment>,
    ) : CommentsUiState
}
