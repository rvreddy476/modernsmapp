package com.us.android.feature.search.data

import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import retrofit2.http.GET
import retrofit2.http.Query

/**
 * The three searches (contract of 2026-09-05, being finished server-side
 * in parallel):
 *
 *  - `GET v1/search/users?q=` — search-service; rows `id`, `username`,
 *    `display_name`, `avatar_url` (older builds say `user_id`).
 *  - `GET v1/search/posts?q=&type=` — search-service; `type` omitted for
 *    posts, `videos`, `flicks`; rows `id`, `author{…}`, `title`, `text`,
 *    `content_type`, `created_at`, `thumbnail_url`, `duration_ms`.
 *  - `GET v1/channels/search?q=&limit=` — post-service; channel JSON.
 *
 * `data` is read as a raw element: the people search wrapped its rows in
 * `{"items": […]}` while the new contract is a bare array, and a page that
 * decodes both is the one that survives the server landing first or last.
 * See [SearchRows.rowsOf].
 */
interface SearchApi {

    @GET("v1/search/users")
    suspend fun users(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/posts")
    suspend fun posts(
        @Query("q") query: String,
        @Query("type") type: String?,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/channels/search")
    suspend fun channels(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>
}

@Serializable
data class UserHitDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
) {
    /** The new contract's `id`, else the older `user_id`. */
    val key: String get() = id.ifBlank { userId }
}

@Serializable
data class PostHitAuthorDto(
    val id: String = "",
    @SerialName("display_name") val displayName: String = "",
    val username: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
)

@Serializable
data class PostHitDto(
    val id: String = "",
    val author: PostHitAuthorDto = PostHitAuthorDto(),
    val title: String = "",
    val text: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    @SerialName("duration_ms") val durationMs: Long = 0L,
)

/** The rows of a search page, whatever shape the page came in. */
object SearchRows {

    /** The keys a wrapped page has been seen to use, in the order they are tried. */
    private val WRAPPER_KEYS = listOf("items", "results", "users", "posts", "channels", "hits")

    /** A bare array is the rows; an object is unwrapped through the first array-valued key it has. */
    fun rowsOf(data: JsonElement?): List<JsonElement> = when (data) {
        is JsonArray -> data.toList()
        is JsonObject -> WRAPPER_KEYS.firstNotNullOfOrNull { key -> data[key] as? JsonArray }?.toList().orEmpty()
        else -> emptyList()
    }

    /** Decodes each row; a row that does not decode is dropped rather than failing the page. */
    inline fun <reified T> decode(json: Json, data: JsonElement?): List<T> =
        rowsOf(data).mapNotNull { row -> runCatching { json.decodeFromJsonElement<T>(row) }.getOrNull() }

    fun users(json: Json, data: JsonElement?): List<UserHitDto> = decode(json, data)
    fun posts(json: Json, data: JsonElement?): List<PostHitDto> = decode(json, data)
    fun channels(json: Json, data: JsonElement?): List<ChannelDto> = decode(json, data)
}
