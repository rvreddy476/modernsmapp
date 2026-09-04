package com.us.android.feature.feed.data

import com.us.android.core.model.FeedItem
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What the viewer asked not to see THIS session: posts they marked "Not
 * interested" and authors they blocked from the post "more" sheet.
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
 * Process-wide, like [FollowGraph], so a block made on a reel is already
 * applied when the same author's post scrolls past on Home.
 */
@Singleton
class HiddenPosts @Inject constructor() {
    private val _state = MutableStateFlow(HiddenSet())
    val state: StateFlow<HiddenSet> = _state.asStateFlow()

    fun hidePost(postId: String) = _state.update { it.copy(postIds = it.postIds + postId) }

    /** "Interested" after a "Not interested": the row comes back. */
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

    fun hides(item: FeedItem): Boolean = item.id in postIds || item.author.id in authorIds
}
