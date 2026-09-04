package com.us.android.feature.post.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Query

/**
 * search-service's people search, for the reel form's "Tag people" row.
 *
 * `GET /v1/search/users?q=&limit=` through the gateway, answering
 * `{"data":{"items":[{user_id, username, display_name, …}]}}`. Read-only,
 * so Retrofit's GET retry policy applies as it does everywhere else.
 */
interface PeopleSearchApi {

    @GET("v1/search/users")
    suspend fun searchUsers(
        @Query("q") query: String,
        @Query("limit") limit: Int,
    ): ApiEnvelope<UserSearchPageDto>
}

@Serializable
data class UserSearchPageDto(
    val items: List<UserSearchHitDto> = emptyList(),
)

@Serializable
data class UserSearchHitDto(
    @SerialName("user_id") val userId: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("is_verified") val isVerified: Boolean = false,
)
