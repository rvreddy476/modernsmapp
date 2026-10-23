package com.us.android.core.feed.data.credits

import androidx.paging.PagingData
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.feedPager
import com.us.android.core.feed.data.toFeedPage
import com.us.android.core.model.CreditedUser
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.pagedApiCall
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/** One genre the server offers: [slug] goes on the wire, [label] is shown. */
data class CreditGenre(val slug: String, val label: String)

/** One pending request to be credited, as the requests screen draws it. */
data class CreditRequest(
    val id: String,
    val role: String,
    /** The name the uploader typed for the viewer. */
    val name: String,
    val postId: String,
    val postTitle: String,
    val coverUrl: String?,
    val channelName: String,
    val from: CreditedUser,
    val createdAt: String,
)

/**
 * Video credits' own reads and writes (2026-10-23): the genre list, the
 * viewer's pending requests and their answers, and the "Appears in" list.
 * Credits themselves are written with the post (create and PATCH).
 */
@Singleton
class CreditsRepository @Inject constructor(
    private val api: CreditsApi,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) {

    private val genresLock = Mutex()
    private var cachedGenres: List<CreditGenre>? = null

    /**
     * The server's genre list, read once per process and kept: it is a
     * taxonomy, not a feed. Null when it could not be read, so the editor
     * says so and offers Retry instead of an empty picker.
     */
    suspend fun genres(): List<CreditGenre>? = genresLock.withLock {
        cachedGenres?.let { return@withLock it }
        when (val result = listApiCall(errorMapper) { api.genres() }) {
            is AppResult.Success -> result.data
                .filter { it.slug.isNotBlank() }
                .map { CreditGenre(slug = it.slug, label = it.label.ifBlank { it.slug }) }
                .also { cachedGenres = it }
            is AppResult.Failure -> null
        }
    }

    /** One page of the viewer's pending requests, newest first. */
    suspend fun requests(cursor: String? = null): AppResult<Paged<CreditRequest>> =
        pagedApiCall(errorMapper) { api.requests(cursor) }.map { page ->
            Paged(
                items = page.items.filter { it.id.isNotBlank() }.map { it.toDomain() },
                nextCursor = page.nextCursor,
            )
        }

    suspend fun accept(creditId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.accept(creditId) }.answered()

    suspend fun decline(creditId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.decline(creditId) }.answered()

    /**
     * A 2xx is the answer the screen needs; the `{status}` body only repeats
     * what was asked. A 2xx without a `data` body is therefore still a
     * success here, not "malformed".
     */
    private fun AppResult<CreditStatusDto>.answered(): AppResult<Unit> = when (this) {
        is AppResult.Success -> AppResult.Success(Unit)
        is AppResult.Failure -> if (error is AppError.Malformed) AppResult.Success(Unit) else AppResult.Failure(error)
    }

    /**
     * "Remove me from this video": the tagged viewer's link goes, the name
     * stays plain — `{status:"unlinked"}`, on a pending tag as on an accepted
     * one. A 404 is what a repeat answers (and what a credit the uploader has
     * since removed answers): either way the viewer is not linked to it any
     * more, which is what was asked, so it is a success here.
     */
    suspend fun removeMe(creditId: String): AppResult<Unit> =
        when (val result = apiCall(errorMapper) { api.removeMe(creditId) }.answered()) {
            is AppResult.Failure -> if (result.error is AppError.NotFound) AppResult.Success(Unit) else result
            is AppResult.Success -> result
        }

    /**
     * The long videos [userId] accepted a credit on, newest first — channel
     * video rows (`GET /v1/creators/:creatorId/appearances`), hydrated like
     * the channel's own videos. Deleted rows are dropped by [toFeedPage], as
     * on every list. The rows carry no `credits`: the watch page reads those
     * from the post detail.
     */
    fun appearances(userId: String): Flow<PagingData<FeedItem>> = feedPager(errorMapper) { request ->
        val page = api.appearances(userId, request.limit, request.cursor).toFeedPage()
        page.copy(items = hydrator.hydrate(page.items))
    }

    /**
     * Whether [userId] appears in anything at all — one row asked for — so
     * the "Appears in" tab is offered only when it would not be empty. A
     * failed read answers false: a tab that may be empty is not shown.
     */
    suspend fun hasAppearances(userId: String): Boolean =
        when (val result = listApiCall(errorMapper) { api.appearances(userId, limit = 1) }) {
            is AppResult.Success -> result.data.any { it.deletedAt.isBlank() }
            is AppResult.Failure -> false
        }
}

private fun CreditRequestDto.toDomain() = CreditRequest(
    id = id,
    role = role,
    name = name,
    postId = post.id,
    postTitle = post.title,
    coverUrl = post.coverUrl?.takeIf { it.isNotBlank() },
    channelName = post.channelName,
    from = from.toDomain(),
    createdAt = createdAt,
)
