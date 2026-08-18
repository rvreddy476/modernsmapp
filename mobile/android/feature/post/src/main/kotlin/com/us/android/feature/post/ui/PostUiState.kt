package com.us.android.feature.post.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.model.Post
import com.us.android.core.model.Profile

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
        /**
         * The post payload carries only `author_id`; the name and avatar come
         * from a second call to the public profile endpoint.
         *
         * Nullable and NOT part of the load gate: the post renders as soon as
         * it arrives, and the header fills in when the profile does. Blocking
         * a post on its author lookup would let a slow profile service hide
         * content that is already in hand.
         */
        val author: Profile? = null,
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
