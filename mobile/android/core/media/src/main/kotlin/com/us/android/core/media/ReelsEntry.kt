package com.us.android.core.media

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The reel a feed sent the viewer to Reels to watch.
 *
 * A single tap on a video in the Home, Friends or hashtag feed opens the
 * Reels tab AT that reel, playing with sound (founder, 2026-09-05). The feed
 * and Reels are two screens of one feature, but the tab switch itself is the
 * shell's — `navigateToTopLevel(REELS)` carries no argument, because a tab
 * root is restored, not pushed — so the post id travels through this holder
 * instead: the feed writes it, navigates, and Reels reads it when it starts.
 *
 * It lives in `:core:media` because both the feed and the shell can see this
 * module and features must not depend on each other. It holds one id and no
 * logic: Reels decides whether that id is already in its pages (scroll to
 * it) or must be fetched (show it first), and clears the request once it has
 * acted on it, so a later visit from the tab opens where Reels was left.
 */
@Singleton
class ReelsEntry @Inject constructor() {

    private val _requested = MutableStateFlow<String?>(null)

    /** The post id Reels should open on, or null when nothing is asked of it. */
    val requested: StateFlow<String?> = _requested.asStateFlow()

    /** A feed video was tapped: Reels should open on [postId]. */
    fun open(postId: String) {
        _requested.value = postId
    }

    /** Reels has taken the request; the next visit is an ordinary one. */
    fun clear() {
        _requested.value = null
    }
}
