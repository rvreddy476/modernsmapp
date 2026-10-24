package com.us.android.core.feed.data

import androidx.paging.PagingData
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FeedSurface
import com.us.android.core.model.TrendingHashtag
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The `feed_mode` a query sends: ranked for For You, chronological for the
 * narrowed home tabs (Following, Friends), nothing for the other surfaces,
 * which rank on their own.
 */
internal fun FeedQuery.feedModeParam(): String? = when {
    surface != FeedSurface.Home -> null
    followingOnly || circleOnly -> FeedApi.FEED_MODE_CHRONOLOGICAL
    ranked -> FeedApi.FEED_MODE_RANKED
    else -> null
}

/**
 * The delta `feed_type` that counts new posts for a query: For You is the
 * home timeline, Following its follow-narrowed form. Null for everything
 * else — the server has no delta for Friends or a hashtag, so those show no
 * "new posts" pill.
 */
internal fun FeedQuery.deltaFeedType(): String? = when {
    surface != FeedSurface.Home || circleOnly -> null
    followingOnly -> FeedApi.DELTA_FOLLOWING
    else -> FeedApi.DELTA_HOME
}

/** How many posts are newer than what the reader has; [more] means "at least". */
data class NewPostCount(val count: Int, val more: Boolean)

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
    fun feed(query: FeedQuery): Flow<PagingData<FeedItem>> = pager { page(query, it.limit, it.cursor) }

    /**
     * [feed], with each generation's FIRST page reported as it lands: true
     * when it loaded, false when it failed. The paging source turns a failure
     * into a `LoadResult.Error` the caller never sees, so this is how a
     * refresh the viewer asked for (Reels' pull-to-refresh, 2026-10-21) learns
     * whether the fresh batch is in. A cancelled load reports nothing.
     */
    @Suppress("TooGenericExceptionCaught") // Reported, then rethrown for the source to classify.
    fun feed(query: FeedQuery, onFirstPage: (loaded: Boolean) -> Unit): Flow<PagingData<FeedItem>> =
        pager { request ->
            if (request.cursor != null) return@pager page(query, request.limit, request.cursor)
            val first = try {
                page(query, request.limit, cursor = null)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                onFirstPage(false)
                throw e
            }
            onFirstPage(first.errorCode == null)
            first
        }

    /**
     * [primary] that never ends: past its last page it carries on with
     * [fallback]'s pages ([endlessPage]). The full-screen Home feed's For
     * You and Following (2026-10-18).
     */
    fun endlessFeed(primary: FeedQuery, fallback: FeedQuery): Flow<PagingData<FeedItem>> = pager { request ->
        endlessPage(
            cursor = request.cursor,
            primary = { cursor -> page(primary, request.limit, cursor) },
            fallback = { cursor -> page(fallback, request.limit, cursor) },
        )
    }

    private suspend fun page(query: FeedQuery, limit: Int, cursor: String?): FeedPage = api.getFeed(
        surface = query.surface.path,
        limit = limit,
        cursor = cursor,
        // Null, not false: the parameter is omitted for the plain feed,
        // so the request the server has served since day one is unchanged.
        followingOnly = query.followingOnly.takeIf { on -> on },
        circleOnly = query.circleOnly.takeIf { on -> on },
        feedMode = query.feedModeParam(),
    ).toFeedPage().withCovers()

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
     * The posts of every hashtag the viewer follows, newest first (followed
     * hashtags, 2026-10-24) — hydrated like a tag's own page. The server's
     * author gate can empty a page that still has a cursor, so an empty page
     * is walked past, up to [FOLLOWED_TAG_PAGE_HOPS] in one load, rather than
     * ending the list there.
     */
    fun followedHashtagPosts(): Flow<PagingData<FeedItem>> = pager { request ->
        followedHashtagPage(request) { limit, cursor ->
            // The cursor is kept on an EMPTY page here (toFeedPage drops it):
            // the gate's empty page is not the end of the list.
            api.getFollowedHashtagPosts(limit, cursor).let { envelope ->
                envelope.toFeedPage().copy(nextCursor = envelope.meta?.nextCursor?.takeIf { it.isNotBlank() })
            }
        }
    }

    /** One load of [followedHashtagPosts]: pages until one holds a post or the cursor ends. */
    internal suspend fun followedHashtagPage(
        request: FeedPageRequest,
        fetch: suspend (limit: Int, cursor: String?) -> FeedPage,
    ): FeedPage {
        var cursor = request.cursor
        repeat(FOLLOWED_TAG_PAGE_HOPS) {
            val page = fetch(request.limit.coerceAtMost(FOLLOWED_TAG_MAX_LIMIT), cursor)
            if (page.items.isNotEmpty() || page.nextCursor == null || page.errorCode != null) {
                return page.copy(items = hashtagHydrator.hydrate(page.items))
            }
            cursor = page.nextCursor
        }
        // Hops exhausted on empty pages: hand Paging the cursor to keep going from.
        return FeedPage(items = emptyList(), nextCursor = cursor)
    }

    /**
     * One post as a feed row: post-service's bare `PostDetail`, hydrated the
     * way a hashtag page is. The author's own just-posted reel comes in here.
     */
    suspend fun post(postId: String): AppResult<FeedItem> =
        apiCall(errorMapper) { api.getPost(postId) }.map { dto ->
            hashtagHydrator.hydrate(listOf(dto.toDomain())).first()
        }

    /**
     * How many posts [query]'s timeline holds that are newer than [anchor],
     * the `created_at` of the newest post the reader has loaded. Null when
     * the query has no delta (see [deltaFeedType]).
     */
    suspend fun newPostCount(query: FeedQuery, anchor: String): AppResult<NewPostCount>? {
        val feedType = query.deltaFeedType() ?: return null
        return apiCall(errorMapper) { api.getDelta(feedType, anchor, NEW_POSTS_LIMIT) }
            .map { NewPostCount(it.newCount, it.hasMore) }
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
        apiCall(errorMapper) { api.feedback(FeedFeedbackRequest.forPost(postId, feedbackSignal(interested))) }
            .map { }

    /**
     * Records "Don't recommend @user" (or its undo) for every post by
     * [authorId] — the same endpoint, author scope. The caller WAITS on the
     * answer before hiding anything: an author-wide hide that had to be put
     * back would empty and refill half a feed.
     */
    suspend fun sendAuthorFeedback(authorId: String, interested: Boolean): AppResult<Unit> =
        apiCall(errorMapper) { api.feedback(FeedFeedbackRequest.forAuthor(authorId, feedbackSignal(interested))) }
            .map { }

    private fun feedbackSignal(interested: Boolean): String =
        if (interested) FeedApi.FEEDBACK_INTERESTED else FeedApi.FEEDBACK_NOT_INTERESTED

    /**
     * A feed-service page through the hydrator: the rows already carry their
     * author and media, so the only thing it fetches is each row's chosen
     * cover (the cover fix, 2026-09-05) — see [HashtagPostHydrator].
     */
    private suspend fun FeedPage.withCovers(): FeedPage = copy(items = hashtagHydrator.hydrate(items))

    /** The shared Pager — see [feedPager]; every list in the app pages the same way. */
    private fun pager(load: suspend (FeedPageRequest) -> FeedPage): Flow<PagingData<FeedItem>> =
        feedPager(errorMapper, load)

    private companion object {
        /** The server clamps to 30; a phone screen shows about half that. */
        const val TRENDING_LIMIT = 20

        /** hashtag_follows_handler.go:113: the followed-posts page is capped at 50. */
        const val FOLLOWED_TAG_MAX_LIMIT = 50

        /** Empty followed-tag pages walked past in one load before Paging gets the cursor back. */
        const val FOLLOWED_TAG_PAGE_HOPS = 3

        /** The pill reads "50+" past this; counting further buys nothing. */
        const val NEW_POSTS_LIMIT = 50
    }
}
