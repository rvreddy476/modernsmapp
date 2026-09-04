package com.us.android.feature.tube.ui.watch

import com.us.android.core.model.FeedItem

/**
 * "Up next" under the player: the rows AFTER the current one in the list
 * the viewer came from, in that order, capped at [limit]. A video that is
 * not in the list at all — opened by id, or the list was refreshed under
 * it — offers the whole list minus itself, so there is always something to
 * play next while there is anything at all.
 */
fun upNext(items: List<FeedItem>, currentId: String, limit: Int = UP_NEXT_LIMIT): List<FeedItem> {
    val index = items.indexOfFirst { it.id == currentId }
    val rest = if (index >= 0) items.drop(index + 1) else items.filter { it.id != currentId }
    return rest.take(limit)
}

/** What plays when the current video ends, or null at the end of the list. */
fun nextAfter(items: List<FeedItem>, currentId: String): FeedItem? = upNext(items, currentId, 1).firstOrNull()

/** A phone screen shows about this many rows before the viewer scrolls. */
const val UP_NEXT_LIMIT = 10
