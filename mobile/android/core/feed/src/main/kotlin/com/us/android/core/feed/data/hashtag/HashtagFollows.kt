package com.us.android.core.feed.data.hashtag

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Followed hashtags (2026-10-24, server commit 546eec063).
 *
 * Authority: post-service internal/http/hashtag_follows_handler.go (routes
 * :16-27, refusals :30-45) and service/hashtag_follows.go (the answers
 * :86-90 and :154-158). Signed-in only. The posts of the followed tags are
 * `GET /v1/hashtags/followed/posts` — the hashtag page's rows — read through
 * FeedApi like a tag's own posts.
 */

/** `{tag, display_name, following}` — service/hashtag_follows.go:86-90; every follow route answers it. */
@Serializable
data class HashtagFollowStateDto(
    val tag: String = "",
    @SerialName("display_name") val displayName: String = "",
    val following: Boolean = false,
)

/** One followed tag — service/hashtag_follows.go:154-158. */
@Serializable
data class FollowedHashtagDto(
    val tag: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("followed_at") val followedAt: String = "",
)

interface HashtagFollowApi {
    @GET("v1/hashtags/{tag}/follow")
    suspend fun state(@Path("tag") tag: String): ApiEnvelope<HashtagFollowStateDto>

    /** Idempotent: a second follow answers `following:true` again. */
    @POST("v1/hashtags/{tag}/follow")
    suspend fun follow(@Path("tag") tag: String): ApiEnvelope<HashtagFollowStateDto>

    /** Idempotent, like [follow]. */
    @DELETE("v1/hashtags/{tag}/follow")
    suspend fun unfollow(@Path("tag") tag: String): ApiEnvelope<HashtagFollowStateDto>

    /** Newest follow first; `meta.next_cursor` while there is more (handler :89-105, limit ≤ 100). */
    @GET("v1/hashtags/followed")
    suspend fun followed(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<FollowedHashtagDto>>
}

/** A tag and whether the viewer follows it. [tag] is the stored form: lower case, no `#`. */
data class HashtagFollow(val tag: String, val label: String, val following: Boolean)

/** One followed tag on the list. */
data class FollowedHashtag(val tag: String, val label: String)

/** One page of the list. */
data class FollowedHashtagsPage(val tags: List<FollowedHashtag>, val nextCursor: String?)

/** service/hashtag_follows.go `ErrHashtagFollowLimit`: a viewer follows at most this many tags. */
const val MAX_FOLLOWED_HASHTAGS = 500

/**
 * The key a tag is addressed and remembered by: the server's normalisation
 * (explicit_tags.go:68-72) — trimmed, the leading `#` dropped, lower case.
 */
fun hashtagKey(raw: String): String = raw.trim().removePrefix("#").lowercase()

/** `#tag` for a tag, the server's display name when it sent one. */
fun hashtagLabel(tag: String, displayName: String = ""): String =
    displayName.takeIf { it.isNotBlank() } ?: "#${hashtagKey(tag)}"

fun HashtagFollowStateDto.toFollow(asked: String): HashtagFollow {
    val key = hashtagKey(tag.ifBlank { asked })
    return HashtagFollow(key, hashtagLabel(key, displayName), following)
}

/** The list's rows: blank tags and repeats dropped, in the server's order. */
fun List<FollowedHashtagDto>.toFollowedTags(): List<FollowedHashtag> {
    val seen = mutableSetOf<String>()
    return mapNotNull { row ->
        val key = hashtagKey(row.tag)
        if (key.isEmpty() || !seen.add(key)) null else FollowedHashtag(key, hashtagLabel(key, row.displayName))
    }
}

/** What the viewer was doing when a follow route failed. */
enum class HashtagFollowAction(val fallback: String) {
    LOAD("We couldn't check this hashtag. Try again."),
    FOLLOW("That hashtag couldn't be followed. Try again."),
    UNFOLLOW("That hashtag couldn't be unfollowed. Try again."),
    LIST("Your followed hashtags couldn't load. Try again."),
}

/** The refusals of writeHashtagFollowError (hashtag_follows_handler.go:30-45), as sentences. */
private val HASHTAG_REFUSALS = mapOf(
    "INVALID_HASHTAG" to "That isn't a hashtag that can be followed. A hashtag is 1 to 50 letters, numbers or underscores.",
    "HASHTAG_UNAVAILABLE" to "This hashtag is unavailable, so it can't be followed.",
    "HASHTAG_FOLLOW_LIMIT" to "You can follow up to $MAX_FOLLOWED_HASHTAGS hashtags. Unfollow one to follow another.",
    "STORE_UNAVAILABLE" to "Hashtags are unavailable right now. Try again in a moment.",
)

private fun AppError.hashtagCode(): String? = when (this) {
    is AppError.Unknown -> code
    is AppError.Server -> code
    is AppError.NotFound -> code
    else -> null
}

/** A failed follow route, in one sentence. */
fun hashtagFollowRefusal(error: AppError, action: HashtagFollowAction): String {
    error.hashtagCode()?.let { code -> HASHTAG_REFUSALS[code]?.let { return it } }
    return when (error) {
        is AppError.NoNetwork, is AppError.Timeout -> "You seem to be offline. Check your connection and try again."
        is AppError.AuthFailed -> "Log in again to follow hashtags."
        is AppError.RateLimited -> "Too many changes at once. Wait a moment and try again."
        else -> action.fallback
    }
}

/**
 * Follow state per tag for this session, shared by the hashtag page and the
 * followed list, so Follow on one and Unfollow on the other agree without a
 * refetch. A tag absent from the map has not been read yet.
 */
@Singleton
class HashtagFollowSession @Inject constructor() {
    private val _following = MutableStateFlow<Map<String, Boolean>>(emptyMap())
    val following: StateFlow<Map<String, Boolean>> = _following.asStateFlow()

    fun record(tag: String, following: Boolean) = _following.update { it + (hashtagKey(tag) to following) }
}

@Singleton
class HashtagFollowRepository @Inject constructor(
    private val api: HashtagFollowApi,
    private val session: HashtagFollowSession,
    private val errorMapper: ErrorMapper,
) {
    val following: StateFlow<Map<String, Boolean>> get() = session.following

    suspend fun state(tag: String): AppResult<HashtagFollow> = remember(tag) { api.state(hashtagKey(tag)) }

    /** Follows, or unfollows, and answers the state the server now holds. */
    suspend fun setFollowing(tag: String, follow: Boolean): AppResult<HashtagFollow> = remember(tag) {
        if (follow) api.follow(hashtagKey(tag)) else api.unfollow(hashtagKey(tag))
    }

    /** One page of the list. The whole envelope travels as the payload, because the cursor is in `meta`. */
    suspend fun followed(limit: Int, cursor: String?): AppResult<FollowedHashtagsPage> =
        apiCall(errorMapper) { api.followed(limit, cursor).let { ApiEnvelope(data = it, error = it.error, meta = it.meta) } }
            .map { envelope ->
                FollowedHashtagsPage(
                    tags = envelope.data.orEmpty().toFollowedTags(),
                    nextCursor = envelope.meta?.nextCursor?.takeIf { it.isNotBlank() },
                )
            }
            .also { result -> if (result is AppResult.Success) result.data.tags.forEach { session.record(it.tag, true) } }

    private suspend fun remember(
        tag: String,
        call: suspend () -> ApiEnvelope<HashtagFollowStateDto>,
    ): AppResult<HashtagFollow> =
        apiCall(errorMapper) { call() }
            .map { it.toFollow(tag) }
            .also { if (it is AppResult.Success) session.record(it.data.tag, it.data.following) }
}
