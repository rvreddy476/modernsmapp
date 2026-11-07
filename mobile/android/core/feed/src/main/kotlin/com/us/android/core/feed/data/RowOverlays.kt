package com.us.android.core.feed.data

import androidx.paging.PagingData
import androidx.paging.flatMap
import com.us.android.core.engagement.data.HiddenSet
import com.us.android.core.model.FeedItem
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine

/**
 * What this session has done to rows the server already sent: the posts it
 * hid, deleted or blocked the author of ([HiddenSet]) and the owner's saved
 * edits ([PostEdits]).
 *
 * ONE view for every row a surface draws — the paged rows AND the post it
 * pins above them (Home's just-published post, the reel Reels holds). The
 * pinned row used to skip both, so a post deleted from its own More sheet
 * said "Post deleted" and kept playing at the top, and an edit left the old
 * words there. Both kinds of row now go through [view], so they cannot
 * disagree again.
 */
data class RowOverlays(
    val hidden: HiddenSet = HiddenSet(),
    val edits: Map<String, PostEditPatch> = emptyMap(),
) {
    val isEmpty: Boolean get() = hidden.isEmpty && edits.isEmpty()

    /** [item] as this session has left it, or null when it must not show. */
    fun view(item: FeedItem): FeedItem? = if (hidden.hides(item)) null else edits.applyTo(item)
}

/** The overlays as they change: hides and edits, each from its process-wide store. */
fun rowOverlays(hidden: Flow<HiddenSet>, edits: Flow<Map<String, PostEditPatch>>): Flow<RowOverlays> =
    combine(hidden, edits, ::RowOverlays)

/**
 * Every row of the page through [RowOverlays.view]. Nothing to lay over is
 * the common case and leaves the page untouched.
 */
fun PagingData<FeedItem>.through(overlays: RowOverlays): PagingData<FeedItem> =
    if (overlays.isEmpty) this else flatMap { item -> listOfNotNull(overlays.view(item)) }
