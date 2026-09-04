package com.us.android.core.engagement.data

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What the viewer asked not to see THIS session: posts they marked "Not
 * interested", posts they deleted, and authors they blocked from the post
 * "more" sheet.
 *
 * ## WHY A LOCAL SET AND NOT A REFRESH
 *
 * A PagingData page cannot be edited in place, and a refresh after every
 * "Not interested" would drop the reader to the top of the feed to remove one
 * row. The same overlay idea engagement uses applies: the intent is layered
 * over every page as a filter, and the server's next fetch — which excludes
 * the post at the hydration tail and the author's posts at the block check —
 * carries it for real.
 *
 * Process-wide, like the follow graph, so a block made on a reel is already
 * applied when the same author's post scrolls past on Home.
 *
 * ## WHY IT LIVES HERE AND NOT IN THE FEED
 *
 * A deleted post is hidden through this set and comes BACK through it when
 * the viewer restores it from Settings › Recently deleted. Settings must not
 * depend on the feed, and the feed must not depend on settings, so the set
 * sits in the neutral engagement seam both can see. The feed adds its own
 * `FeedItem` overload of [HiddenSet.hides]; this module knows only ids.
 */
@Singleton
class HiddenPosts @Inject constructor() {
    private val _state = MutableStateFlow(HiddenSet())
    val state: StateFlow<HiddenSet> = _state.asStateFlow()

    fun hidePost(postId: String) = _state.update { it.copy(postIds = it.postIds + postId) }

    /** "Interested" after a "Not interested", or a restore after a delete: the row comes back. */
    fun unhidePost(postId: String) = _state.update { it.copy(postIds = it.postIds - postId) }

    fun hideAuthor(authorId: String) = _state.update { it.copy(authorIds = it.authorIds + authorId) }

    /** The block was refused by the server: the author's posts return. */
    fun unhideAuthor(authorId: String) = _state.update { it.copy(authorIds = it.authorIds - authorId) }
}

/** The filter itself. Empty is the common case and costs nothing per page. */
data class HiddenSet(
    val postIds: Set<String> = emptySet(),
    val authorIds: Set<String> = emptySet(),
) {
    val isEmpty: Boolean get() = postIds.isEmpty() && authorIds.isEmpty()

    fun hides(postId: String, authorId: String): Boolean = postId in postIds || authorId in authorIds
}
