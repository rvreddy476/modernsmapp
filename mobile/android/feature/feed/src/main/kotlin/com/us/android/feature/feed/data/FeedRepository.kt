package com.us.android.feature.feed.data

import androidx.paging.Pager
import androidx.paging.PagingConfig
import androidx.paging.PagingData
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedSurface
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class FeedRepository @Inject constructor(
    private val api: FeedApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * A paged stream for one surface.
     *
     * `enablePlaceholders = false`: the server never reports a total count, so
     * placeholder slots would be invented. A feed that renders grey rows for
     * items that may not exist is worse than one that simply ends.
     */
    fun feed(surface: FeedSurface): Flow<PagingData<FeedItem>> = Pager(
        config = PagingConfig(
            pageSize = PAGE_SIZE,
            prefetchDistance = PREFETCH_DISTANCE,
            enablePlaceholders = false,
            initialLoadSize = PAGE_SIZE * INITIAL_LOAD_MULTIPLIER,
        ),
        pagingSourceFactory = { FeedPagingSource(api, surface, errorMapper) },
    ).flow

    private companion object {
        const val PAGE_SIZE = 15

        /** Start loading the next page five rows early, not at the very end. */
        const val PREFETCH_DISTANCE = 5

        /**
         * Two pages on first load, not Paging's default of three. The first
         * screen is what cold-start latency is measured on, and a third page
         * is bytes nobody has scrolled to yet.
         */
        const val INITIAL_LOAD_MULTIPLIER = 2
    }
}
