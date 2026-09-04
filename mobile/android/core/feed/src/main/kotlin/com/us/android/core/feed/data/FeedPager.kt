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
 */
fun feedPager(errorMapper: ErrorMapper, load: suspend (FeedPageRequest) -> FeedPage): Flow<PagingData<FeedItem>> =
    Pager(
        config = PagingConfig(
            pageSize = FEED_PAGE_SIZE,
            prefetchDistance = FEED_PREFETCH_DISTANCE,
            enablePlaceholders = false,
            initialLoadSize = FEED_PAGE_SIZE * FEED_INITIAL_LOAD_MULTIPLIER,
        ),
        pagingSourceFactory = {
            FeedPagingSource(
                loader = { limit, cursor -> load(FeedPageRequest(limit, cursor)) },
                errorMapper = errorMapper,
            )
        },
    ).flow

const val FEED_PAGE_SIZE = 15

/** Start loading the next page five rows early, not at the very end. */
private const val FEED_PREFETCH_DISTANCE = 5

/**
 * Two pages on first load, not Paging's default of three. The first screen
 * is what cold-start latency is measured on, and a third page is bytes
 * nobody has scrolled to yet.
 */
private const val FEED_INITIAL_LOAD_MULTIPLIER = 2
