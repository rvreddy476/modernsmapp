package com.us.android.core.feed.data

import com.us.android.core.model.FeedItem

/**
 * A collection's reorder, planned on the client (web parity, 2026-10-22,
 * `reorder.ts`). The server's move route takes the 0-based index a post
 * should land on, counted over EVERY row of the collection — the ones the
 * viewer may not see included — clamps it, re-writes the whole order and
 * answers it. So a move on screen is planned twice: the list the viewer sees
 * after it, and the position the server must be sent.
 */
data class CollectionMove(
    /** The `position` for `PATCH /v1/playlists/:id/items/:postId`. */
    val position: Int,
    /** Every row's post id after the move: the optimistic [VideoCollectionContents.order]. */
    val order: List<String>,
    /** The visible videos after the move: what the page shows at once. */
    val videos: List<FeedItem>,
)

/** [list] with the element at [from] moved to [to], both clamped; the same list when nothing moves. */
fun <T> moveIndex(list: List<T>, from: Int, to: Int): List<T> {
    if (list.isEmpty()) return list
    val f = from.coerceIn(0, list.lastIndex)
    val t = to.coerceIn(0, list.lastIndex)
    if (f == t) return list
    val next = list.toMutableList()
    val moved = next.removeAt(f)
    next.add(t, moved)
    return next
}

/**
 * Moves the visible video [postId] to [toVisibleIndex] among the visible
 * videos. Null when nothing would move (an unknown id, the same place, an
 * edge). The server position is where the video now at [toVisibleIndex]
 * sits in [order]: removing and re-inserting there lands the moved video
 * just after it when moving down and just before it when moving up — the
 * same neighbour the viewer sees, hidden rows notwithstanding.
 */
fun planCollectionMove(
    order: List<String>,
    videos: List<FeedItem>,
    postId: String,
    toVisibleIndex: Int,
): CollectionMove? {
    val from = videos.indexOfFirst { it.id == postId }
    if (from < 0 || videos.isEmpty()) return null
    val to = toVisibleIndex.coerceIn(0, videos.lastIndex)
    if (from == to) return null
    // An order that does not know the video (a stale read) falls back to the visible list.
    val full = if (order.containsAll(videos.map { it.id })) order else videos.map { it.id }
    val position = full.indexOf(videos[to].id)
    val fullFrom = full.indexOf(postId)
    return CollectionMove(
        position = position,
        order = moveIndex(full, fullFrom, position),
        videos = moveIndex(videos, from, to),
    )
}

/**
 * The server's answer to a move laid over what is on screen: the visible
 * videos in the server's order, a video the server no longer lists dropped.
 * Nothing on screen is re-read, so no thumbnail blinks. A video the server
 * listed but the page never drew (hidden, or added elsewhere) stays undrawn.
 */
fun reconcileCollectionOrder(serverOrder: List<String>, videos: List<FeedItem>): List<FeedItem> {
    val byId = videos.associateBy { it.id }
    return serverOrder.mapNotNull { byId[it] }
}
