package com.us.android.core.model

/**
 * The ONE button a "People you may know / People you might be interested in"
 * row carries (founder, 24 Oct 2026): Follow, or the state a follow left
 * behind. No Message, no Connect, no Add friend. Tapping the person still
 * opens their profile, and a dismiss may stay where the surface has one.
 *
 * Every suggestion surface draws its button from [suggestionButton], so the
 * rule lives in one place; the surfaces' compose tests pin that nothing else
 * is drawn beside it.
 */
enum class SuggestionButton(val label: String, val tappable: Boolean) {
    /** Not followed yet: the one tappable button. */
    FOLLOW("Follow", tappable = true),

    /** Followed. Shown, not pressed: unfollowing is the profile's job. */
    FOLLOWING("Following", tappable = false),

    /** A private account's follow request is waiting: still the follow button's state. */
    REQUESTED("Requested", tappable = false),
}

/** The button for the viewer's follow [edge] to the suggested person; unknown reads as not followed. Pure. */
fun suggestionButton(edge: FollowStatus?): SuggestionButton = when (edge) {
    FollowStatus.FOLLOWING -> SuggestionButton.FOLLOWING
    FollowStatus.REQUESTED -> SuggestionButton.REQUESTED
    FollowStatus.NONE, null -> SuggestionButton.FOLLOW
}
