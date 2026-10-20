package com.us.android.core.profile.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The follower / following lists and the two batch reads that turn their ids
 * into rows. On its own interface, like [GraphCountsApi], so the many
 * hand-written [ProfileApi] fakes do not each grow four methods no other
 * screen calls. Created from the same app-wide Retrofit.
 *
 * The routes are graph-service's and profile-service's own, the same the web
 * reads (`useInfiniteFollowers` / `useInfiniteFollowing`, `GRAPH_ROUTES`).
 */
interface FollowListApi {

    /**
     * `GET /v1/graph/followers/:userId`. With `paginate=cursor` (first page)
     * or `cursor=<next_cursor>` (later pages) the data is
     * `{"items": [ids], "next_cursor": "..."}`; an owner who blocked the
     * caller gets a bare `[]` on either path. Read RAW for that reason.
     */
    @GET("v1/graph/followers/{userId}")
    suspend fun followers(
        @Path("userId") userId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
        @Query("paginate") paginate: String?,
    ): ApiEnvelope<JsonElement>

    /** `GET /v1/graph/following/:userId`, the same shapes as [followers]. */
    @GET("v1/graph/following/{userId}")
    suspend fun following(
        @Path("userId") userId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
        @Query("paginate") paginate: String?,
    ): ApiEnvelope<JsonElement>

    /**
     * `POST /v1/profiles/batch` — answers a BARE `{"<id>": {profile}}` map,
     * no envelope. Blocked and hidden accounts are left out silently.
     */
    @POST("v1/profiles/batch")
    suspend fun profiles(@Body body: ProfileBatchRequest): Map<String, ProfileSummaryDto?>

    /**
     * `POST /v1/graph/relationships/batch` — a BARE `{"<id>": {...}}` map, no
     * envelope. On this route `follows` is the viewer following the target
     * and `follow_request_status` is `pending_sent` while the viewer's
     * request to a private account waits. At most 100 targets.
     */
    @POST("v1/graph/relationships/batch")
    suspend fun relationships(@Body body: RelationshipBatchRequest): Map<String, BatchRelationshipDto?>
}

@Serializable
data class ProfileBatchRequest(
    @SerialName("user_ids") val userIds: List<String>,
)

@Serializable
data class RelationshipBatchRequest(
    @SerialName("viewer_id") val viewerId: String,
    @SerialName("target_ids") val targetIds: List<String>,
)

/** The few public-profile fields a list row draws. Every field defaulted, like every response DTO here. */
@Serializable
data class ProfileSummaryDto(
    @SerialName("user_id") val userId: String? = null,
    val username: String? = null,
    @SerialName("display_name") val displayName: String? = null,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    @SerialName("is_verified") val isVerified: Boolean = false,
)

@Serializable
data class BatchRelationshipDto(
    val follows: Boolean = false,
    @SerialName("follow_request_status") val followRequestStatus: String? = null,
)

/** Which list of an account: the people who follow it, or the people it follows. */
enum class FollowListKind { FOLLOWERS, FOLLOWING }

/**
 * One person in a follower / following list.
 *
 * [viewerEdge] is the SIGNED-IN viewer's edge toward this person — what the
 * row's pill shows — and null when it is not known (no session, or the
 * relationship read failed): a row then offers nothing rather than guessing.
 */
data class FollowListPerson(
    val userId: String,
    val username: String,
    val displayName: String,
    val avatarMediaId: String?,
    val isVerified: Boolean,
    val viewerEdge: FollowStatus?,
)

/**
 * A page of a follower / following list, hydrated into rows.
 *
 * The graph answers ids only. Each page then costs two more requests, sent
 * together: the profiles batch for names and pictures, and the relationships
 * batch for the viewer's own edge toward each person. A page of ids is at
 * most [PAGE_SIZE], well under both batch caps (100).
 */
@Singleton
class FollowListRepository @Inject constructor(
    private val api: FollowListApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * One page of [kind] for [userId]. [cursor] null is the first page.
     * [viewerId] blank (signed out) skips the relationship read.
     *
     * A failed profile read fails the page — a list of nameless rows is not
     * a list. A failed relationship read does not: the rows arrive with no
     * edge and draw no pill. A person the profiles batch left out (blocked,
     * deactivated) is left out of the page too, as the server intends.
     */
    suspend fun page(
        kind: FollowListKind,
        userId: String,
        viewerId: String,
        cursor: String? = null,
        limit: Int = PAGE_SIZE,
    ): AppResult<Paged<FollowListPerson>> {
        val ids = when (val idsPage = idsPage(kind, userId, cursor, limit)) {
            is AppResult.Failure -> return idsPage
            is AppResult.Success -> idsPage.data
        }
        if (ids.items.isEmpty()) return AppResult.Success(Paged(emptyList(), ids.nextCursor))
        return coroutineScope {
            val profiles = async { bare { api.profiles(ProfileBatchRequest(ids.items)) } }
            val targets = ids.items.filter { it != viewerId }
            val edges = async {
                if (viewerId.isBlank() || targets.isEmpty()) {
                    null
                } else {
                    bare { api.relationships(RelationshipBatchRequest(viewerId, targets)) }
                }
            }
            when (val found = profiles.await()) {
                is AppResult.Failure -> found
                is AppResult.Success -> {
                    val relationships = (edges.await() as? AppResult.Success)?.data
                    AppResult.Success(
                        Paged(
                            items = ids.items.mapNotNull { id ->
                                found.data[id]?.toPerson(id, relationships?.edgeFor(id))
                            },
                            nextCursor = ids.nextCursor,
                        ),
                    )
                }
            }
        }
    }

    private suspend fun idsPage(
        kind: FollowListKind,
        userId: String,
        cursor: String?,
        limit: Int,
    ): AppResult<Paged<String>> {
        // The first page asks for cursor mode explicitly; later pages carry
        // the cursor, which implies it. Without either the route falls back
        // to offset paging, whose answer has no next cursor at all.
        val paginate = if (cursor == null) CURSOR_MODE else null
        val result = envelope {
            when (kind) {
                FollowListKind.FOLLOWERS -> api.followers(userId, limit, cursor, paginate)
                FollowListKind.FOLLOWING -> api.following(userId, limit, cursor, paginate)
            }
        }
        return when (result) {
            is AppResult.Failure -> result
            is AppResult.Success -> AppResult.Success(idsFromGraphList(result.data))
        }
    }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun <T> bare(block: suspend () -> T): AppResult<T> = try {
        AppResult.Success(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    /** An envelope whose `data` may legitimately be `null` (an empty Go slice). */
    @Suppress("TooGenericExceptionCaught")
    private suspend fun envelope(block: suspend () -> ApiEnvelope<JsonElement>): AppResult<JsonElement?> = try {
        val body = block()
        val error = body.error
        if (error != null) {
            AppResult.Failure(
                AppError.Unknown(
                    code = error.code,
                    statusCode = null,
                    requestId = body.meta?.requestId,
                ),
            )
        } else {
            AppResult.Success(body.data)
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    companion object {
        const val PAGE_SIZE = 20
        private const val CURSOR_MODE = "cursor"
    }
}

/**
 * The ids out of a follower / following response: `{items, next_cursor}` on
 * the cursor path, a bare array on the offset path and for a caller the owner
 * blocked, `null` for an empty Go slice. A blank cursor is the last page.
 * Internal so every shape is unit-tested.
 */
internal fun idsFromGraphList(data: JsonElement?): Paged<String> = when (data) {
    is JsonArray -> Paged(data.ids(), nextCursor = null)
    is JsonObject -> Paged(
        items = (data["items"] as? JsonArray)?.ids().orEmpty(),
        nextCursor = (data["next_cursor"] as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotBlank() },
    )
    else -> Paged(emptyList(), nextCursor = null)
}

private fun JsonArray.ids(): List<String> =
    mapNotNull { (it as? JsonPrimitive)?.contentOrNull?.takeIf { id -> id.isNotBlank() } }.distinct()

/** The viewer's edge from a batch row; absent from a successful batch reads as no edge. */
private fun Map<String, BatchRelationshipDto?>.edgeFor(id: String): FollowStatus {
    val row = this[id] ?: return FollowStatus.NONE
    return if (row.follows) FollowStatus.FOLLOWING else FollowStatus.fromWire(row.followRequestStatus)
}

private fun ProfileSummaryDto.toPerson(id: String, edge: FollowStatus?) = FollowListPerson(
    userId = userId?.takeIf { it.isNotBlank() } ?: id,
    username = username.orEmpty(),
    displayName = displayName.orEmpty(),
    avatarMediaId = avatarMediaId?.takeIf { it.isNotBlank() },
    isVerified = isVerified,
    viewerEdge = edge,
)
