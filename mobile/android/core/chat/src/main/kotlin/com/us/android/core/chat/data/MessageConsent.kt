package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.pagedApiCall
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/*
 * DM consent (founder-approved 2026-10-20).
 *
 * Nobody lands in someone's Chats without consent. graph-service decides,
 * for every profile, whether the viewer may message directly, may send a
 * message request, or may not message at all — and says why. This file is
 * the client's half of that: the permission read, the paged requests folder
 * and the rules the composer of an outgoing request enforces before the
 * server has to.
 *
 * Its own interface, not members on [ChatApi] or [ChatParityApi]: tests
 * implement those member by member, and a new abstract route would break
 * every fake for a call it never makes.
 */
interface MessageConsentApi {

    /**
     * `GET /v1/permissions/check?target_user_id=<id>&actions=message`.
     * `{"target_user_id", "decisions": {"message": {...}}, "computed_at", "ttl_seconds"}`.
     */
    @GET("v1/permissions/check")
    suspend fun checkPermission(
        @Query("target_user_id") targetUserId: String,
        @Query("actions") actions: String = ACTION_MESSAGE,
    ): ApiEnvelope<PermissionCheckDto>

    /**
     * One page of the requests folder, newest first:
     * `?direction=incoming|outgoing&filtered=true|false&limit=1..100&cursor=`.
     * `filtered=true` is the Hidden requests sub-list. Responds
     * `{"data":[row…],"meta":{"next_cursor","has_more"}}`; the cursor is
     * opaque and passed back untouched.
     */
    @GET("v1/chat/requests")
    suspend fun requestsPage(
        @Query("direction") direction: String = DIRECTION_INCOMING,
        @Query("filtered") filtered: Boolean = false,
        @Query("limit") limit: Int = REQUEST_PAGE_LIMIT,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<ConversationDto>>

    /** `{"count": n, "filtered_count": m, "capped": bool}`: the Requests badge and the Hidden requests count. */
    @GET("v1/chat/requests/count")
    suspend fun requestsCount(): ApiEnvelope<RequestCountDto>
}

@Serializable
data class PermissionCheckDto(
    @SerialName("target_user_id") val targetUserId: String = "",
    val decisions: Map<String, PermissionDecisionDto> = emptyMap(),
    @SerialName("ttl_seconds") val ttlSeconds: Int? = null,
)

/** `{"allowed": bool, "fallback"?: "message_request", "reason"?: string}`; empty fields are left out. */
@Serializable
data class PermissionDecisionDto(
    val allowed: Boolean = false,
    val fallback: String? = null,
    val reason: String? = null,
)

@Serializable
data class RequestCountDto(
    val count: Int = 0,
    @SerialName("filtered_count") val filteredCount: Int = 0,
    /** The server stopped counting: show the numbers as "999+". */
    val capped: Boolean = false,
)

/** The waiting requests ([count]) and the Hidden ones ([hidden]); [capped] = "999+". */
data class RequestCounts(val count: Int, val hidden: Int, val capped: Boolean)

/** A request count as a badge shows it: "999+" once the server stopped counting. */
fun requestCountLabel(count: Int, capped: Boolean): String = if (capped) "$REQUEST_COUNT_CAP+" else "$count"

const val REQUEST_COUNT_CAP = 999

const val ACTION_MESSAGE = "message"
const val FALLBACK_MESSAGE_REQUEST = "message_request"
const val DIRECTION_INCOMING = "incoming"
const val DIRECTION_OUTGOING = "outgoing"
const val REQUEST_PAGE_LIMIT = 20

// ── The decision ─────────────────────────────────────────────────────────

/** What graph-service said the viewer may do about messaging someone. */
enum class MessageDecision {
    /** A direct chat. */
    Allow,

    /** A message request (the request channel). */
    Request,

    /** Not at all; [MessagePermission.reason] says why. */
    Deny,
}

/** The six deny reasons of the contract (§4). Anything else reads as null. */
object MessageDenyReason {
    const val UNAVAILABLE = "unavailable"
    const val MESSAGES_OFF = "messages_off"
    const val YOU_PAUSED = "you_paused"
    const val NO_ONE = "no_one"
    const val FOLLOWERS_ONLY = "followers_only"
    const val PEOPLE_I_FOLLOW = "people_i_follow"

    val ALL = setOf(UNAVAILABLE, MESSAGES_OFF, YOU_PAUSED, NO_ONE, FOLLOWERS_ONLY, PEOPLE_I_FOLLOW)

    /** A reason this client knows, or null (missing or unknown: the screen explains generically). */
    fun normalise(raw: String?): String? = raw?.trim()?.lowercase()?.takeIf { it in ALL }
}

data class MessagePermission(val decision: MessageDecision, val reason: String? = null)

/** The wire decision as the client reads it: allowed, else the request fallback, else a deny with its reason. */
fun PermissionDecisionDto.toMessagePermission(): MessagePermission = when {
    allowed -> MessagePermission(MessageDecision.Allow)
    fallback.equals(FALLBACK_MESSAGE_REQUEST, ignoreCase = true) -> MessagePermission(MessageDecision.Request)
    else -> MessagePermission(MessageDecision.Deny, MessageDenyReason.normalise(reason))
}

// ── The outgoing request's composer ──────────────────────────────────────

/**
 * What the requester may send before the recipient accepts (contract §5):
 * ONE message, text only, at most 500 characters, no links — no photos,
 * video, voice, GIFs, stickers, post cards or calls. The server enforces all
 * of it; the client says so first, so a refusal is never the first the
 * requester hears of the rule.
 */
object RequestComposerRules {
    const val MAX_CHARS = 500

    // The server's own bare-domain list (message-service containsLink), so
    // the two never disagree about what a link is.
    private val bareDomain = Regex(
        "\\b[a-z0-9][a-z0-9-]*\\.(com|net|org|io|co|in|me|app|dev|xyz|info|link|to|ly|gg|sh)\\b",
        RegexOption.IGNORE_CASE,
    )

    fun containsLink(text: String): Boolean {
        val lower = text.lowercase()
        if ("http://" in lower || "https://" in lower || "www." in lower) return true
        return bareDomain.containsMatchIn(text)
    }

    /** Characters as the server counts them (runes), not UTF-16 units. */
    fun length(text: String): Int = text.codePointCount(0, text.length)

    /** True when the open thread is a request the viewer sent and the other person has not answered. */
    fun isOutgoingPending(conversation: Conversation?, viewerId: String): Boolean {
        if (conversation == null || !conversation.isRequest || viewerId.isBlank()) return false
        if (conversation.type == "group") return false
        conversation.requestDirection?.let { return it == DIRECTION_OUTGOING }
        conversation.sender?.userId?.takeIf { it.isNotBlank() }?.let { return it == viewerId }
        return conversation.createdBy.isNotBlank() && conversation.createdBy == viewerId
    }

    /** Why [draft] cannot go as the request's message; null when it can (or is empty). */
    fun problem(draft: String): RequestDraftProblem? {
        val text = draft.trim()
        return when {
            text.isEmpty() -> null
            length(text) > MAX_CHARS -> RequestDraftProblem.TooLong
            containsLink(text) -> RequestDraftProblem.HasLink
            else -> null
        }
    }
}

enum class RequestDraftProblem { TooLong, HasLink }

// ── Repository ───────────────────────────────────────────────────────────

/** A requests page plus where the next one starts; [next] null = the end. */
data class RequestPage(val items: List<Conversation>, val next: RequestPageKey?)

/** The server's opaque `meta.next_cursor`. */
data class RequestPageKey(val cursor: String)

@Singleton
class MessageConsentRepository @Inject constructor(
    private val api: MessageConsentApi,
    private val errorMapper: ErrorMapper,
) {

    /** The message decision for [targetUserId]; a check without a `message` entry is a Malformed failure. */
    suspend fun messagePermission(targetUserId: String): AppResult<MessagePermission> =
        when (val result = apiCall(errorMapper) { api.checkPermission(targetUserId) }) {
            is AppResult.Success -> result.data.decisions[ACTION_MESSAGE]?.let { AppResult.Success(it.toMessagePermission()) }
                ?: AppResult.Failure(AppError.Malformed("permission check carried no message decision"))
            is AppResult.Failure -> result
        }

    /**
     * One page of the incoming requests ([hidden] = the Hidden requests
     * sub-list), newest first. Rows the viewer SENT are dropped here too:
     * only incoming ones wait for the viewer.
     */
    suspend fun requestsPage(hidden: Boolean, key: RequestPageKey? = null): AppResult<RequestPage> {
        val result = pagedApiCall(errorMapper) {
            api.requestsPage(
                direction = DIRECTION_INCOMING,
                filtered = hidden,
                limit = REQUEST_PAGE_LIMIT,
                cursor = key?.cursor,
            )
        }
        return when (result) {
            is AppResult.Success -> {
                val items = result.data.items
                    .filter { it.direction != DIRECTION_OUTGOING }
                    .map { it.toDomain() }
                    .withoutDatingChats()
                AppResult.Success(RequestPage(items, result.data.nextCursor?.let(::RequestPageKey)))
            }
            is AppResult.Failure -> result
        }
    }

    /**
     * The Requests badge and the Hidden requests count; null when this server
     * has no count route (404, or 501 from a build without it) — the caller
     * then counts the rows it has.
     */
    suspend fun requestCounts(): AppResult<RequestCounts?> =
        when (val result = apiCall(errorMapper) { api.requestsCount() }) {
            is AppResult.Success -> AppResult.Success(
                RequestCounts(
                    count = result.data.count.coerceAtLeast(0),
                    hidden = result.data.filteredCount.coerceAtLeast(0),
                    capped = result.data.capped,
                ),
            )
            is AppResult.Failure ->
                if (result.error.isMissingRoute() || result.error.isNotImplemented()) AppResult.Success(null) else result
        }

    private fun AppError.isNotImplemented(): Boolean = this is AppError.Server && statusCode == HTTP_NOT_IMPLEMENTED

    /** The request the viewer already sent to [userId] and is still waiting on, if any (first page only). */
    suspend fun pendingRequestTo(userId: String): AppResult<Conversation?> {
        val result = pagedApiCall(errorMapper) { api.requestsPage(direction = DIRECTION_OUTGOING) }
        return when (result) {
            is AppResult.Success -> AppResult.Success(
                result.data.items
                    .filter { it.direction == null || it.direction == DIRECTION_OUTGOING }
                    .map { it.toDomain() }
                    .firstOrNull { row -> row.type != "group" && (row.peer?.userId == userId || row.members.any { it.userId == userId }) },
            )
            is AppResult.Failure -> result
        }
    }
}

private const val HTTP_NOT_IMPLEMENTED = 501
