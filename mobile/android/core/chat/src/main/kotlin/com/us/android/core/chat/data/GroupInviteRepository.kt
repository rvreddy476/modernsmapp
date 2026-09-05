package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/** A group's invite link as the server holds it. */
data class InviteLink(
    val code: String,
    val url: String,
    val conversationId: String,
    val expiresAt: String?,
    val maxUses: Int,
    val uses: Int,
)

/** What a code resolves to before the viewer decides to join. */
data class InvitePreview(
    val code: String,
    val conversationId: String,
    val title: String,
    val description: String,
    val avatarUrl: String?,
    val memberCount: Int,
    val expiresAt: String?,
    val isLive: Boolean,
    val isMember: Boolean,
)

/**
 * The one word the link section and the join sheet need: can this link
 * still be used, and if not, why. Pure and top-level so the table is pinned
 * by a unit test rather than re-derived in two screens.
 */
enum class InviteLinkState { Live, Expired, Exhausted, Member, NotLive }

/** How the server answered a join or a preview, by contract code. */
enum class InviteFailure { NotFound, NotLive, NotAllowed, GroupFull, Other }

/**
 * Classifies an owner's link: expiry first (a dead link is dead however
 * many uses remain), then the use cap, where 0 means unlimited.
 */
fun inviteLinkState(link: InviteLink, now: Instant = Instant.now()): InviteLinkState {
    val expiry = link.expiresAt?.let { runCatching { Instant.parse(it) }.getOrNull() }
    return when {
        expiry != null && !expiry.isAfter(now) -> InviteLinkState.Expired
        link.maxUses > 0 && link.uses >= link.maxUses -> InviteLinkState.Exhausted
        else -> InviteLinkState.Live
    }
}

/**
 * Classifies a preview for the viewer: membership wins (there is nothing to
 * join), then the server's liveness verdict, then the expiry the client can
 * read itself.
 */
fun invitePreviewState(preview: InvitePreview, now: Instant = Instant.now()): InviteLinkState {
    val expiry = preview.expiresAt?.let { runCatching { Instant.parse(it) }.getOrNull() }
    return when {
        preview.isMember -> InviteLinkState.Member
        !preview.isLive -> InviteLinkState.NotLive
        expiry != null && !expiry.isAfter(now) -> InviteLinkState.Expired
        else -> InviteLinkState.Live
    }
}

/**
 * The code inside an invite URL, or the raw code when the text is one
 * already. Accepts `https://atpost.app/chat/join/CODE`, the same with a
 * trailing slash or query, and a bare code; anything else is null.
 */
fun inviteCodeFrom(text: String): String? {
    val trimmed = text.trim()
    if (trimmed.isBlank()) return null
    val marker = "/chat/join/"
    val index = trimmed.indexOf(marker)
    val candidate = if (index >= 0) {
        trimmed.substring(index + marker.length).substringBefore('?').substringBefore('#').trimEnd('/')
    } else {
        trimmed
    }
    return candidate.takeIf { it.isNotBlank() && it.all { c -> c.isLetterOrDigit() || c == '-' || c == '_' } }
}

/** Maps a failed invite call to the contract's own vocabulary. */
fun AppError.toInviteFailure(): InviteFailure = when (this) {
    is AppError.NotFound -> InviteFailure.NotFound
    is AppError.Forbidden -> if (code == JOIN_NOT_ALLOWED) InviteFailure.NotAllowed else InviteFailure.Other
    is AppError.Unknown -> when {
        code == INVITE_NOT_LIVE || statusCode == HTTP_GONE -> InviteFailure.NotLive
        code == GROUP_FULL || statusCode == HTTP_CONFLICT -> InviteFailure.GroupFull
        code == INVITE_NOT_FOUND -> InviteFailure.NotFound
        else -> InviteFailure.Other
    }
    else -> InviteFailure.Other
}

private const val INVITE_NOT_FOUND = "INVITE_NOT_FOUND"
private const val INVITE_NOT_LIVE = "INVITE_NOT_LIVE"
private const val JOIN_NOT_ALLOWED = "JOIN_NOT_ALLOWED"
private const val GROUP_FULL = "GROUP_FULL"
private const val HTTP_GONE = 410
private const val HTTP_CONFLICT = 409

/** Invite links over [GroupInviteApi], in [AppResult]. */
@Singleton
class GroupInviteRepository @Inject constructor(
    private val api: GroupInviteApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun create(conversationId: String): AppResult<InviteLink> =
        apiCall(errorMapper, { it.toDomain() }) { api.createInviteLink(conversationId) }

    /** The live link, or a NotFound failure when the group has none. */
    suspend fun current(conversationId: String): AppResult<InviteLink> =
        apiCall(errorMapper, { it.toDomain() }) { api.inviteLink(conversationId) }

    suspend fun revoke(conversationId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.revokeInviteLink(conversationId) }

    suspend fun preview(code: String): AppResult<InvitePreview> =
        apiCall(errorMapper, { it.toDomain() }) { api.invitePreview(code) }

    suspend fun join(code: String): AppResult<Conversation> =
        apiCall(errorMapper, { it.toDomain() }) { api.joinByCode(code) }
}

private fun InviteLinkDto.toDomain() = InviteLink(
    code = code,
    url = url,
    conversationId = conversationId,
    expiresAt = expiresAt,
    maxUses = maxUses,
    uses = uses,
)

private fun InvitePreviewDto.toDomain() = InvitePreview(
    code = code,
    conversationId = conversationId,
    title = title,
    description = description,
    avatarUrl = avatarUrl,
    memberCount = memberCount,
    expiresAt = expiresAt,
    isLive = isLive,
    isMember = isMember,
)

/** The ConversationDto → domain mapping, shared by every chat repository in this package. */
internal fun ConversationDto.toDomain(): Conversation = Conversation(
    id = id,
    type = type,
    title = title,
    isRequest = isRequest,
    members = members.map { ConversationMember(it.userId, it.role, it.displayName, it.avatarMediaId) },
    updatedAt = updatedAt,
    avatarMediaId = avatarMediaId,
    lastMessageAt = lastMessageAt,
    lastMessagePreview = lastMessagePreview,
    lastMessageSender = lastMessageSender,
    hasUnread = hasUnread,
    isPinned = isPinned,
    isMuted = isMuted,
    description = description,
    avatarUrl = avatarUrl,
)
