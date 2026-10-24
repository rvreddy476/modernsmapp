package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The discovery reads the web has and Android lacked (2026-10-24): Reels
 * Explore (web features/reels/explore/exploreApi.ts) and Tube's Trending and
 * Topics (web features/posttube/discovery/discoveryApi.ts). Its own
 * interface, created from the app-wide Retrofit, so no existing fake grows
 * members it never answers.
 */
interface DiscoveryApi {

    /**
     * The video taxonomy, `[{id, slug, label, kind}]` (post-service
     * service/categories.go:41). `kind=short` answers the reel entries,
     * `kind=long` the long-video ones (post-service internal/http/handler.go:744).
     */
    @GET("v1/posts/categories")
    suspend fun categories(@Query("kind") kind: String): ApiEnvelope<List<FeedCategoryDto>>

    /**
     * One feed surface narrowed by `category` and, on `videos`, ordered by
     * `sort` (`recent` | `popular`; feed-service internal/http/handler.go:364).
     * `reels?category=` is the Explore grid (handler.go:308): a malformed slug
     * is 400 `INVALID_CATEGORY`; a slug no reel carries is an empty page —
     * which, because the server walks a bounded number of windows, may still
     * carry a cursor.
     */
    @GET("v1/feed/{surface}")
    suspend fun feed(
        @Path("surface") surface: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
        @Query("category") category: String? = null,
        @Query("sort") sort: String? = null,
    ): ApiEnvelope<List<FeedItemDto>>

    /**
     * The ranked trending posts (post-service internal/http/handler.go:2674):
     * `{"data":{"items":[PostDetail…],"next_cursor":"…"},"meta":{"next_cursor"}}`
     * (writeTrendingPage, handler.go:2724). Bare rows: hydrated client-side.
     * There is no period on the wire; the page narrows by publish date.
     */
    @GET("v1/posts/trending")
    suspend fun trending(
        @Query("content_type") contentType: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<TrendingPostsDto>
}

/** `GET /v1/posts/trending`'s data object. */
@Serializable
data class TrendingPostsDto(
    val items: List<FeedItemDto> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

// ── Topics ────────────────────────────────────────────────────────────────

/** Which screens an entry of the taxonomy belongs on. */
enum class TopicKind { ALL, SHORT, LONG }

/** One entry of the taxonomy: the slug the feeds take and the label to show. */
data class Topic(val slug: String, val label: String, val kind: TopicKind = TopicKind.ALL)

/**
 * The taxonomy as the screens read it — the web's `normalizeTopics`: the
 * slug is `slug`, else `id`, lowercased and trimmed; an entry without one,
 * or one seen before, is dropped; a blank label is the slug in title case;
 * a missing or unknown kind is "all". The server's order is kept.
 */
fun normalizeTopics(rows: List<FeedCategoryDto>): List<Topic> {
    val seen = mutableSetOf<String>()
    return rows.mapNotNull { row ->
        val slug = row.slug.ifBlank { row.id }.trim().lowercase()
        if (slug.isEmpty() || !seen.add(slug)) return@mapNotNull null
        Topic(slug = slug, label = row.label.trim().ifBlank { titleCase(slug) }, kind = topicKind(row.kind))
    }
}

private fun topicKind(raw: String): TopicKind = when (raw.trim().lowercase()) {
    "short" -> TopicKind.SHORT
    "long" -> TopicKind.LONG
    else -> TopicKind.ALL
}

private fun titleCase(slug: String): String =
    slug.split('-', '_', ' ').filter { it.isNotBlank() }.joinToString(" ") { word ->
        word.replaceFirstChar { it.uppercase() }
    }

/** What belongs on a reel screen: kind short or all. */
fun shortTopics(topics: List<Topic>): List<Topic> = topics.filter { it.kind != TopicKind.LONG }

/** What belongs on a long-video screen: kind long or all. */
fun longTopics(topics: List<Topic>): List<Topic> = topics.filter { it.kind != TopicKind.SHORT }

// ── Reels Explore ─────────────────────────────────────────────────────────

/** The "All" chip: no category on the request. */
const val EXPLORE_ALL = ""

const val EXPLORE_PAGE_SIZE = 18

/** Requests one viewer action (a chip tap, "Show more") may spend walking past empty pages. */
const val EXPLORE_MAX_HOPS = 4

/** A chip's value as the feed validates it, and whether the server would take it. */
data class ExploreSlug(val slug: String, val valid: Boolean)

/** feed-service's rule (service/category.go:34): lowercase, then `^[a-z0-9][a-z0-9_-]{0,31}$`. */
private val CATEGORY_SLUG = Regex("^[a-z0-9][a-z0-9_-]{0,31}$")

/**
 * A raw chip value → the slug the feed validates (feed-service
 * NormalizeCategoryFilter, service/category.go:41): trimmed and lowercased;
 * empty is "All" and always valid. [ExploreSlug.valid] is false for anything
 * the server would refuse with `INVALID_CATEGORY`, so it is never sent.
 */
fun normalizeExploreCategory(raw: String?): ExploreSlug {
    val slug = raw.orEmpty().trim().lowercase()
    if (slug.isEmpty()) return ExploreSlug(EXPLORE_ALL, valid = true)
    return ExploreSlug(slug, CATEGORY_SLUG.matches(slug))
}

/** One viewer action's worth of the Explore grid. */
data class ExplorePage(
    val items: List<FeedItem>,
    /** Null when the feed has nothing further back. */
    val nextCursor: String?,
    /** Requests this page took (1 unless it walked past empty pages). */
    val requests: Int,
)

/** The category was refused as malformed — by this client first, or by the server's 400. */
const val INVALID_CATEGORY = "INVALID_CATEGORY"

/** True for the refusal no retry will mend. */
fun AppError.isInvalidCategory(): Boolean = this is AppError.Unknown && code == INVALID_CATEGORY

/**
 * Walks the Explore feed for one viewer action: past empty pages while a
 * cursor is present, up to [maxHops] requests, stopping at the first page
 * with reels. An empty result WITH a cursor means "there may be more" — the
 * grid offers to keep looking rather than calling the category empty. A
 * cursor the server repeats ends the walk, so a misbehaving server cannot
 * loop it. Pure over [fetch], so the walk is a table test.
 */
suspend fun walkExplore(
    cursor: String?,
    maxHops: Int = EXPLORE_MAX_HOPS,
    fetch: suspend (cursor: String?) -> AppResult<Pair<List<FeedItem>, String?>>,
): AppResult<ExplorePage> {
    var at = cursor
    var requests = 0
    val hops = maxHops.coerceAtLeast(1)
    while (true) {
        requests += 1
        val (items, rawNext) = when (val result = fetch(at)) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return result
        }
        val next = rawNext?.takeIf { it.isNotBlank() }
        val stuck = next != null && next == at
        if (items.isNotEmpty() || next == null || stuck || requests >= hops) {
            return AppResult.Success(ExplorePage(items, if (stuck) null else next, requests))
        }
        at = next
    }
}

/** The reels across the pages loaded so far, each once, in order. */
fun exploreTiles(pages: List<ExplorePage>): List<FeedItem> {
    val seen = mutableSetOf<String>()
    return pages.flatMap { it.items }.filter { seen.add(it.id) }
}

// ── Tube Trending ─────────────────────────────────────────────────────────

/** The web's period pills: Today, Week, Month — narrowed on the client by publish date. */
enum class TrendingPeriod(val label: String, val windowMillis: Long) {
    TODAY("Today", DAY_MILLIS),
    WEEK("Week", 7 * DAY_MILLIS),
    MONTH("Month", 30 * DAY_MILLIS),
}

/**
 * True when [publishedAt] parses and falls inside [period] ending at
 * [nowMillis] (and no more than a minute in the future, for clock skew) —
 * the web's `withinPeriod`. An unparsable or blank date is out.
 */
fun withinPeriod(publishedAt: String?, period: TrendingPeriod, nowMillis: Long): Boolean {
    val at = publishedAt?.takeIf { it.isNotBlank() }?.let(::parseInstantMillis) ?: return false
    return nowMillis - at <= period.windowMillis && at <= nowMillis + FUTURE_SKEW_MILLIS
}

/** The trending rows narrowed to [period], the server's rank kept. */
fun filterByPeriod(items: List<FeedItem>, period: TrendingPeriod, nowMillis: Long): List<FeedItem> =
    items.filter { withinPeriod(it.publishedAt(), period, nowMillis) }

/** When a post went live: a scheduled post's `publish_at`, else when it was made. */
fun FeedItem.publishedAt(): String = publishAt?.takeIf { it.isNotBlank() } ?: createdAt

private fun parseInstantMillis(raw: String): Long? =
    runCatching { java.time.OffsetDateTime.parse(raw).toInstant().toEpochMilli() }.getOrNull()
        ?: runCatching { java.time.Instant.parse(raw).toEpochMilli() }.getOrNull()

private const val DAY_MILLIS = 24L * 60 * 60 * 1000
private const val FUTURE_SKEW_MILLIS = 60_000L

// ── Topic feed ────────────────────────────────────────────────────────────

/** A topic page's order: `sort=recent` (the default) or `sort=popular` on `/v1/feed/videos`. */
enum class TopicSort(val wire: String, val label: String) {
    RECENT("recent", "Recent"),
    POPULAR("popular", "Popular"),
}

/** A page of a list read outside Paging: the rows and the cursor to the next. */
data class DiscoveryPage(val items: List<FeedItem>, val nextCursor: String?)

/**
 * The discovery reads, each answering [AppResult] so a screen can tell an
 * empty list from a failed one. Rows are hydrated like every other list so
 * the cards have their author and delivery.
 */
@Singleton
class DiscoveryRepository @Inject constructor(
    private val api: DiscoveryApi,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) {

    /** The whole taxonomy of one kind, normalised. */
    suspend fun topics(kind: TopicKind): AppResult<List<Topic>> {
        val wire = if (kind == TopicKind.SHORT) "short" else "long"
        // Go writes a nil slice as null: an empty taxonomy, not a malformed answer.
        return when (val result = apiCall(errorMapper) { api.categories(wire).let { it.copy(data = it.data.orEmpty()) } }) {
            is AppResult.Success -> AppResult.Success(
                normalizeTopics(result.data).let { if (kind == TopicKind.SHORT) shortTopics(it) else longTopics(it) },
            )
            is AppResult.Failure -> result
        }
    }

    /**
     * One viewer action's worth of the Explore grid for [category] ("" is
     * All). A malformed category is refused here, before any request, with
     * the code the server would answer.
     */
    suspend fun explorePage(category: String, cursor: String?): AppResult<ExplorePage> {
        val (slug, valid) = normalizeExploreCategory(category)
        if (!valid) return AppResult.Failure(AppError.Unknown(code = INVALID_CATEGORY, statusCode = BAD_REQUEST))
        return walkExplore(cursor) { at ->
            surfacePage(REELS, at, EXPLORE_PAGE_SIZE, category = slug.ifEmpty { null }, sort = null)
        }
    }

    /** A topic's long videos, `GET /v1/feed/videos?category=&sort=`. */
    suspend fun topicPage(slug: String, sort: TopicSort, cursor: String?): AppResult<DiscoveryPage> =
        when (
            val result = surfacePage(
                VIDEOS,
                cursor,
                TOPIC_PAGE_SIZE,
                category = slug.trim().lowercase(),
                sort = sort.wire,
            )
        ) {
            is AppResult.Success -> AppResult.Success(DiscoveryPage(result.data.first, result.data.second))
            is AppResult.Failure -> result
        }

    /** The ranked trending long videos, a page at a time. */
    suspend fun trendingPage(cursor: String?): AppResult<DiscoveryPage> {
        var meta: String? = null
        val result = apiCall(errorMapper) {
            api.trending(LONG_VIDEO, TRENDING_PAGE_SIZE, cursor).also { meta = it.meta?.nextCursor }
        }
        return when (result) {
            is AppResult.Success -> {
                val rows = result.data.items.filter { it.deletedAt.isBlank() }.map { it.toDomain() }
                // Both placements carry the cursor (handler.go:2724); meta wins, a blank one is none.
                val next = (meta ?: result.data.nextCursor)?.takeIf { it.isNotBlank() }
                AppResult.Success(DiscoveryPage(hydrator.hydrate(rows), next))
            }
            is AppResult.Failure -> result
        }
    }

    private suspend fun surfacePage(
        surface: String,
        cursor: String?,
        limit: Int,
        category: String?,
        sort: String?,
    ): AppResult<Pair<List<FeedItem>, String?>> {
        var next: String? = null
        val result = apiCall(errorMapper) {
            api.feed(surface, limit, cursor, category, sort).let { envelope ->
                next = envelope.meta?.nextCursor
                envelope.copy(data = envelope.data.orEmpty())
            }
        }
        return when (result) {
            is AppResult.Success -> {
                val rows = result.data.filter { it.deletedAt.isBlank() }.map { it.toDomain() }
                AppResult.Success(hydrator.hydrate(rows) to next)
            }
            is AppResult.Failure -> result
        }
    }

    private companion object {
        const val REELS = "reels"
        const val VIDEOS = "videos"
        const val LONG_VIDEO = "long_video"
        const val TOPIC_PAGE_SIZE = 20
        const val TRENDING_PAGE_SIZE = 24
        const val BAD_REQUEST = 400
    }
}
