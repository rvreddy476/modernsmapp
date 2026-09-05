package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.network.pagedApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** A community's updates over [CommunityUpdatesApi], in [AppResult]. */
@Singleton
class CommunityUpdatesRepository @Inject constructor(
    private val api: CommunityUpdatesApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun post(
        id: String,
        body: String,
        mediaIds: List<String>,
        title: String?,
        event: CommunityEvent?,
    ): AppResult<CommunityUpdate> = apiCall(errorMapper, { it.toDomain() }) {
        api.postUpdate(
            id,
            PostUpdateRequest(
                body = body,
                mediaIds = mediaIds,
                title = title?.takeIf { it.isNotBlank() },
                event = event?.let { CommunityEventDto(it.title, it.startsAt, it.endsAt, it.location) },
            ),
        )
    }

    suspend fun page(id: String, cursor: String? = null, limit: Int = PAGE): AppResult<Paged<CommunityUpdate>> =
        pagedApiCall(errorMapper) { api.updates(id, limit, cursor) }.mapPage { it.toDomain() }

    suspend fun react(id: String, updateId: String, emoji: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.react(id, updateId, ReactRequest(emoji)) }

    suspend fun unreact(id: String, updateId: String, emoji: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.unreact(id, updateId, ReactRequest(emoji)) }

    suspend fun view(id: String, updateId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.view(id, updateId) }

    suspend fun report(id: String, updateId: String, reason: String, details: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.reportUpdate(id, updateId, ReportRequest(reason, details)) }

    companion object {
        const val PAGE = 20
    }
}

internal fun CommunityUpdateDto.toDomain() = CommunityUpdate(
    id = id,
    channelId = channelId,
    authorId = authorId,
    updateType = updateType,
    title = title?.takeIf { it.isNotBlank() },
    body = body,
    mediaIds = mediaIds,
    event = event?.let { CommunityEvent(it.title, it.startsAt, it.endsAt, it.location) },
    isPinned = isPinned,
    publishedAt = publishedAt.ifBlank { createdAt },
    viewCount = viewCount,
    reactionCount = reactionCount,
    reactions = reactions.map { ReactionCount(it.emoji, it.count) },
    viewerReaction = viewerReaction?.takeIf { it.isNotBlank() },
    canEdit = canEdit,
)
