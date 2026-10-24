package com.us.android.feature.profile.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.model.FollowCounts
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.ProfileRelationship
import com.us.android.core.model.ProfileStats
import com.us.android.core.model.withGraph
import com.us.android.core.model.withPostCount

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
        /** True while a follow/block/cancel-request mutation is in flight. */
        val relationshipBusy: Boolean = false,
        /**
         * Set when a relationship action failed. Surfaced as transient
         * feedback rather than replacing the loaded profile — losing the whole
         * screen because an unfollow failed is a worse outcome than the
         * failure itself.
         */
        val actionError: String? = null,
        /**
         * True while the screen is asking "cancel your follow request?"
         * before it actually cancels one.
         *
         * A tap on "Requested" is destructive in a way Follow/Unfollow are
         * not — it discards a pending ask the other person may already be
         * about to approve — so it gets a confirmation the plain toggle
         * never needed.
         */
        val showCancelRequestConfirm: Boolean = false,
        /**
         * The count from the FIRST PAGE of `GET /v1/graph/follow-requests/incoming`,
         * fetched alongside the profile only when this is the signed-in user's
         * own. Null on someone else's profile, where the "Requests" pill never
         * renders.
         */
        val incomingFollowRequestCount: Int? = null,
        /**
         * The graph's live follower / following / friend counts
         * (`GET /v1/graph/counts/:id`, T-20). Null until they arrive or when
         * the read failed; re-read after every follow, unfollow and block so
         * the header moves with the action.
         */
        val graphCounts: FollowCounts? = null,
        /**
         * post-service's live post total (`GET /v1/posts/by-author/:id/counts`,
         * 2026-10-17). Null until it arrives or when the read failed.
         */
        val postTotal: Int? = null,
        /** True while a mute or unmute is on the wire. */
        val muteBusy: Boolean = false,
        /** A one-line confirmation of a menu action ("Muted", "Link copied"), shown then dismissed. */
        val notice: String? = null,
    ) : ProfileUiState {

        /**
         * Stats when available, profile counts otherwise — never both — with
         * the graph's follow counts and post-service's post total laid over
         * them when those arrived. The profile side's counts are only the
         * fallback: its follow counts stopped moving when follows moved to the
         * graph (audit R-1), and its post count is never written at all.
         */
        val counts: ProfileCounts
            get() = (stats?.counts ?: profile.counts).withGraph(graphCounts).withPostCount(postTotal)

        /**
         * This profile blocked the viewer: the screen draws a neutral
         * "not available" state in place of the follow and message controls
         * and the grid, and never says that a block is the reason.
         */
        val unavailable: Boolean get() = !profile.isOwnProfile && relationship.isBlockedByTarget

        /**
         * Whether the Followers / Following counts open their lists: the
         * same rule the media grid follows. Always on the viewer's own
         * profile; never on one that blocked the viewer, nor on a private
         * account the viewer does not follow.
         */
        val canOpenFollowLists: Boolean
            get() = profile.isOwnProfile ||
                (!unavailable && (!profile.isPrivate || relationship.followStatus == FollowStatus.FOLLOWING))
    }
}
