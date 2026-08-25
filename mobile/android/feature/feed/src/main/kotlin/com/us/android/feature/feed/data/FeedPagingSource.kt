package com.us.android.feature.feed.data

import androidx.paging.PagingSource
import androidx.paging.PagingState
import com.us.android.core.common.error.AppError
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPoll
import com.us.android.core.model.FeedPollOption
import com.us.android.core.model.FeedSurface
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.feed.data.dto.FeedItemDto
import kotlinx.coroutines.CancellationException
import java.util.concurrent.ConcurrentHashMap

/**
 * Pages a feed surface by cursor.
 *
 * Network-only, with no Room layer yet. That is a deliberate first step, not
 * an oversight: offline-first needs a `RemoteMediator` over a Room schema, and
 * the feed item shape was only observed for the first time on 2026-08-17.
 * Designing a durable schema around a one-day-old contract would bake in
 * whatever it gets wrong. Paging 3 is here from the start so the load, error
 * and retry semantics are the framework's rather than hand-rolled, and the
 * mediator slots underneath without touching the UI.
 *
 * TWO PAGINATION REGIMES, ONE SOURCE
 *
 * Only [FeedSurface.Home] returns `meta.next_cursor`. Reels, videos and watch
 * returned a full page with NO cursor and no `meta` at all. The capture is
 * explicit that a cursor must not be invented for them, so this source reports
 * a single terminal page on those surfaces rather than looping forever on a
 * null key or fabricating an offset the server never offered.
 */
class FeedPagingSource(
    private val api: FeedApi,
    private val surface: FeedSurface,
    private val errorMapper: ErrorMapper,
) : PagingSource<String, FeedItem>() {

    /**
     * Post ids already emitted by THIS paging generation.
     *
     * ## WHY DE-DUPLICATION IS A CRASH FIX, NOT POLISH
     *
     * A repeated id in a `LazyColumn` keyed by id throws
     * `IllegalArgumentException: Key "…" was already used` and kills the feed —
     * the first screen after login — so the app becomes unusable rather than
     * showing one row twice.
     *
     * The feed is time-ordered and assembled by fan-out, so an id can
     * legitimately repeat: a post can arrive through more than one path, and a
     * post inserted between two page fetches shifts every later row into the
     * next one. Filtering inside a single page cannot catch that — the same id
     * arrives on a LATER page — which is why this is scoped to the source.
     *
     * A new generation (refresh, or invalidation) constructs a new
     * `FeedPagingSource`, so the set resets exactly when the list does.
     *
     * Concurrent: append and refresh loads can overlap.
     */
    private val emittedIds = ConcurrentHashMap.newKeySet<String>()

    @Suppress("TooGenericExceptionCaught")
    override suspend fun load(params: LoadParams<String>): LoadResult<String, FeedItem> = try {
        val envelope = api.getFeed(
            surface = surface.path,
            limit = params.loadSize.coerceAtMost(MAX_LIMIT),
            cursor = params.key,
        )
        envelope.error?.let { error ->
            LoadResult.Error(
                AppErrorException(AppError.Unknown(code = error.code, statusCode = null)),
            )
        } ?: LoadResult.Page(
            // De-duplicated by post id — see `dropDuplicates`. A repeated id is a
            // CRASH in Compose, not a cosmetic issue, because LazyColumn keys
            // must be unique.
            // First occurrence wins, so ordering is untouched.
            data = (envelope.data ?: emptyList())
                .map { it.toDomain() }
                .filter { emittedIds.add(it.id) },
            prevKey = null, // Feeds are forward-only; there is no previous page.
            nextKey = envelope.nextKey(),
        )
    } catch (e: CancellationException) {
        // Rethrown before the generic branch: swallowing it here would break
        // structured concurrency and leave a load that refuses to die when the
        // user scrolls away.
        throw e
    } catch (e: Throwable) {
        // Deliberately broad. Retrofit surfaces HttpException, okio surfaces
        // IOException, and the converter can throw a SerializationException;
        // ErrorMapper already classifies all three into the typed AppError the
        // UI branches on, so enumerating them here would only risk missing one
        // and crashing a background load.
        LoadResult.Error(AppErrorException(errorMapper.map(e)))
    }

    /**
     * Paging asks for this after a refresh so the list can resume near where
     * the user was. Returning null restarts from the newest page, which is the
     * correct behaviour for a feed: a refresh should show new content, not
     * silently resume mid-scroll.
     */
    override fun getRefreshKey(state: PagingState<String, FeedItem>): String? = null

    private companion object {
        /**
         * Paging's `loadSize` is three times the page size on the initial load.
         * The server accepted `limit=1` and `limit=2` in captures; no maximum
         * was probed, so this ceiling is client caution rather than a known
         * server limit.
         */
        const val MAX_LIMIT = 50
    }
}

/**
 * Only the paginated surface yields a next key, and only when the page was
 * full. A short page means the end regardless of what `meta` says — otherwise
 * a server that always echoes a cursor would page forever.
 */
/**
 * The cursor for the next page, or null at the end.
 *
 * All four surfaces paginate as of the 2026-08-17 hydration closure. Home
 * returns an RFC3339 timestamp and the ranked surfaces return an opaque
 * base64 timeuuid; both are replayed verbatim and neither is ever parsed.
 *
 * The terminal page omits `meta` entirely rather than sending an empty
 * cursor, so an absent or blank value is the end. An empty page is also
 * treated as the end regardless of what `meta` says — otherwise a server that
 * always echoed a cursor would page forever.
 */
private fun ApiEnvelope<List<FeedItemDto>>.nextKey(): String? {
    val items = data ?: return null
    if (items.isEmpty()) return null
    return meta?.nextCursor?.takeIf { it.isNotBlank() }
}

/** Carries a typed [AppError] through Paging's `Throwable`-shaped error channel. */
class AppErrorException(val error: AppError) : Exception(error::class.simpleName)

internal fun FeedItemDto.toDomain() = FeedItem(
    id = id,
    authorId = authorId,
    author = FeedAuthor(
        // The server always sends `author`; a genuinely deleted profile comes
        // back as its non-enumerating placeholder, which is the server's call
        // to make, not the client's. Falling back to `authorId` here keeps a
        // row renderable if the object is ever absent entirely.
        id = author.id.ifBlank { authorId },
        displayName = author.displayName,
        username = author.username,
        avatarMediaId = author.avatarMediaId,
    ),
    text = text,
    visibility = visibility,
    feedContentType = feedContentType,
    postType = postType,
    createdAt = createdAt,
    isPinned = isPinned,
    media = media.toOrderedFeedMedia(),
    counts = FeedCounts(
        likes = counts.likes,
        comments = counts.comments,
        reposts = repostCount,
        views = viewCount,
    ),
    viewer = FeedViewerState(
        isBookmarked = isBookmarked,
        hasReacted = hasReacted,
        hasReposted = hasReposted,
        viewerReaction = viewerReaction,
    ),
    isRepostable = isRepostable,
    score = score,
    poll = poll?.let { dto ->
        FeedPoll(
            question = dto.question,
            allowsMultiple = dto.allowsMultiple,
            options = dto.options.map {
                FeedPollOption(
                    id = it.id,
                    label = it.label,
                    voteCount = it.voteCount,
                    percentage = it.percentage,
                )
            },
            totalVotes = dto.totalVotes,
            viewerVotedOptionIds = dto.viewerVotes,
            hasEnded = dto.hasEnded,
        )
    },
)
