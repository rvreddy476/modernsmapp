package com.us.android.core.profile.data

import com.us.android.core.common.result.getOrNull
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * `GET v1/users/by-username/{username}` — user-service's handle lookup (the
 * gateway sends `/v1/users` there). The app's one use (2026-10-22) is a
 * tapped `cleestudio.com/u/<username>` link: the profile screen takes a
 * user id, so the handle is resolved to one first. Only the id is read;
 * the rest of the projection is the profile screen's to fetch.
 */
interface UsernameLookupApi {

    @GET("v1/users/by-username/{username}")
    suspend fun byUsername(@Path("username") username: String): ApiEnvelope<UsernameLookupDto>
}

/** The projection's id, under whichever key the service writes it. */
@Serializable
data class UsernameLookupDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
) {
    val key: String get() = id.ifBlank { userId }
}

@Singleton
class UsernameLookupRepository @Inject constructor(
    private val api: UsernameLookupApi,
    private val errorMapper: ErrorMapper,
) {

    /** The user id behind [username] (a leading `@` dropped), or null when there is none or the read failed. */
    suspend fun userIdOf(username: String): String? {
        val handle = username.trim().removePrefix("@")
        if (handle.isEmpty()) return null
        return apiCall(errorMapper) { api.byUsername(handle) }.getOrNull()?.key?.takeIf { it.isNotBlank() }
    }
}
