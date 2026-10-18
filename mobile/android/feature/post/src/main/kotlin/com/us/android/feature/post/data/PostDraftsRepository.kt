package com.us.android.feature.post.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.creator.model.PostDetails
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.post.data.dto.CONTENT_TYPE_POST
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_IMAGE
import com.us.android.feature.post.data.dto.POST_TYPE_TEXT
import com.us.android.feature.post.data.dto.REMIX_ALLOW
import com.us.android.feature.post.data.dto.REMIX_DISALLOW
import com.us.android.feature.post.studio.studioCreateRequest
import javax.inject.Inject
import javax.inject.Singleton

/** A draft as the Drafts list and the draft editor read it. */
data class PostDraft(
    val id: String,
    val text: String,
    val language: String,
    val mediaIds: List<String>,
    val altTexts: Map<String, String>,
    val details: PostDetails,
    val status: String,
    val updatedAt: String,
)

/**
 * The composer drafts on the server (2026-10-18): what the details screen's
 * Drafts button saves, the Drafts list reads and the draft editor changes
 * and publishes. Photo and text drafts only — the reel form keeps its own.
 */
@Singleton
class PostDraftsRepository @Inject constructor(
    private val api: PostDraftsApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun create(draft: DraftContent): AppResult<PostDraft> =
        apiCall(errorMapper) { api.create(PostDraftRequestDto(payload = draftPayload(draft))) }.map { it.toDraft() }

    suspend fun update(draftId: String, draft: DraftContent): AppResult<PostDraft> =
        apiCall(errorMapper) { api.update(draftId, PostDraftRequestDto(payload = draftPayload(draft))) }
            .map { it.toDraft() }

    /** The author's drafts still waiting for them, newest first as the server orders them. */
    suspend fun list(): AppResult<List<PostDraft>> =
        apiCall(errorMapper) { api.list(LIST_LIMIT) }.map { rows ->
            rows.filter { it.postType == DRAFT_KIND_POST && it.status == DRAFT_STATUS_DRAFT }.map { it.toDraft() }
        }

    suspend fun get(draftId: String): AppResult<PostDraft> =
        apiCall(errorMapper) { api.get(draftId) }.map { it.toDraft() }

    suspend fun delete(draftId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.delete(draftId) }.map { }

    /** Publishes now; the post's id. */
    suspend fun publish(draftId: String): AppResult<String> =
        apiCall(errorMapper) { api.publish(draftId) }.map { it.id }

    private companion object {
        const val LIST_LIMIT = 50
    }
}

/** Everything a draft holds, as the screens hand it over. */
data class DraftContent(
    val text: String,
    val language: String,
    val mediaIds: List<String>,
    val altTexts: Map<String, String> = emptyMap(),
    val details: PostDetails = PostDetails(),
)

/**
 * A draft's payload: the SAME fields, under the same rules, as the create
 * request the post would be published with ([studioCreateRequest]) — so a
 * draft published from the server is the post Post would have made.
 */
internal fun draftPayload(draft: DraftContent): PostDraftPayloadDto {
    val postType = if (draft.mediaIds.isEmpty()) POST_TYPE_TEXT else POST_TYPE_IMAGE
    val request = studioCreateRequest(draft.text, draft.language, postType, draft.mediaIds, draft.details)
    return PostDraftPayloadDto(
        text = request.text,
        visibility = request.visibility,
        contentType = CONTENT_TYPE_POST,
        mediaIds = request.mediaIds.takeIf { it.isNotEmpty() },
        altTexts = draft.altTexts.filterKeys { it in draft.mediaIds }.filterValues { it.isNotBlank() }.takeIf { it.isNotEmpty() },
        noComments = request.noComments,
        locationName = request.locationName,
        language = request.language,
        title = request.title,
        distribution = DistributionRequest(),
        coverMediaId = request.coverMediaId,
        paidPromotion = request.paidPromotion,
        postType = postType,
        hashtags = request.hashtags,
        mentions = request.mentions,
        remixSetting = request.remixSetting,
        commentAccess = request.commentAccess,
        linkUrl = request.linkUrl,
        aiGenerated = request.aiGenerated,
        isBranded = request.isBranded,
        coverIndex = request.coverMediaId?.let { draft.mediaIds.indexOf(it) }?.takeIf { it >= 0 },
        locationPlaceId = request.locationPlaceId,
    )
}

/** A saved draft read back into what the details screen edits. */
internal fun PostDraftDto.toDraft(): PostDraft {
    val p = payload
    val media = p.mediaIds.orEmpty()
    val cover = p.coverIndex ?: p.coverMediaId?.let { media.indexOf(it) }?.takeIf { it >= 0 }
    return PostDraft(
        id = id,
        text = p.text,
        language = p.language?.takeIf { it.isNotBlank() } ?: DEFAULT_LANGUAGE,
        mediaIds = media,
        altTexts = p.altTexts.orEmpty(),
        details = PostDetails(
            title = p.title?.takeIf { it.isNotBlank() },
            hashtags = p.hashtags,
            mentions = p.mentions,
            visibility = p.visibility,
            // comment_access is the newer, enforced field; no_comments the older spelling.
            allowComments = when {
                p.commentAccess != null -> p.commentAccess != COMMENT_ACCESS_OFF
                p.noComments != null -> !p.noComments
                else -> null
            },
            allowReuse = when (p.remixSetting) {
                null, "" -> null
                REMIX_DISALLOW -> false
                REMIX_ALLOW -> true
                else -> true
            },
            paidPromotion = p.paidPromotion,
            isBranded = p.isBranded,
            aiGenerated = p.aiGenerated,
            linkUrl = p.linkUrl?.takeIf { it.isNotBlank() },
            locationName = p.locationName?.takeIf { it.isNotBlank() },
            locationPlaceId = p.locationPlaceId?.takeIf { it.isNotBlank() },
            coverIndex = cover,
        ),
        status = status,
        updatedAt = updatedAt.ifBlank { createdAt },
    )
}

private const val DEFAULT_LANGUAGE = "en"
private const val COMMENT_ACCESS_OFF = "nobody"
