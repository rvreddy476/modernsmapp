package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.model.ProfileStats

/**
 * Everything the profile screen renders, as one immutable value.
 *
 * Modelled as a sealed hierarchy rather than a struct of nullable fields plus
 * `isLoading`/`errorMessage` booleans, because that shape permits states that
 * cannot exist — loading and error at once, or content with an error banner
 * and no content. The screen then has to guess a precedence order, and two
 * screens guess differently.
 *
 * `@Immutable` is load-bearing, not decoration: Compose skips recomposition of
 * a composable whose inputs are unchanged only when it can prove stability.
 */
@Immutable
sealed interface ProfileUiState {

    data object Loading : ProfileUiState

    /**
     * [retryable] separates "try again" from "this will never work". A 404 for
     * a deleted account should not offer a retry button that re-fetches the
     * same 404 forever.
     */
    @Immutable
    data class Error(
        val message: String,
        val retryable: Boolean,
    ) : ProfileUiState

    @Immutable
    data class Content(
        val profile: Profile,
        /**
         * Null while the stats call is still in flight or has failed. The
         * header falls back to the counts carried on the profile payload,
         * which is why a stats failure does not fail the screen.
         */
        val stats: ProfileStats?,
        val relationship: ProfileRelationship,
        /** True while a follow/block mutation is in flight. */
        val relationshipBusy: Boolean = false,
        /**
         * Set when a relationship action failed. Surfaced as transient
         * feedback rather than replacing the loaded profile — losing the whole
         * screen because an unfollow failed is a worse outcome than the
         * failure itself.
         */
        val actionError: String? = null,
    ) : ProfileUiState {

        /** Stats when available, profile counts otherwise. Never both. */
        val counts get() = stats?.counts ?: profile.counts
    }
}
