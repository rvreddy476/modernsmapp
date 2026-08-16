package com.us.android.feature.post.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.model.Post

/**
 * Everything the post screen renders.
 *
 * Sealed rather than a struct of nullables plus `isLoading`, for the same
 * reason as the profile screen: the flat shape permits states that cannot
 * exist, and every screen then invents its own precedence order.
 */
@Immutable
sealed interface PostUiState {

    data object Loading : PostUiState

    @Immutable
    data class Error(
        val message: String,
        val retryable: Boolean,
    ) : PostUiState

    @Immutable
    data class Content(
        val post: Post,
        /** True while any interaction is in flight. */
        val busy: Boolean = false,
        /** True once this viewer reposted in THIS session — see the ViewModel. */
        val hasReposted: Boolean = false,
        /**
         * Transient feedback for a failed interaction. Never replaces the
         * post: losing a loaded screen because a like failed is worse than the
         * failure.
         */
        val actionError: String? = null,
    ) : PostUiState
}
