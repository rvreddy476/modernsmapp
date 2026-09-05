package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * Group invite links (contract of 2026-09-05, live on dev).
 *
 * A SEPARATE interface from [ChatApi] on purpose: every test fake that
 * implements [ChatApi] would otherwise have to grow five more stubs, and the
 * link lifecycle is its own small surface — an owner or admin mints one link
 * per group, anyone with the code previews it, a member-to-be joins by it.
 *
 * The revoke answers with no body worth reading, so it is declared `Unit`
 * and routed through `noContentApiCall`; an HTTP error still surfaces.
 */
interface GroupInviteApi {

    /** Mints (or re-issues) the group's link. Owner/admin only. */
    @POST("v1/chat/conversations/{id}/invite-link")
    suspend fun createInviteLink(@Path("id") conversationId: String): ApiEnvelope<InviteLinkDto>

    /** The group's current link; `INVITE_NOT_FOUND` when none is live. */
    @GET("v1/chat/conversations/{id}/invite-link")
    suspend fun inviteLink(@Path("id") conversationId: String): ApiEnvelope<InviteLinkDto>

    @DELETE("v1/chat/conversations/{id}/invite-link")
    suspend fun revokeInviteLink(@Path("id") conversationId: String)

    /** What a code points at, for the join preview. 404 / 410 speak for themselves. */
    @GET("v1/chat/invites/{code}")
    suspend fun invitePreview(@Path("code") code: String): ApiEnvelope<InvitePreviewDto>

    /** Joins by code; answers the conversation, or `JOIN_NOT_ALLOWED` / `GROUP_FULL`. */
    @POST("v1/chat/invites/{code}/join")
    suspend fun joinByCode(@Path("code") code: String): ApiEnvelope<ConversationDto>
}

@Serializable
data class InviteLinkDto(
    val code: String = "",
    val url: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("expires_at") val expiresAt: String? = null,
    /** 0 means unlimited. */
    @SerialName("max_uses") val maxUses: Int = 0,
    val uses: Int = 0,
)

@Serializable
data class InvitePreviewDto(
    val code: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    val title: String = "",
    val description: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("member_count") val memberCount: Int = 0,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("is_live") val isLive: Boolean = true,
    @SerialName("is_member") val isMember: Boolean = false,
)
