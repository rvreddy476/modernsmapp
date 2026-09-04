package com.us.android.feature.feed.data

import androidx.paging.Pager
import androidx.paging.PagingConfig
import androidx.paging.PagingData
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.TrendingHashtag
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class FeedRepository @Inject constructor(
    private val api: FeedApi,
    private val errorMapper: ErrorMapper,
    private val hashtagHydrator: FeedItemHydrator,
) {

    /**
     * A paged stream for one query: a surface plus its server-side narrowing.
     *
     * `enablePlaceholders = false`: the server never reports a total count, so
     * placeholder slots would be invented. A feed that renders grey rows for
     * items that may not exist is worse than one that simply ends.
     */
    fun feed(query: FeedQuery): Flow<PagingData<FeedItem>> = pager {
        api.getFeed(
            surface = query.surface.path,
            limit = it.limit,
            cursor = it.cursor,
            // Null, not false: the parameter is omitted for the plain feed,
            // so the request the server has served since day one is unchanged.
            followingOnly = query.followingOnly.takeIf { on -> on },
            circleOnly = query.circleOnly.takeIf { on -> on },
        ).toFeedPage()
    }

    /**
     * A paged stream of the posts carrying [tag], newest first.
     *
     * The same pager, the same de-duplication and the same card as the home
     * timeline; the one difference is that post-service sends bare rows, so
     * each page passes through [FeedItemHydrator] to pick up the author and
     * the media delivery the card needs.
     */
    fun hashtagPosts(tag: String): Flow<PagingData<FeedItem>> = pager {
        val page = api.getPostsByHashtag(
            tag = tag.removePrefix("#"),
            limit = it.limit,
            cursor = it.cursor,
        ).toFeedPage()
        page.copy(items = hashtagHydrator.hydrate(page.items))
    }

    /**
     * One post as a feed row: post-service's bare `PostDetail`, hydrated the
     * way a hashtag page is. The author's own just-posted reel comes in here.
     */
    suspend fun post(postId: String): AppResult<FeedItem> =
        apiCall(errorMapper) { api.getPost(postId) }.map { dto ->
            hashtagHydrator.hydrate(listOf(dto.toDomain())).first()
        }

    /** Today's trending tags, most-used first. Empty is a real answer, not a failure. */
    suspend fun trendingHashtags(): AppResult<List<TrendingHashtag>> =
        apiCall(errorMapper) { api.getTrendingHashtags(TRENDING_LIMIT) }.map { dto ->
            dto.hashtags
                .filter { it.normalizedName.isNotBlank() }
                .map { TrendingHashtag(it.normalizedName, it.displayName, it.postCount) }
        }

    /**
     * Casts one poll vote. Returns whether the server accepted it — the
     * caller's optimistic flip stands on success and reverts on failure.
     */
    suspend fun votePoll(postId: String, optionId: String): Boolean =
        runCatching { api.votePoll(postId, PollVoteRequest(optionId)) }.isSuccess

    /**
     * Records "Interested" / "Not interested" for [postId].
     *
     * The caller has already hidden (or kept) the row; the answer only
     * decides whether to tell the viewer the server disagreed. `interested`
     * after `not_interested` is how a hide is undone — latest wins.
     */
    suspend fun sendFeedback(postId: String, interested: Boolean): AppResult<Unit> =
        apiCall(errorMapper) {
            api.feedback(
                FeedFeedbackRequest(
                    postId = postId,
                    signal = if (interested) FeedApi.FEEDBACK_INTERESTED else FeedApi.FEEDBACK_NOT_INTERESTED,
                ),
            )
        }.map { }

    private fun pager(load: suspend (PageRequest) -> FeedPage): Flow<PagingData<FeedItem>> = Pager(
        config = PagingConfig(
            pageSize = PAGE_SIZE,
            prefetchDistance = PREFETCH_DISTANCE,
            enablePlaceholders = false,
            initialLoadSize = PAGE_SIZE * INITIAL_LOAD_MULTIPLIER,
        ),
        pagingSourceFactory = {
            FeedPagingSource(
                loader = { limit, cursor -> load(PageRequest(limit, cursor)) },
                errorMapper = errorMapper,
            )
        },
    ).flow

    /** What the paging source asks a loader for. */
    private data class PageRequest(val limit: Int, val cursor: String?)

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

        /** The server clamps to 30; a phone screen shows about half that. */
        const val TRENDING_LIMIT = 20
    }
}
