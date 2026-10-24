package com.us.android.core.chat.hidden

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.Authenticator
import okhttp3.Interceptor
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Hidden chats on the chat HTTP client.
 *
 * The chat endpoints are built on a client DERIVED from the app's one
 * authenticated client (`newBuilder()`: same pool, same interceptors, same
 * token refresher — so refresh stays single-flight) with two additions:
 *
 *  1. [HiddenChatHeadersInterceptor]: `X-Chat-Open` on every
 *     `/v1/chat/conversations/:id/…` call about a conversation known to be
 *     hidden, while the open token is held — and on nothing else, so the
 *     token never travels further than it must; `X-Device-Id` on the
 *     hidden-chat routes and beside every `X-Chat-Open` (the server binds
 *     tokens to the session, else to this id).
 *  2. [HiddenAwareAuthenticator]: a 401 that refuses a SECRET (a wrong chat
 *     password, a wrong or reused 2-step code) is an answer, not an expired
 *     bearer. The shared authenticator would refresh the token and replay the
 *     same secret, spending a second of the five tries on one tap.
 */

/** Headers one chat call should carry; empty for every call that is not about a hidden chat. */
fun hiddenHeadersFor(
    encodedPath: String,
    isLocked: (String) -> Boolean,
    openToken: String?,
    deviceId: String?,
): Map<String, String> {
    val headers = LinkedHashMap<String, String>()
    val device = deviceId?.trim()?.takeIf { it.isNotEmpty() }
    if (isHiddenRoute(encodedPath) && device != null) headers[DEVICE_ID_HEADER] = device
    val id = conversationIdOfPath(encodedPath)
    val token = openToken?.trim()?.takeIf { it.isNotEmpty() }
    if (id != null && token != null && isLocked(id)) {
        headers[OPEN_HEADER] = token
        if (device != null) headers[DEVICE_ID_HEADER] = device
    }
    return headers
}

/** 401 codes that refuse a secret the user typed; replaying the request would spend another try. */
val SECRET_REFUSAL_CODES: Set<String> = setOf("WRONG_PASSWORD", "INVALID_OTP", "OTP_REPLAYED")

/** Whether a 401 with this error [code] may be answered by refreshing the bearer and replaying. */
fun refreshAllowedFor(code: String?): Boolean = code?.uppercase() !in SECRET_REFUSAL_CODES

private val peekJson = Json { ignoreUnknownKeys = true; isLenient = true }

/** `error.code` of a response, read without consuming its body. */
internal fun Response.peekErrorCode(): String? = runCatching {
    val raw = peekBody(PEEK_BYTES).string()
    peekJson.parseToJsonElement(raw).jsonObject["error"]?.jsonObject?.get("code")?.jsonPrimitive?.contentOrNull
}.getOrNull()

private const val PEEK_BYTES = 4_096L
private const val HTTP_FORBIDDEN = 403

/** Supplies this install's stable device id (never blank once known). */
fun interface HiddenDeviceId {
    fun get(): String?
}

@Singleton
class HiddenChatHeadersInterceptor @Inject constructor(
    private val tokens: HiddenChatTokens,
    private val deviceId: HiddenDeviceId,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val path = request.url.encodedPath
        val explicitOpen = request.header(OPEN_HEADER)
        // Only a hidden-chat call reads the token or the device id.
        val concerned = isHiddenRoute(path) || conversationIdOfPath(path)?.let(tokens::isLocked) == true
        val extra = if (concerned) hiddenHeadersFor(path, tokens::isLocked, tokens.open(), deviceId.get()) else emptyMap()
        val outgoing = if (extra.isEmpty()) {
            request
        } else {
            request.newBuilder().apply {
                extra.forEach { (name, value) -> if (name != OPEN_HEADER || explicitOpen == null) header(name, value) }
            }.build()
        }
        val response = chain.proceed(outgoing)
        learn(path, outgoing, response)
        return response
    }

    /**
     * What a response teaches: a 403 CHAT_LOCKED names a hidden chat (and, if
     * a token rode the call, that the token is dead); a success under an open
     * token slid it on the server, so slide it here too.
     */
    private fun learn(path: String, request: Request, response: Response) {
        val id = conversationIdOfPath(path) ?: return
        val sentOpen = request.header(OPEN_HEADER) != null
        if (response.code == HTTP_FORBIDDEN && response.peekErrorCode() == "CHAT_LOCKED") {
            tokens.markLocked(listOf(id))
            if (sentOpen) tokens.drop(HiddenDropReason.Server, openOnly = true)
        } else if (response.isSuccessful && sentOpen) {
            tokens.slide(isOpen = true, expiresAtHeader = response.header(OPEN_EXPIRES_HEADER))
        }
    }
}

/** The shared authenticator, minus refresh-and-replay of a refused secret. */
class HiddenAwareAuthenticator(private val delegate: Authenticator) : Authenticator {
    override fun authenticate(route: Route?, response: Response): Request? {
        if (!refreshAllowedFor(response.peekErrorCode())) return null
        return delegate.authenticate(route, response)
    }
}

const val OPEN_EXPIRES_HEADER = "X-Chat-Open-Expires-At"
const val REVEAL_EXPIRES_HEADER = "X-Chat-Reveal-Expires-At"
