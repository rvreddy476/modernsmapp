package com.us.android.core.feed.data

/**
 * A feed that does not end (post-screens parity, 2026-10-18): when the
 * primary timeline runs out, the pages carry on from a FALLBACK one — the
 * Following tab into For You, For You into the plain newest-first home
 * timeline — so a full-screen feed never shows "all caught up". Posts
 * already shown are dropped by the paging source's de-duplication, which
 * spans the whole generation, fallback pages included.
 *
 * The cursor says which timeline a page came from: a primary cursor is the
 * server's, verbatim; a fallback cursor is [FALLBACK_CURSOR_PREFIX] followed
 * by the fallback server's own cursor (empty for its first page). Neither
 * server cursor is ever parsed. Only when the fallback ends too does the
 * feed end. A handled error is passed through untouched, so it still
 * surfaces as an error rather than as an early switch.
 *
 * Pure over its two loaders, so the switch is a table test.
 */
suspend fun endlessPage(
    cursor: String?,
    primary: suspend (cursor: String?) -> FeedPage,
    fallback: suspend (cursor: String?) -> FeedPage,
): FeedPage {
    if (cursor != null && cursor.startsWith(FALLBACK_CURSOR_PREFIX)) {
        val page = fallback(cursor.removePrefix(FALLBACK_CURSOR_PREFIX).ifEmpty { null })
        return page.copy(nextCursor = page.nextCursor?.let { FALLBACK_CURSOR_PREFIX + it })
    }
    val page = primary(cursor)
    if (page.errorCode != null || page.nextCursor != null) return page
    // The primary timeline has ended. An empty last page goes straight on to
    // the fallback's first, so a viewer with nothing in Following sees For
    // You at once rather than an empty screen and then a page.
    if (page.items.isEmpty()) return endlessPage(FALLBACK_CURSOR_PREFIX, primary, fallback)
    return page.copy(nextCursor = FALLBACK_CURSOR_PREFIX)
}

/** Marks a cursor as the fallback timeline's. A server cursor (RFC 3339 or base64) never contains it. */
const val FALLBACK_CURSOR_PREFIX = "fallback|"
