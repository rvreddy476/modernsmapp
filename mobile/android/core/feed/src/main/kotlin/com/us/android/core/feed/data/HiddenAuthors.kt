package com.us.android.core.feed.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.profile.data.FollowListApi
import com.us.android.core.profile.data.ProfileBatchRequest
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import javax.inject.Inject
import javax.inject.Singleton

/**
 * feed-service's list of the channels the viewer asked not to be recommended
 * ("Don't recommend this channel"), `GetMutedAuthors`. Its own interface so
 * the many fakes of [FeedApi] keep their shape; the undo is the existing
 * [FeedApi.feedback] with the author scope and `interested`.
 */
interface HiddenAuthorsApi {

    /** `GET /v1/feed/feedback/authors`: newest first, always a list (`[]`, never null, per the handler). */
    @GET("v1/feed/feedback/authors")
    suspend fun hiddenAuthors(): ApiEnvelope<List<HiddenAuthorDto>>
}

/** `postgres.MutedAuthor`: who, and since when. */
@Serializable
data class HiddenAuthorDto(
    @SerialName("author_id") val authorId: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

/** One hidden channel as the page draws it. A blank name means the profile could not be read. */
data class HiddenChannel(
    val authorId: String,
    val name: String,
    val username: String,
    val avatarMediaId: String?,
    /** When it was hidden (`created_at`), for "Hidden 3 Oct". */
    val hiddenAt: String,
)

/** What the Hidden channels page reads and writes; a seam so its ViewModel is tested against a fake. */
interface HiddenAuthorsSource {
    suspend fun hidden(): AppResult<List<HiddenChannel>>

    /** "Show again": the author-scoped `interested`, which undoes the hide. */
    suspend fun showAgain(authorId: String): AppResult<Unit>
}

/**
 * Settings › Hidden channels (web parity, 2026-10-22): the list, named
 * through the profiles batch the web uses (`useBatchProfiles`), and the
 * undo. A failed profile read does not fail the page: the rows still list,
 * and still undo, under "Channel" — the web's own fallback.
 */
@Singleton
class HiddenAuthorsRepository @Inject constructor(
    private val api: HiddenAuthorsApi,
    private val feed: FeedApi,
    private val profiles: FollowListApi,
    private val errorMapper: ErrorMapper,
) : HiddenAuthorsSource {

    override suspend fun hidden(): AppResult<List<HiddenChannel>> {
        val rows = when (val result = listApiCall(errorMapper) { api.hiddenAuthors() }) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data.filter { it.authorId.isNotBlank() }.distinctBy { it.authorId }
        }
        if (rows.isEmpty()) return AppResult.Success(emptyList())
        val found = profileMap(rows.map { it.authorId })
        return AppResult.Success(
            rows.map { row ->
                val profile = found[row.authorId]
                HiddenChannel(
                    authorId = row.authorId,
                    name = profile?.displayName?.trim().orEmpty(),
                    username = profile?.username?.trim().orEmpty(),
                    avatarMediaId = profile?.avatarMediaId?.takeIf { it.isNotBlank() },
                    hiddenAt = row.createdAt,
                )
            },
        )
    }

    override suspend fun showAgain(authorId: String): AppResult<Unit> =
        apiCall(errorMapper) {
            feed.feedback(FeedFeedbackRequest.forAuthor(authorId, FeedApi.FEEDBACK_INTERESTED))
        }.map { }

    /** The profiles batch, at most [PROFILE_BATCH] ids a call; empty on any failure (the names are a nicety). */
    @Suppress("TooGenericExceptionCaught")
    private suspend fun profileMap(ids: List<String>) = buildMap {
        ids.chunked(PROFILE_BATCH).forEach { chunk ->
            try {
                profiles.profiles(ProfileBatchRequest(chunk)).forEach { (id, profile) -> if (profile != null) put(id, profile) }
            } catch (e: CancellationException) {
                throw e
            } catch (_: Throwable) {
                // Rows without names still list and still undo.
            }
        }
    }

    private companion object {
        /** profile-service's batch cap. */
        const val PROFILE_BATCH = 100
    }
}
