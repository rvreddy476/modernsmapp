package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.decodeFromJsonElement
import retrofit2.http.GET
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * `GET v1/search/users?q=` — the people picker behind "Add members" and
 * "Add admin". The page arrives either as a bare array or wrapped in
 * `{"items": […]}` (both shapes have been captured), so `data` is read raw
 * and unwrapped here; a row that does not decode is dropped, never fatal.
 */
interface PeopleLookupApi {

    @GET("v1/search/users")
    suspend fun users(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<JsonElement>
}

@Serializable
data class PersonHitDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
) {
    val key: String get() = id.ifBlank { userId }
}

/** One person the picker can offer. */
data class PersonHit(
    val userId: String,
    val username: String,
    val displayName: String,
    val avatarUrl: String?,
) {
    val nameForDisplay: String get() = displayName.ifBlank { username.ifBlank { "Unnamed" } }
}

@Singleton
class PeopleLookupRepository @Inject constructor(
    private val api: PeopleLookupApi,
    private val errorMapper: ErrorMapper,
    private val json: Json,
) {

    suspend fun search(query: String, limit: Int = SEARCH_LIMIT): AppResult<List<PersonHit>> =
        apiCall(errorMapper, { element -> rowsOf(element).mapNotNull(::decodeRow) }) {
            api.users(query, limit)
        }

    private fun rowsOf(data: JsonElement): List<JsonElement> = when (data) {
        is JsonArray -> data.toList()
        is JsonObject -> WRAPPER_KEYS.firstNotNullOfOrNull { key -> data[key] as? JsonArray }?.toList().orEmpty()
        else -> emptyList()
    }

    private fun decodeRow(row: JsonElement): PersonHit? =
        runCatching { json.decodeFromJsonElement<PersonHitDto>(row) }.getOrNull()
            ?.takeIf { it.key.isNotBlank() }
            ?.let { PersonHit(it.key, it.username, it.displayName, it.avatarUrl) }

    private companion object {
        const val SEARCH_LIMIT = 20
        val WRAPPER_KEYS = listOf("items", "results", "users", "hits")
    }
}
