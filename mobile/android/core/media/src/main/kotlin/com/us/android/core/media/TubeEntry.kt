package com.us.android.core.media

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The long video Tube home should show FIRST, above whatever it ranked.
 *
 * The third of the publish journey's three landings (founder, 2026-09-06:
 * "photo → the feed, reel → Reels, long video → Tube, that video there").
 * A long video used to stay put on the profile after posting because Tube
 * had no way of being told which video to open with; this is that way.
 *
 * Exactly [FeedEntry]'s shape, and for exactly its reason: Tube home is
 * reached by a navigation that carries no argument — `navigateToTube()`
 * pushes a `data object` route — so the post id travels beside the
 * navigation rather than inside it. Tube fetches that one post by id, draws
 * it above the ranked rows and filters it out of them so it never appears
 * twice, then clears the request so the next visit is an ordinary one.
 *
 * It lives in `:core:media` because both `:feature:profile` (which writes
 * it) and `:feature:tube` (which reads it) can see this module, and
 * features must not depend on each other.
 */
@Singleton
class TubeEntry @Inject constructor() {

    private val _first = MutableStateFlow<String?>(null)

    /** The video Tube home should pin at the top, or null for an ordinary visit. */
    val first: StateFlow<String?> = _first.asStateFlow()

    /** A long video finished posting: Tube home should open with [postId] first. */
    fun showFirst(postId: String) {
        _first.value = postId
    }

    /** Tube has taken the request; the next visit is an ordinary one. */
    fun clear() {
        _first.value = null
    }
}
