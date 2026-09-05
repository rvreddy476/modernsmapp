package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.network.pagedApiCall
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import javax.inject.Inject
import javax.inject.Singleton

/** One community, as the viewer sees it. */
data class Community(
    val id: String,
    val ownerId: String,
    val handle: String,
    val name: String,
    val description: String,
    val avatarMediaId: String?,
    val visibility: String,
    val memberCount: Int,
    val updateCount: Int,
    val isVerified: Boolean,
    val viewerRole: String,
    val viewerMuted: Boolean,
    val canPost: Boolean,
) {
    val isOwner: Boolean get() = viewerRole == ROLE_OWNER
    val isAdmin: Boolean get() = viewerRole == ROLE_ADMIN || isOwner
    val isMember: Boolean get() = viewerRole == ROLE_OWNER || viewerRole == ROLE_ADMIN || viewerRole == ROLE_SUBSCRIBER
    val isBanned: Boolean get() = viewerRole == ROLE_BANNED
    val handleForDisplay: String get() = "@$handle"

    companion object {
        const val ROLE_OWNER = "owner"
        const val ROLE_ADMIN = "admin"
        const val ROLE_SUBSCRIBER = "subscriber"
        const val ROLE_BANNED = "banned"
        const val VISIBILITY_PUBLIC = "public"
        const val VISIBILITY_PRIVATE = "private"
    }
}

data class CommunityEvent(
    val title: String,
    val startsAt: String,
    val endsAt: String,
    val location: String,
)

data class ReactionCount(val emoji: String, val count: Int)

/** One update in a community's feed. */
data class CommunityUpdate(
    val id: String,
    val channelId: String,
    val authorId: String,
    val updateType: String,
    val title: String?,
    val body: String,
    val mediaIds: List<String>,
    val event: CommunityEvent?,
    val isPinned: Boolean,
    val publishedAt: String,
    val viewCount: Int,
    val reactionCount: Int,
    val reactions: List<ReactionCount>,
    val viewerReaction: String?,
    val canEdit: Boolean,
)

/** One of a community's admins. */
data class CommunityAdmin(val userId: String, val displayName: String)

/**
 * The viewer's reaction, applied locally the way the server will: ONE
 * reaction per person, so tapping the emoji already chosen removes it and
 * tapping a different one replaces it — the old count drops, the new one
 * rises. Pure, so the table is pinned by a test rather than by a device.
 */
fun CommunityUpdate.withViewerReaction(emoji: String?): CommunityUpdate {
    val previous = viewerReaction
    if (previous == emoji) return this
    val counts = reactions.associate { it.emoji to it.count }.toMutableMap()
    if (previous != null) {
        val left = (counts[previous] ?: 0) - 1
        if (left > 0) counts[previous] = left else counts.remove(previous)
    }
    if (emoji != null) counts[emoji] = (counts[emoji] ?: 0) + 1
    return copy(
        viewerReaction = emoji,
        reactions = counts.map { (key, value) -> ReactionCount(key, value) },
        reactionCount = counts.values.sum(),
    )
}

/** What a tap on [emoji] means for this update: remove the same one, otherwise set it. */
fun CommunityUpdate.reactionAfterTap(emoji: String): String? = if (viewerReaction == emoji) null else emoji

/** How a community write failed, in the contract's words. */
enum class CommunityFailure { HandleTaken, Validation, NotAllowed, Other }

fun AppError.toCommunityFailure(): CommunityFailure = when (this) {
    is AppError.Forbidden -> CommunityFailure.NotAllowed
    is AppError.InvalidRequest -> CommunityFailure.Validation
    is AppError.Unknown -> when {
        code == HANDLE_TAKEN || statusCode == HTTP_CONFLICT -> CommunityFailure.HandleTaken
        code == VALIDATION_ERROR || statusCode == HTTP_UNPROCESSABLE -> CommunityFailure.Validation
        else -> CommunityFailure.Other
    }
    else -> CommunityFailure.Other
}

private const val HANDLE_TAKEN = "HANDLE_TAKEN"
private const val VALIDATION_ERROR = "VALIDATION_ERROR"
private const val HTTP_CONFLICT = 409
private const val HTTP_UNPROCESSABLE = 422

/** Communities over [CommunityApi], in [AppResult]. */
@Singleton
class CommunityRepository @Inject constructor(
    private val api: CommunityApi,
    private val membership: CommunityMembershipApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun create(
        name: String,
        handle: String,
        description: String,
        visibility: String,
        avatarMediaId: String?,
    ): AppResult<Community> = apiCall(errorMapper, { it.toDomain() }) {
        api.create(CreateCommunityRequest(name, handle, description, visibility, avatarMediaId))
    }

    suspend fun community(id: String): AppResult<Community> =
        apiCall(errorMapper, { it.toDomain() }) { api.community(id) }

    suspend fun update(
        id: String,
        name: String? = null,
        description: String? = null,
        visibility: String? = null,
        avatarMediaId: String? = null,
    ): AppResult<Community> = apiCall(errorMapper, { it.toDomain() }) {
        api.update(id, UpdateCommunityRequest(name, description, visibility, avatarMediaId))
    }

    suspend fun delete(id: String): AppResult<Unit> = noContentApiCall(errorMapper) { api.delete(id) }

    suspend fun mine(): AppResult<List<Community>> =
        listApiCall(errorMapper) { api.mine() }.mapItems { it.toDomain() }

    suspend fun discover(
        query: String?,
        cursor: String? = null,
        limit: Int = DISCOVER_PAGE,
    ): AppResult<Paged<Community>> =
        pagedApiCall(errorMapper) { api.discover(query?.takeIf { it.isNotBlank() }, limit, cursor) }
            .mapPage { it.toDomain() }

    suspend fun join(id: String): AppResult<Unit> = noContentApiCall(errorMapper) { membership.subscribe(id) }

    suspend fun leave(id: String): AppResult<Unit> = noContentApiCall(errorMapper) { membership.unsubscribe(id) }

    suspend fun mute(id: String, mutedUntil: String? = null): AppResult<Unit> =
        noContentApiCall(errorMapper) { membership.mute(id, MuteCommunityRequest(mutedUntil)) }

    suspend fun unmute(id: String): AppResult<Unit> = noContentApiCall(errorMapper) { membership.unmute(id) }

    /**
     * The admin roster. Rows are read leniently — an object with `user_id`
     * (or `id`) and an optional `display_name`, or a bare id string — so the
     * screen works whichever shape the service settles on.
     */
    suspend fun admins(id: String): AppResult<List<CommunityAdmin>> =
        when (val result = apiCall(errorMapper) { membership.admins(id) }) {
            is AppResult.Success -> AppResult.Success(adminsOf(result.data))
            // A `"data": null` roster is an empty roster, as every list route is read.
            is AppResult.Failure ->
                if (result.error is AppError.Malformed) AppResult.Success(emptyList()) else result
        }

    suspend fun addAdmin(id: String, userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { membership.addAdmin(id, CommunityAdminRequest(userId)) }

    suspend fun removeAdmin(id: String, userId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { membership.removeAdmin(id, userId) }

    suspend fun report(id: String, reason: String, details: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.report(id, ReportRequest(reason, details)) }

    private fun adminsOf(element: JsonElement): List<CommunityAdmin> {
        val rows: List<JsonElement> = when (element) {
            is JsonArray -> element
            is JsonObject -> (element["items"] as? JsonArray) ?: (element["admins"] as? JsonArray) ?: emptyList()
            else -> emptyList()
        }
        return rows.mapNotNull { row ->
            when (row) {
                is JsonPrimitive -> row.contentOrNull?.takeIf { it.isNotBlank() }?.let { CommunityAdmin(it, "") }
                is JsonObject -> {
                    val id = row.string("user_id") ?: row.string("id") ?: return@mapNotNull null
                    CommunityAdmin(id, row.string("display_name").orEmpty())
                }
                else -> null
            }
        }
    }

    private fun JsonObject.string(key: String): String? =
        (this[key] as? JsonPrimitive)?.contentOrNull?.takeIf { it.isNotBlank() }

    companion object {
        const val DISCOVER_PAGE = 20
    }
}

private inline fun <T, R> AppResult<List<T>>.mapItems(block: (T) -> R): AppResult<List<R>> = when (this) {
    is AppResult.Success -> AppResult.Success(data.map(block))
    is AppResult.Failure -> this
}

internal inline fun <T, R> AppResult<Paged<T>>.mapPage(block: (T) -> R): AppResult<Paged<R>> = when (this) {
    is AppResult.Success -> AppResult.Success(Paged(data.items.map(block), data.nextCursor))
    is AppResult.Failure -> this
}

internal fun CommunityDto.toDomain() = Community(
    id = id,
    ownerId = ownerId,
    handle = handle,
    name = name,
    description = description,
    avatarMediaId = avatarMediaId,
    visibility = visibility,
    memberCount = memberCount,
    updateCount = updateCount,
    isVerified = isVerified,
    viewerRole = viewerRole,
    viewerMuted = viewerMuted,
    canPost = canPost,
)
