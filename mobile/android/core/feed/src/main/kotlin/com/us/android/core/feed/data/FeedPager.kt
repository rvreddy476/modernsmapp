package com.us.android.core.feed.data

import androidx.paging.Pager
import androidx.paging.PagingConfig
import androidx.paging.PagingData
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow

/**
 * The one Pager every feed list is built on — the home timeline, its
 * narrowings, reels, a hashtag's posts, Tube's videos and the viewer's own
 * lists. One configuration, so the first screen of every surface costs the
 * same two pages and the next page starts loading the same five rows early.
 *
 * `enablePlaceholders = false`: the server never reports a total count, so
 * placeholder slots would be invented. A feed that renders grey rows for
 * items that may not exist is worse than one that simply ends.
 *
 * [maxSize] bounds how many rows the list holds; see [FEED_STAGE_MAX_SIZE]
 * for the surfaces that set it. Unbounded is the default because a finite
 * list (a profile grid, a playlist) must keep its top: feeds are
 * forward-only (no `prevKey`), so a page dropped from the start can never
 * be loaded again.
 */
fun feedPager(
    errorMapper: ErrorMapper,
    maxSize: Int = PagingConfig.MAX_SIZE_UNBOUNDED,
    load: suspend (FeedPageRequest) -> FeedPage,
): Flow<PagingData<FeedItem>> =
    Pager(
        config = feedPagingConfig(maxSize),
        pagingSourceFactory = {
            FeedPagingSource(
                loader = { limit, cursor -> load(FeedPageRequest(limit, cursor)) },
                errorMapper = errorMapper,
            )
        },
    ).flow

/**
 * The shared configuration. Placeholders stay off with a bounded [maxSize]
 * too: with no `prevKey` a dropped page is never reloaded, so a placeholder
 * in its place would be a blank page the reader could scroll back into
 * forever. Without them the dropped rows simply leave the list, and the
 * pagers — keyed by post id — keep the reader on the post they are on.
 */
internal fun feedPagingConfig(maxSize: Int = PagingConfig.MAX_SIZE_UNBOUNDED) = PagingConfig(
    pageSize = FEED_PAGE_SIZE,
    prefetchDistance = FEED_PREFETCH_DISTANCE,
    enablePlaceholders = false,
    initialLoadSize = FEED_PAGE_SIZE * FEED_INITIAL_LOAD_MULTIPLIER,
    maxSize = maxSize,
)

const val FEED_PAGE_SIZE = 15

/**
 * The most rows a full-screen stage feed holds: Home's For You and
 * Following (which never end, see [endlessPage]), Friends and Reels. Ten
 * pages; past it the oldest pages behind the reader are dropped, so a long
 * session's heap stops growing with every post scrolled past. Scrolling
 * back up stops at the oldest row still held, well over a hundred posts back.
 */
const val FEED_STAGE_MAX_SIZE = FEED_PAGE_SIZE * 10

/** Start loading the next page five rows early, not at the very end. */
private const val FEED_PREFETCH_DISTANCE = 5

/**
 * Two pages on first load, not Paging's default of three. The first screen
 * is what cold-start latency is measured on, and a third page is bytes
 * nobody has scrolled to yet.
 */
private const val FEED_INITIAL_LOAD_MULTIPLIER = 2
