package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Settings › Privacy › Blocked accounts (web parity, 2026-10-22): the three
 * graph-service routes the web's `/settings/blocked` reads, on their own
 * interface so the many hand-written [ProfileApi] fakes keep their shape.
 * Unblock itself is [ProfileApi.unblock], the call the profile already makes.
 *
 * `GET /v1/graph/blocked-and-muted` is now VIEWER-BOUND (security sweep,
 * 2026-10-21: `user_id` naming anyone else is `400 VIEWER_BOUND`) and inside
 * the standard envelope — the privacy hole the earlier note on [ProfileApi]
 * described is closed, which is why it can be read at all.
 */
interface BlockedAccountsApi {

    /**
     * Everyone whose content is withheld from the viewer: blocks in BOTH
     * directions plus the viewer's own mutes, as one list of ids
     * (`{"data":{"user_ids":[…]}}`). There is no blocks-only list; the
     * relationship batch says which of them the VIEWER blocked or muted.
     */
    @GET("v1/graph/blocked-and-muted")
    suspend fun blockedAndMuted(): ApiEnvelope<BlockedAndMutedDto>

    /** `POST /v1/graph/relationships/batch`: a BARE `{"<id>": {...}}` map, at most 100 targets. */
    @POST("v1/graph/relationships/batch")
    suspend fun relationships(@Body body: RelationshipBatchRequest): Map<String, BlockRelationshipDto?>

    /** `DELETE /v1/graph/mute {user_id}`: 204, no body. A DELETE with a body, as unblock is. */
    @HTTP(method = "DELETE", path = "v1/graph/mute", hasBody = true)
    suspend fun unmute(@Body body: GraphUserIdRequest)
}

@Serializable
data class BlockedAndMutedDto(@SerialName("user_ids") val userIds: List<String> = emptyList())

/**
 * The batch row's block and mute edges. `blocked` (legacy) and
 * `viewer_blocked_target` both mean "the viewer blocked the target" on THIS
 * route; `blocked_by` / `target_blocked_viewer` the other way, which the
 * viewer cannot undo and is never shown.
 */
@Serializable
data class BlockRelationshipDto(
    val blocked: Boolean = false,
    @SerialName("viewer_blocked_target") val viewerBlockedTarget: Boolean? = null,
    @SerialName("is_muted") val isMuted: Boolean = false,
)

/** One account on the page: who, and what the viewer may undo. */
data class RestrictedAccount(
    val userId: String,
    val displayName: String,
    val username: String,
    val avatarMediaId: String?,
)

/** The page's two lists: the accounts the viewer blocked, and those they only muted. */
data class RestrictedAccounts(
    val blocked: List<RestrictedAccount>,
    val muted: List<RestrictedAccount>,
)

/** What the Blocked accounts page reads and writes; a seam so its ViewModel is tested against a fake. */
interface BlockedAccountsSource {
    suspend fun load(viewerId: String): AppResult<RestrictedAccounts>
    suspend fun unblock(userId: String): AppResult<Unit>
    suspend fun unmute(userId: String): AppResult<Unit>
}

/**
 * The web's recipe (`useBlockedUsers`): the withheld ids, then ONE
 * relationships batch to pick out the viewer's own blocks (and, here, their
 * mutes), then the profiles batch for names. A block made BY the other
 * account is never listed: the viewer cannot undo it, and saying it exists
 * would tell them who blocked them.
 *
 * The relationships batch is not optional: without it a block cannot be
 * told from a mute or from a block made against the viewer, so its failure
 * fails the page. The profiles batch is: a row without a name still lists,
 * and still unblocks.
 */
@Singleton
class BlockedAccountsRepository @Inject constructor(
    private val api: BlockedAccountsApi,
    private val profiles: FollowListApi,
    private val profileApi: ProfileApi,
    private val errorMapper: ErrorMapper,
) : BlockedAccountsSource {

    override suspend fun load(viewerId: String): AppResult<RestrictedAccounts> {
        val ids = when (val result = apiCall(errorMapper) { api.blockedAndMuted() }) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data.userIds.filter { it.isNotBlank() && it != viewerId }.distinct()
        }
        if (ids.isEmpty()) return AppResult.Success(RestrictedAccounts(emptyList(), emptyList()))
        // graph-service caps the batch at 100; the web reads the first 100 the same way.
        val targets = ids.take(BATCH_MAX)
        val edges = when (val result = bare { api.relationships(RelationshipBatchRequest(viewerId, targets)) }) {
            is AppResult.Failure -> return result
            is AppResult.Success -> result.data
        }
        val blocked = targets.filter { edges[it]?.viewerBlocked() == true }
        val muted = targets.filter { id -> id !in blocked && edges[id]?.isMuted == true }
        val names = (bare { profiles.profiles(ProfileBatchRequest(blocked + muted)) } as? AppResult.Success)?.data.orEmpty()
        return AppResult.Success(
            RestrictedAccounts(
                blocked = blocked.map { it.toAccount(names[it]) },
                muted = muted.map { it.toAccount(names[it]) },
            ),
        )
    }

    override suspend fun unblock(userId: String): AppResult<Unit> =
        apiCall(errorMapper) { profileApi.unblock(GraphUserIdRequest(userId)) }.map { }

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
        const val BATCH_MAX = 100
    }
}

/** The explicit field when the server sent it (a boolean, even false, is authoritative), else the legacy one. */
private fun BlockRelationshipDto.viewerBlocked(): Boolean = viewerBlockedTarget ?: blocked

private fun String.toAccount(profile: ProfileSummaryDto?) = RestrictedAccount(
    userId = this,
    displayName = profile?.displayName?.trim().orEmpty(),
    username = profile?.username?.trim().orEmpty(),
    avatarMediaId = profile?.avatarMediaId?.takeIf { it.isNotBlank() },
)
