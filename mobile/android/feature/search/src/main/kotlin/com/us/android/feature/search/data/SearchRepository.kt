package com.us.android.feature.search.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.feed.data.SoundRowDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The search page's data: one search per tab, the suggestions while
 * typing, the viewer's recents and the popular searches.
 *
 * A 404 on a search — the endpoint not yet deployed — is an empty page,
 * not a failure: a tab the server does not answer yet must read as
 * "nothing found", never as a broken screen. Every other failure is
 * carried through, in words the page can show with Retry.
 */
@Singleton
class SearchRepository @Inject constructor(
    private val api: SearchApi,
    private val errorMapper: ErrorMapper,
    private val json: Json,
) {

    suspend fun search(scope: SearchScope, query: String): AppResult<List<SearchHit>> {
        val record = if (scope.recordsHistory) RECORD else null
        return when (scope) {
            SearchScope.TOP -> page({ api.top(query, TOP_TYPES, TOP_LIMIT) }) { data -> topHits(SearchRows.top(json, data)) }
            SearchScope.PEOPLE -> page({ api.users(query, LIMIT, record) }) { data ->
                SearchRows.users(json, data).filter { it.key.isNotBlank() }.map { it.toHit() }
            }
            SearchScope.VIDEOS, SearchScope.REELS -> page({ api.posts(query, scope.wireType, LIMIT) }) { data ->
                val reels = scope == SearchScope.REELS
                SearchRows.posts(json, data).filter { it.key.isNotBlank() }.map { it.toVideo(isReel = reels) }
            }
            SearchScope.SOUNDS -> page({ api.sounds(query, LIMIT, 0) }) { data ->
                SearchRows.sounds(json, data).filter { it.id.isNotBlank() }.map { it.toHit() }
            }
            SearchScope.HASHTAGS -> page({ api.hashtags(query, LIMIT, record) }) { data ->
                SearchRows.hashtags(data).map { SearchHit.Hashtag(it) }
            }
            SearchScope.CHANNELS -> page({ api.channels(query, LIMIT) }) { data ->
                SearchRows.channels(json, data).filter { it.userId.isNotBlank() }.map { it.toHit() }
            }
        }
    }

    /** Suggestions for what is being typed. Never recorded; a failure is simply no suggestions. */
    suspend fun suggestions(query: String): List<SearchSuggestion> =
        when (val result = apiCall(errorMapper) { api.autocomplete(query, SUGGESTION_LIMIT) }) {
            is AppResult.Success -> SearchRows.autocomplete(json, result.data).mapNotNull { it.toSuggestion() }.distinct()
            is AppResult.Failure -> emptyList()
        }

    /** The signed-in viewer's recent searches, newest first. Call only when signed in. */
    suspend fun history(): AppResult<List<RecentSearch>> =
        apiCall(errorMapper) { api.history(HISTORY_LIMIT) }.map { data ->
            SearchRows.history(json, data)
                .filter { it.id.isNotBlank() && it.query.isNotBlank() }
                .map { RecentSearch(id = it.id, query = it.query.trim()) }
        }

    /** Removes ONE past search. Already gone (404) is the outcome asked for, so it is a success. */
    suspend fun deleteHistoryItem(id: String): AppResult<Unit> =
        unit(apiCall(errorMapper) { api.deleteHistoryItem(id) }, notFoundIsSuccess = true)

    /** Clears every past search. */
    suspend fun clearHistory(): AppResult<Unit> =
        unit(apiCall(errorMapper) { api.clearHistory() }, notFoundIsSuccess = false)

    /** The viewer's saved searches (2026-10-24), as the server orders them. Call only when signed in. */
    suspend fun saved(): AppResult<List<SavedSearch>> =
        apiCall(errorMapper) { api.saved() }.map { page ->
            page.items.filter { it.id.isNotBlank() && it.query.isNotBlank() }
                .map { SavedSearch(id = it.id, query = it.query.trim()) }
        }

    /** Saves [query] (trimmed) as a universal search; the stored row back. */
    suspend fun save(query: String): AppResult<SavedSearch> =
        apiCall(errorMapper) { api.save(SaveSearchRequest(query = query.trim(), searchType = UNIVERSAL)) }
            .map { SavedSearch(id = it.id, query = it.query.ifBlank { query }.trim()) }

    /** Removes one saved search. Already gone (404) is the outcome asked for. */
    suspend fun deleteSaved(id: String): AppResult<Unit> =
        unit(apiCall(errorMapper) { api.deleteSaved(id) }, notFoundIsSuccess = true)

    /** Popular searches for the empty box. An empty state never becomes an error: a failure is none. */
    suspend fun popular(): List<String> =
        when (val result = apiCall(errorMapper) { api.popular(POPULAR_LIMIT) }) {
            is AppResult.Success -> SearchRows.popular(result.data)
            is AppResult.Failure -> emptyList()
        }

    private fun unit(result: AppResult<JsonElement>, notFoundIsSuccess: Boolean): AppResult<Unit> = when (result) {
        is AppResult.Success -> AppResult.Success(Unit)
        is AppResult.Failure ->
            if (notFoundIsSuccess && result.error is AppError.NotFound) AppResult.Success(Unit) else result
    }

    private suspend fun page(
        call: suspend () -> ApiEnvelope<JsonElement>,
        map: (JsonElement?) -> List<SearchHit>,
    ): AppResult<List<SearchHit>> = when (val result = apiCall(errorMapper, call)) {
        is AppResult.Success -> AppResult.Success(map(result.data))
        is AppResult.Failure -> if (result.error is AppError.NotFound) AppResult.Success(emptyList()) else result
    }

    companion object {
        /** search-service's own default kind of saved search. */
        const val UNIVERSAL = "universal"
        const val LIMIT = 30
        const val TOP_LIMIT = 20
        const val TOP_TYPES = "posts,users,hashtags"

        /** How many people and tags lead the Top tab before its posts. */
        const val TOP_PEOPLE = 3
        const val TOP_TAGS = 3
        const val SUGGESTION_LIMIT = 8
        const val HISTORY_LIMIT = 20
        const val POPULAR_LIMIT = 10
        private const val RECORD = 1

        /** Why a search failed, in words the page can show. */
        fun errorMessage(error: AppError): String = when (error) {
            is AppError.NoNetwork -> "You're offline. Check your connection and try again."
            is AppError.Timeout -> "That took too long. Try again."
            is AppError.RateLimited -> "Too many searches. Give it a moment."
            else -> "We couldn't search right now. Try again."
        }

        /**
         * Top, as one list: a few people, a few tags, then the posts — each
         * post drawn as a reel, a video or a post by its content type.
         */
        internal fun topHits(rows: TopRows): List<SearchHit> {
            val people = rows.users.filter { it.key.isNotBlank() }.take(TOP_PEOPLE).map { it.toHit() }
            val tags = rows.hashtags.take(TOP_TAGS).map { SearchHit.Hashtag(it) }
            val posts = rows.posts.filter { it.key.isNotBlank() }.map { post ->
                when (post.contentType.lowercase()) {
                    "flick", "reel" -> post.toVideo(isReel = true)
                    "long_video", "video" -> post.toVideo(isReel = false)
                    else -> post.toPost()
                }
            }
            return people + tags + posts
        }
    }
}

private fun UserHitDto.toHit() = SearchHit.User(
    id = key,
    username = username,
    displayName = displayName,
    avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
)

private fun PostHitAuthorDto.toAuthor() = SearchAuthor(
    id = id,
    displayName = displayName,
    username = username,
    avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
)

private fun PostHitDto.toPost() = SearchHit.Post(
    id = key,
    author = author.toAuthor(),
    text = text,
    title = title,
    createdAt = createdAt,
)

private fun PostHitDto.toVideo(isReel: Boolean) = SearchHit.Video(
    id = key,
    title = title.ifBlank { text },
    author = author.toAuthor(),
    thumbnailUrl = thumbnailUrl?.takeIf { it.isNotBlank() },
    durationMs = durationMs,
    createdAt = createdAt,
    isReel = isReel,
)

private fun ChannelDto.toHit() = SearchHit.Channel(
    id = userId,
    name = name,
    handle = handle,
    avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
    videoCount = videoCount,
)

private fun SoundRowDto.toHit() = SearchHit.Sound(
    id = id,
    title = title,
    artist = artist,
    durationMs = durationMs,
    usageCount = usageCount,
    isOriginal = isOriginal,
)

private fun AutocompleteDto.toSuggestion(): SearchSuggestion? = when (kind.lowercase()) {
    "", "user" -> if (userId.isBlank()) null else SearchSuggestion.Person(userId, username, displayName)
    "hashtag" -> hashtag.trim().removePrefix("#").takeIf { it.isNotBlank() }?.let { SearchSuggestion.Tag(it) }
    else -> name.ifBlank { handle }.trim().takeIf { it.isNotBlank() }?.let { SearchSuggestion.Query(it) }
}
