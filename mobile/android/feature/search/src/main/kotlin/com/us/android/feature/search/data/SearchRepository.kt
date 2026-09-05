package com.us.android.feature.search.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.search.navigation.SearchScope
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import javax.inject.Inject
import javax.inject.Singleton

/**
 * One search, by scope. A 404 — the endpoint not yet deployed — is an
 * empty page, not a failure: the server is being finished in parallel and
 * a scope it does not answer yet must read as "nothing found", never as a
 * broken screen. Every other failure is carried through, in words the page
 * can show with Retry.
 */
@Singleton
class SearchRepository @Inject constructor(
    private val api: SearchApi,
    private val errorMapper: ErrorMapper,
    private val json: Json,
) {

    suspend fun search(scope: SearchScope, query: String): AppResult<List<SearchHit>> = when (scope) {
        SearchScope.USERS -> page({ api.users(query, LIMIT) }) { data ->
            SearchRows.users(json, data).filter { it.key.isNotBlank() }.map { it.toHit() }
        }
        SearchScope.POSTS -> page({ api.posts(query, scope.wireType, LIMIT) }) { data ->
            SearchRows.posts(json, data).filter { it.id.isNotBlank() }.map { it.toPost() }
        }
        SearchScope.REELS, SearchScope.VIDEOS -> page({ api.posts(query, scope.wireType, LIMIT) }) { data ->
            val reels = scope == SearchScope.REELS
            SearchRows.posts(json, data).filter { it.id.isNotBlank() }.map { it.toVideo(isReel = reels) }
        }
        SearchScope.CHANNELS -> page({ api.channels(query, LIMIT) }) { data ->
            SearchRows.channels(json, data).filter { it.userId.isNotBlank() }.map { it.toHit() }
        }
    }

    private suspend fun page(
        call: suspend () -> ApiEnvelope<JsonElement>,
        map: (JsonElement?) -> List<SearchHit>,
    ): AppResult<List<SearchHit>> = when (val result = apiCall(errorMapper, call)) {
        is AppResult.Success -> AppResult.Success(map(result.data))
        is AppResult.Failure -> if (result.error is AppError.NotFound) AppResult.Success(emptyList()) else result
    }

    companion object {
        const val LIMIT = 30

        /** Why a search failed, in words the page can show. */
        fun errorMessage(error: AppError): String = when (error) {
            is AppError.NoNetwork -> "You're offline. Check your connection and try again."
            is AppError.Timeout -> "That took too long. Try again."
            is AppError.RateLimited -> "Too many searches. Give it a moment."
            else -> "We couldn't search right now. Try again."
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
    id = id,
    author = author.toAuthor(),
    text = text,
    title = title,
    createdAt = createdAt,
)

private fun PostHitDto.toVideo(isReel: Boolean) = SearchHit.Video(
    id = id,
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
