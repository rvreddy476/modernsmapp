package com.us.android.core.media

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The post the home feed should show FIRST, above whatever the server ranked.
 *
 * The one thing that writes here is a publish that has just finished. The
 * founder's journey (2026-09-06) is: press Post, land on your profile and
 * watch the upload, and when it completes land on the feed with your new post
 * at the top. The last hop is the reason this exists — the feed is a tab root,
 * so `navigateToTopLevel(HOME)` restores it rather than pushing it and cannot
 * carry an argument, exactly as [ReelsEntry] describes for Reels.
 *
 * ## WHY A PIN AND NOT A REFRESH
 *
 * "At the top" is a promise the server does not make. The ranked feed decides
 * its own order, and a brand-new post may land anywhere in the first page or
 * not be in it at all yet. So the feed does not refresh and hope: it fetches
 * this one post by id and draws it above the paged rows, filtering it out of
 * those rows so it never appears twice — the same head-slot shape the Reels
 * tab already uses for a reel that has just finished posting.
 *
 * It lives in `:core:media` because both `:feature:feed` and `:feature:profile`
 * can see this module and features must not depend on each other. It holds one
 * id and no logic; the feed clears it once it has drawn it, so returning to
 * the tab later is an ordinary visit.
 */
@Singleton
class FeedEntry @Inject constructor() {

    private val _first = MutableStateFlow<String?>(null)

    /** The post the feed should pin at the top, or null for an ordinary feed. */
    val first: StateFlow<String?> = _first.asStateFlow()

    /** A publish finished: the home feed should open with [postId] first. */
    fun showFirst(postId: String) {
        _first.value = postId
    }

    /** The feed has taken the request; the next visit is an ordinary one. */
    fun clear() {
        _first.value = null
    }
}
