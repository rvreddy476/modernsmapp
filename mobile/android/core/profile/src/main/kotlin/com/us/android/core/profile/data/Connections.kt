package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Connections (2026-10-24, the web's `useConnections`): graph-service's
 * connection-request routes and the mute pair, on their own interface so the
 * many hand-written [ProfileApi] fakes keep their shape. Every route is in
 * graph-service `internal/http/handler.go:116-139`:
 *
 *     POST   /v1/graph/connection-request          {user_id, source?, message?} → {status:"request_sent"}
 *     POST   /v1/graph/connection-request/accept   {user_id: the SENDER}        → {status:"accepted"}
 *     POST   /v1/graph/connection-request/decline  {user_id: the SENDER}        → {status:"declined"}
 *     POST   /v1/graph/connection-request/cancel   {user_id: the RECEIVER}      → {status:"cancelled"}
 *     DELETE /v1/graph/connection                  {user_id}                    → {status:"removed"}
 *     GET    /v1/graph/connection-requests         the viewer's incoming, pending (store.ConnectionRequest[])
 *     GET    /v1/graph/connection-requests/sent    the viewer's outgoing, pending
 *     GET    /v1/graph/connections/:userId         a BARE array of ids (limit/offset)
 *     POST   /v1/graph/mute   {user_id}            → 204
 *     DELETE /v1/graph/mute   {user_id}            → 204
 *
 * There is no friendship id: every write names the OTHER person.
 */
interface ConnectionsApi {

    @POST("v1/graph/connection-request")
    suspend fun send(@Body body: ConnectionRequestBody): ApiEnvelope<GraphStatusDto>

    @POST("v1/graph/connection-request/accept")
    suspend fun accept(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    @POST("v1/graph/connection-request/decline")
    suspend fun decline(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    @POST("v1/graph/connection-request/cancel")
    suspend fun cancel(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    /** A DELETE with a body, as unblock is (`parseAuthAndBody`, handler.go:319). */
    @HTTP(method = "DELETE", path = "v1/graph/connection", hasBody = true)
    suspend fun remove(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    @GET("v1/graph/connection-requests")
    suspend fun incoming(): ApiEnvelope<List<ConnectionRequestDto>>

    @GET("v1/graph/connection-requests/sent")
    suspend fun sent(): ApiEnvelope<List<ConnectionRequestDto>>

    @GET("v1/graph/connections/{userId}")
    suspend fun connections(
        @Path("userId") userId: String,
        @Query("limit") limit: Int,
        @Query("offset") offset: Int,
    ): ApiEnvelope<List<String>>

    /** `POST /v1/graph/mute {user_id}` (handler.go:1007): 204, no body. */
    @POST("v1/graph/mute")
    suspend fun mute(@Body body: GraphUserIdRequest)

    /** `DELETE /v1/graph/mute {user_id}`: 204, no body. */
    @HTTP(method = "DELETE", path = "v1/graph/mute", hasBody = true)
    suspend fun unmute(@Body body: GraphUserIdRequest)
}

/**
 * The send body (`SendConnectionRequest`, handler.go:764-776). [message] is
 * at most 280 characters (MESSAGE_TOO_LONG); null fields are left off the
 * wire by the platform `Json` (`explicitNulls = false`).
 */
@Serializable
data class ConnectionRequestBody(
    @SerialName("user_id") val userId: String,
    val source: String? = null,
    val message: String? = null,
)

/** One `store.ConnectionRequest` (graph-service store/graph.go:113-122). */
@Serializable
data class ConnectionRequestDto(
    @SerialName("sender_id") val senderId: String = "",
    @SerialName("receiver_id") val receiverId: String = "",
    val status: String = "",
    val source: String = "",
    val message: String? = null,
    @SerialName("created_at") val createdAt: String = "",
)

/** Which pending list: requests to the viewer, or the viewer's own. */
enum class ConnectionRequestDirection { RECEIVED, SENT }

/**
 * One pending request as a row draws it: the OTHER person (the sender of a
 * received request, the receiver of a sent one), named when the profiles
 * batch knew them.
 */
data class ConnectionRequestPerson(
    val userId: String,
    val displayName: String,
    val username: String,
    val avatarMediaId: String?,
    val createdAt: String,
    val message: String?,
)

/** One connection, as a list row draws it. */
data class ConnectionPerson(
    val userId: String,
    val displayName: String,
    val username: String,
    val avatarMediaId: String?,
)

/** What the connection screens and controls act through; a seam so their ViewModels test against a fake. */
interface ConnectionsSource {
    suspend fun requests(direction: ConnectionRequestDirection): AppResult<List<ConnectionRequestPerson>>

    /** Who has a request to the viewer still waiting: the inbox offers Accept / Decline only on those rows. No names read. */
    suspend fun pendingSenderIds(): AppResult<Set<String>>
    suspend fun connections(userId: String, offset: Int = 0, limit: Int = CONNECTIONS_PAGE): AppResult<List<ConnectionPerson>>
    suspend fun send(userId: String): AppResult<Unit>
    suspend fun accept(senderId: String): AppResult<Unit>
    suspend fun decline(senderId: String): AppResult<Unit>
    suspend fun cancel(receiverId: String): AppResult<Unit>
    suspend fun remove(userId: String): AppResult<Unit>
    suspend fun mute(userId: String): AppResult<Unit>
    suspend fun unmute(userId: String): AppResult<Unit>

    companion object {
        const val CONNECTIONS_PAGE = 50
    }
}

/**
 * The connection routes plus the profiles batch for the names, the web's
 * recipe (`usePendingFriendRequests`, `useSentFriendRequests`, `useFriends`):
 * the graph answers ids, profile-service the people. A failed profiles batch
 * does not fail a request list — a nameless row still accepts — but a person
 * the batch left out of a CONNECTIONS list (blocked, deactivated) is left out
 * of the list too, as the follower lists do.
 */
@Singleton
class ConnectionsRepository @Inject constructor(
    private val api: ConnectionsApi,
    private val profiles: FollowListApi,
    private val errorMapper: ErrorMapper,
) : ConnectionsSource {

    override suspend fun requests(direction: ConnectionRequestDirection): AppResult<List<ConnectionRequestPerson>> {
        val rows = when (
            val result = listApiCall(errorMapper) {
                if (direction == ConnectionRequestDirection.RECEIVED) api.incoming() else api.sent()
            }
        ) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data
        }
        val ids = rows.map { it.counterpart(direction) }.filter { it.isNotBlank() }.distinct()
        if (ids.isEmpty()) return AppResult.Success(emptyList())
        val names = (bare { profiles.profiles(ProfileBatchRequest(ids)) } as? AppResult.Success)?.data.orEmpty()
        return AppResult.Success(
            rows.filter { it.counterpart(direction).isNotBlank() }
                .distinctBy { it.counterpart(direction) }
                .map { row ->
                    val id = row.counterpart(direction)
                    val p = names[id]
                    ConnectionRequestPerson(
                        userId = id,
                        displayName = p?.displayName?.takeIf { it.isNotBlank() } ?: p?.username.orEmpty(),
                        username = p?.username.orEmpty(),
                        avatarMediaId = p?.avatarMediaId?.takeIf { it.isNotBlank() },
                        createdAt = row.createdAt,
                        message = row.message?.takeIf { it.isNotBlank() },
                    )
                },
        )
    }

    override suspend fun pendingSenderIds(): AppResult<Set<String>> =
        listApiCall(errorMapper) { api.incoming() }
            .map { rows -> rows.mapNotNullTo(mutableSetOf()) { it.senderId.takeIf(String::isNotBlank) } }

    override suspend fun connections(userId: String, offset: Int, limit: Int): AppResult<List<ConnectionPerson>> {
        val ids = when (val result = listApiCall(errorMapper) { api.connections(userId, limit, offset) }) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data.filter { it.isNotBlank() }.distinct()
        }
        if (ids.isEmpty()) return AppResult.Success(emptyList())
        return when (val found = bare { profiles.profiles(ProfileBatchRequest(ids)) }) {
            is AppResult.Failure -> found
            is AppResult.Success -> AppResult.Success(
                ids.mapNotNull { id ->
                    found.data[id]?.let { p ->
                        ConnectionPerson(
                            userId = id,
                            displayName = p.displayName?.takeIf { it.isNotBlank() } ?: p.username.orEmpty(),
                            username = p.username.orEmpty(),
                            avatarMediaId = p.avatarMediaId?.takeIf { it.isNotBlank() },
                        )
                    }
                },
            )
        }
    }

    override suspend fun send(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.send(ConnectionRequestBody(userId = userId, source = SOURCE_PROFILE)) }.map { }

    override suspend fun accept(senderId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.accept(GraphUserIdRequest(senderId)) }.map { }

    override suspend fun decline(senderId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.decline(GraphUserIdRequest(senderId)) }.map { }

    override suspend fun cancel(receiverId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.cancel(GraphUserIdRequest(receiverId)) }.map { }

    override suspend fun remove(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.remove(GraphUserIdRequest(userId)) }.map { }

    override suspend fun mute(userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.mute(GraphUserIdRequest(userId)) }

    override suspend fun unmute(userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.unmute(GraphUserIdRequest(userId)) }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun <T> bare(block: suspend () -> T): AppResult<T> = try {
        AppResult.Success(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    private companion object {
        /** graph-service stores where a request came from; the web sends none, the app says "profile". */
        const val SOURCE_PROFILE = "profile"
    }
}

/** The other person on a request row: the sender of a received request, the receiver of a sent one. */
internal fun ConnectionRequestDto.counterpart(direction: ConnectionRequestDirection): String =
    if (direction == ConnectionRequestDirection.RECEIVED) senderId else receiverId

/**
 * "Allow connection requests" (the web's Social preferences, privacySettings.ts:211-214):
 * off is `no_one`; turning it back on restores the narrower audience the
 * account already had rather than widening it — only `no_one` goes to `everyone`.
 */
fun connectionRequestAudience(allow: Boolean, original: String): String = when {
    !allow -> CONNECTION_REQUESTS_NO_ONE
    original == CONNECTION_REQUESTS_NO_ONE || original.isBlank() -> CONNECTION_REQUESTS_EVERYONE
    else -> original
}

const val CONNECTION_REQUESTS_NO_ONE = "no_one"
const val CONNECTION_REQUESTS_EVERYONE = "everyone"
