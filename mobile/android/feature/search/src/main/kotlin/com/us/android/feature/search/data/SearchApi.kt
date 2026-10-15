package com.us.android.feature.search.data

import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.feed.data.SoundRowDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.decodeFromJsonElement
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The search page's routes (search-service unless noted; T-15/T-16,
 * 2026-10-14):
 *
 *  - `GET v1/search?q=&types=posts,users,hashtags` — Top, the ranked
 *    grouped shape: `{"results":{"posts":{"items"}, "users":{"items"},
 *    "hashtags":{"items"}}}`.
 *  - `GET v1/search/users?q=&record=1` — People; rows `user_id` (older
 *    builds `id`), `username`, `display_name`, `avatar_url`.
 *  - `GET v1/search/posts?q=&type=videos|flicks` — Videos and Reels; rows
 *    `id`, `author{…}`, `title`, `text`, `content_type`, `thumbnail_url`,
 *    `duration_ms`.
 *  - `GET v1/search/hashtags?q=&record=1` — Hashtags; `{"hashtags":[…]}`.
 *  - `GET v1/audio/search?q=&limit=&offset=` — media-service; Sounds,
 *    `{"tracks":[…]}`.
 *  - `GET v1/channels/search?q=` — post-service; the video app's channels.
 *  - `GET v1/search/autocomplete?q=` — suggestions while typing.
 *  - `GET v1/search/history`, `DELETE v1/search/history/{id}`,
 *    `DELETE v1/search/history` — the signed-in viewer's recents.
 *  - `GET v1/search/popular?limit=` — popular searches, `[{"query"}]`.
 *
 * `record` is sent ONLY by People and Hashtags, and only from this page:
 * those routes also serve pickers whose keystrokes are not searches. Top
 * and the posts search record a signed-in first page by default.
 *
 * `data` is read as a raw element: a page that decodes both a bare array
 * and an `{"items": […]}` wrapper survives the server landing first or
 * last. See [SearchRows.rowsOf].
 */
interface SearchApi {

    @GET("v1/search")
    suspend fun top(
        @Query("q") query: String,
        @Query("types") types: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/users")
    suspend fun users(
        @Query("q") query: String,
        @Query("limit") limit: Int,
        @Query("record") record: Int? = null,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/posts")
    suspend fun posts(
        @Query("q") query: String,
        @Query("type") type: String?,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/hashtags")
    suspend fun hashtags(
        @Query("q") query: String,
        @Query("limit") limit: Int,
        @Query("record") record: Int? = null,
    ): ApiEnvelope<JsonElement>

    @GET("v1/audio/search")
    suspend fun sounds(
        @Query("q") query: String,
        @Query("limit") limit: Int,
        @Query("offset") offset: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/channels/search")
    suspend fun channels(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/autocomplete")
    suspend fun autocomplete(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>

    @GET("v1/search/history")
    suspend fun history(@Query("limit") limit: Int): ApiEnvelope<JsonElement>

    @DELETE("v1/search/history/{id}")
    suspend fun deleteHistoryItem(@Path("id") id: String): ApiEnvelope<JsonElement>

    @DELETE("v1/search/history")
    suspend fun clearHistory(): ApiEnvelope<JsonElement>

    @GET("v1/search/popular")
    suspend fun popular(@Query("limit") limit: Int): ApiEnvelope<JsonElement>
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
    @SerialName("post_id") val postId: String = "",
    val author: PostHitAuthorDto = PostHitAuthorDto(),
    val title: String = "",
    val text: String = "",
    @SerialName("content_type") val contentType: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    @SerialName("duration_ms") val durationMs: Long = 0L,
) {
    /** The row's `id`, else the document's `post_id` (the ranked Top bucket carries both). */
    val key: String get() = id.ifBlank { postId }
}

/** One past search: `{id, user_id, query, searched_at}`. */
@Serializable
data class HistoryItemDto(
    val id: String = "",
    val query: String = "",
    @SerialName("searched_at") val searchedAt: String = "",
)

/** One `GET v1/search/autocomplete` row; [kind] is `user`, `hashtag` or `community` (blank on older builds: a user). */
@Serializable
data class AutocompleteDto(
    val kind: String = "",
    @SerialName("user_id") val userId: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    val hashtag: String = "",
    val handle: String = "",
    val name: String = "",
)

/** Top's three buckets, each already decoded. */
data class TopRows(
    val users: List<UserHitDto>,
    val posts: List<PostHitDto>,
    val hashtags: List<String>,
)

/** The rows of a search page, whatever shape the page came in. */
object SearchRows {

    /** The keys a wrapped page has been seen to use, in the order they are tried. */
    private val WRAPPER_KEYS = listOf("items", "results", "users", "posts", "channels", "hits", "hashtags", "tracks")

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
    fun sounds(json: Json, data: JsonElement?): List<SoundRowDto> = decode(json, data)
    fun history(json: Json, data: JsonElement?): List<HistoryItemDto> = decode(json, data)
    fun autocomplete(json: Json, data: JsonElement?): List<AutocompleteDto> = decode(json, data)

    /**
     * Tags, without their `#`, de-duplicated case-insensitively: the
     * hashtags route answers bare strings, Top's bucket answers
     * `{"hashtag": …}` objects.
     */
    fun hashtags(data: JsonElement?): List<String> = rowsOf(data)
        .mapNotNull { row ->
            when (row) {
                is JsonPrimitive -> row.contentOrNull
                is JsonObject -> (row["hashtag"] as? JsonPrimitive)?.contentOrNull
                else -> null
            }
        }
        .map { it.trim().removePrefix("#").trim() }
        .filter { it.isNotEmpty() }
        .distinctBy { it.lowercase() }

    /** Popular searches: `[{"query": …}]` (or a bare list of strings), trimmed, no blanks, no repeats. */
    fun popular(data: JsonElement?): List<String> = rowsOf(data)
        .mapNotNull { row ->
            when (row) {
                is JsonPrimitive -> row.contentOrNull
                is JsonObject -> (row["query"] as? JsonPrimitive)?.contentOrNull
                else -> null
            }
        }
        .map { it.trim() }
        .filter { it.isNotEmpty() }
        .distinctBy { it.lowercase() }

    /** Top's `{"results": {"users": {"items"}, "posts": {"items"}, "hashtags": {"items"}}}`. */
    fun top(json: Json, data: JsonElement?): TopRows {
        val results = (data as? JsonObject)?.get("results") as? JsonObject
        fun bucket(name: String): JsonElement? = results?.get(name)
        return TopRows(
            users = users(json, bucket("users")),
            posts = posts(json, bucket("posts")),
            hashtags = hashtags(bucket("hashtags")),
        )
    }
}
