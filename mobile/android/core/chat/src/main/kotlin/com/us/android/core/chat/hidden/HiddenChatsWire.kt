package com.us.android.core.chat.hidden

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.net.URLDecoder

/*
 * Hidden chats wire (contract pinned 24 Oct 2026, server 0d5e09cfb): request
 * bodies, decoders and error mapping. Pure, so each is a JVM unit test; the
 * calls themselves are [HiddenChatsApi].
 *
 *   GET  /v1/chat/hidden/settings              {configured, strict, shortcut}
 *   PUT  /v1/chat/hidden/settings              partial {secret_key?, chat_password?, strict?, current_chat_password?}
 *   POST /v1/chat/hidden/reset                 {secret_key, chat_password, strict?} + fresh re-auth
 *   POST /v1/chat/hidden/reveal  {secret_key}  200 {reveal_token, expires_at} | 404 | 429 LOCKED
 *   GET  /v1/chat/hidden/conversations         X-Chat-Reveal → the normal row shape | 404
 *   POST /v1/chat/hidden/open    {chat_password} + X-Chat-Reveal → {open_token, expires_at} | 401 | 429
 *   POST /v1/chat/hidden/lock                  drops this device's tokens
 *   POST /v1/chat/conversations/:id/hide       409 HIDDEN_CHATS_NOT_CONFIGURED before setup
 *   POST /v1/chat/conversations/:id/unhide     X-Chat-Open
 *   every other /v1/chat/conversations/:id/…   X-Chat-Open on a hidden chat, else 403 CHAT_LOCKED
 *
 * NEVER LOGGED: no secret, token or body from here reaches a log line. The
 * app-wide HTTP log is BASIC (method + URL), the bodies and headers carry the
 * secrets, and the one URL that carries a token ([withChatOpen]) is redacted
 * from that log by name.
 */

const val REVEAL_HEADER = "X-Chat-Reveal"
const val OPEN_HEADER = "X-Chat-Open"
const val DEVICE_ID_HEADER = "X-Device-Id"
const val STEP_UP_TOKEN_HEADER = "X-Step-Up-Token"

/** The media query parameter media-service reads for a hidden chat's attachments. */
const val CHAT_OPEN_QUERY = "chat_open"

const val HIDDEN_ROUTE_PREFIX = "/v1/chat/hidden/"
private const val CONVERSATIONS_PREFIX = "/v1/chat/conversations/"

// ── Bodies ───────────────────────────────────────────────────────────────

/**
 * PUT /hidden/settings: only the fields being set travel (an empty string is
 * "not changing it"); once configured, [currentChatPassword] rides every
 * change. `shortcut` is a web keyboard shortcut and is never sent from here,
 * so the web's choice is kept.
 */
fun settingsBody(
    secretKey: String? = null,
    chatPassword: String? = null,
    currentChatPassword: String? = null,
    strict: Boolean? = null,
): JsonObject = buildJsonObject {
    secretKey?.takeIf { it.isNotEmpty() }?.let { put("secret_key", it) }
    chatPassword?.takeIf { it.isNotEmpty() }?.let { put("chat_password", it) }
    currentChatPassword?.takeIf { it.isNotEmpty() }?.let { put("current_chat_password", it) }
    strict?.let { put("strict", it) }
}

/** POST /hidden/reset after re-auth: both new secrets, and strict mode when it is being set. */
fun resetBody(secretKey: String, chatPassword: String, strict: Boolean? = null): JsonObject = buildJsonObject {
    put("secret_key", secretKey)
    put("chat_password", chatPassword)
    strict?.let { put("strict", it) }
}

/** POST /hidden/reveal: surrounding spaces are not part of a key. */
fun revealBody(secretKey: String): JsonObject = buildJsonObject { put("secret_key", secretKey.trim()) }

/** POST /hidden/open: the password exactly as typed — spaces may be part of it. */
fun openBody(chatPassword: String): JsonObject = buildJsonObject { put("chat_password", chatPassword) }

/** identity POST /v1/auth/step-up: the authenticator (or recovery) code. */
fun stepUpBody(otp: String): JsonObject = buildJsonObject { put("otp", otp.trim()) }

/** The socket frame that subscribes a hidden chat's room: the open token rides in it, no entitlement. */
fun hiddenSubscribeFrame(json: Json, conversationId: String, openToken: String): String =
    json.encodeToString(HiddenSubscribeFrame.serializer(), HiddenSubscribeFrame(conversationId = conversationId, openToken = openToken))

@Serializable
internal data class HiddenSubscribeFrame(
    // @EncodeDefault: the app-wide Json omits defaults (see SendMessageRequest).
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val type: String = "conversation.subscribe",
    @SerialName("conversation_id") val conversationId: String,
    @SerialName("open_token") val openToken: String,
)

// ── Decoders ─────────────────────────────────────────────────────────────

@Serializable
data class HiddenSettingsDto(
    val configured: Boolean = false,
    val strict: Boolean = false,
    val shortcut: String? = null,
)

@Serializable
data class HiddenTokenDto(
    @SerialName("reveal_token") val revealToken: String? = null,
    @SerialName("open_token") val openToken: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class StepUpTokenDto(@SerialName("access_token") val accessToken: String? = null)

/** The account's hidden-chat settings, never a secret. */
data class HiddenSettings(val configured: Boolean, val strict: Boolean)

fun HiddenSettingsDto.toSettings() = HiddenSettings(configured = configured, strict = strict)

/** `expires_at` as epoch millis: RFC 3339, epoch seconds or millis; unknown → now + [fallbackTtlMs]. */
@Suppress("MagicNumber") // 1e12 tells epoch millis from epoch seconds.
fun expiryMillis(expiresAt: String?, nowMs: Long, fallbackTtlMs: Long = HIDDEN_TOKEN_TTL_MS): Long {
    val raw = expiresAt?.trim().orEmpty()
    if (raw.isEmpty()) return nowMs + fallbackTtlMs
    raw.toLongOrNull()?.let { n -> if (n > 0) return if (n < 1_000_000_000_000L) n * 1000 else n }
    return runCatching { java.time.Instant.parse(raw).toEpochMilli() }.getOrNull()
        ?: runCatching { java.time.OffsetDateTime.parse(raw).toInstant().toEpochMilli() }.getOrNull()
        ?: (nowMs + fallbackTtlMs)
}

// ── Paths ────────────────────────────────────────────────────────────────

/**
 * The conversation a chat API path is about (`/v1/chat/conversations/<id>/…`),
 * or null. `direct` and `group` are create routes, not a conversation.
 */
fun conversationIdOfPath(encodedPath: String): String? {
    if (!encodedPath.startsWith(CONVERSATIONS_PREFIX)) return null
    val raw = encodedPath.removePrefix(CONVERSATIONS_PREFIX).substringBefore('/')
    if (raw.isEmpty()) return null
    val id = runCatching { URLDecoder.decode(raw, Charsets.UTF_8.name()) }.getOrNull() ?: return null
    return id.takeUnless { it == "direct" || it == "group" || it.isBlank() }
}

/** The hidden-chat routes themselves (`/v1/chat/hidden/…`). */
fun isHiddenRoute(encodedPath: String): Boolean = encodedPath.startsWith(HIDDEN_ROUTE_PREFIX)

/**
 * The one place a hidden chat's media URL gets its open token (media-service
 * reads `chat_open`). If the server moves to signed URLs instead, this is the
 * only function to change. The token rides a URL, so the app's HTTP log
 * redacts this parameter by name ([CHAT_OPEN_QUERY]); nothing else may log it.
 */
fun withChatOpen(url: String, openToken: String?): String {
    val token = openToken?.trim().orEmpty()
    if (token.isEmpty()) return url
    val parsed = url.toHttpUrlOrNull() ?: return url
    return parsed.newBuilder().setQueryParameter(CHAT_OPEN_QUERY, token).build().toString()
}

// ── Errors ───────────────────────────────────────────────────────────────

/** What a hidden-chat call's refusal means. */
sealed interface HiddenError {
    /** A wrong key, "not configured", or a reveal token the server no longer holds — all one answer. */
    data object NotFound : HiddenError
    data class WrongPassword(val attemptsLeft: Int?) : HiddenError
    data class Locked(val retryAfterSec: Long?) : HiddenError
    data object ChatLocked : HiddenError
    data object SecretsMustDiffer : HiddenError
    data object StepUpRequired : HiddenError
    data object InvalidSecretKey : HiddenError
    data object InvalidChatPassword : HiddenError
    data class Invalid(val message: String?) : HiddenError
    data object NotConfigured : HiddenError
    data object Unavailable : HiddenError
    data object Network : HiddenError
    data class Unknown(val status: Int, val code: String?) : HiddenError
}

private val errorJson = Json { ignoreUnknownKeys = true; isLenient = true }

private fun JsonElement?.obj(): JsonObject? = runCatching { this?.jsonObject }.getOrNull()

private fun JsonElement?.text(): String? = (this as? JsonPrimitive)?.contentOrNull?.trim()?.takeIf { it.isNotEmpty() }

/** A field from wherever the server put it: `error.details`, `error`, then the top level. */
private fun errField(root: JsonObject?, vararg names: String): JsonElement? {
    val error = root?.get("error").obj()
    val details = error?.get("details").obj()
    for (source in listOf(details, error, root)) {
        source ?: continue
        for (name in names) source[name]?.takeUnless { it is kotlinx.serialization.json.JsonNull }?.let { return it }
    }
    return null
}

/** `error.code` of a raw error body, upper-cased; null when there is none. */
fun errorCodeOf(body: String?): String? =
    body?.let { runCatching { errorJson.parseToJsonElement(it).jsonObject }.getOrNull() }
        ?.get("error").obj()?.get("code").text()?.uppercase()

/** Seconds until a lockout ends: `retry_after` (seconds or a timestamp), else the Retry-After header. */
@Suppress("MagicNumber") // Epoch-second and epoch-millisecond thresholds.
fun retryAfterSeconds(root: JsonObject?, header: String?, nowMs: Long): Long? {
    val raw = errField(root, "retry_after", "retry_after_seconds", "locked_until").text() ?: header?.trim()
    raw ?: return null
    raw.toDoubleOrNull()?.let { n ->
        val seconds = when {
            n > 1e12 -> (n - nowMs) / 1000.0
            n > 1e9 -> n - nowMs / 1000.0
            else -> n
        }
        return kotlin.math.ceil(seconds).toLong().coerceAtLeast(0)
    }
    return runCatching { java.time.Instant.parse(raw).toEpochMilli() }.getOrNull()
        ?.let { ((it - nowMs + 999) / 1000).coerceAtLeast(0) }
}

/**
 * Maps one refusal: [status] (0 = no response at all), the raw error body,
 * and the Retry-After header. Keyed on `error.code`, the status only when the
 * body names none.
 */
@Suppress("CyclomaticComplexMethod", "MagicNumber") // One branch per contract code.
fun hiddenErrorOf(status: Int, body: String?, retryAfterHeader: String? = null, nowMs: Long = System.currentTimeMillis()): HiddenError {
    if (status == 0) return HiddenError.Network
    val root = body?.let { runCatching { errorJson.parseToJsonElement(it).jsonObject }.getOrNull() }
    val code = root?.get("error").obj()?.get("code").text()?.uppercase().orEmpty()
    val message = root?.get("error").obj()?.get("message").text()
    return when {
        code == "CHAT_LOCKED" -> HiddenError.ChatLocked
        code == "LOCKED" || status == 429 -> HiddenError.Locked(retryAfterSeconds(root, retryAfterHeader, nowMs))
        code == "WRONG_PASSWORD" -> HiddenError.WrongPassword(
            errField(root, "attempts_left", "attempts_remaining").text()?.toDoubleOrNull()?.toInt()?.coerceAtLeast(0),
        )
        code.contains("STEP_UP") || code == "REAUTH_REQUIRED" -> HiddenError.StepUpRequired
        code == "SECRETS_MUST_DIFFER" -> HiddenError.SecretsMustDiffer
        code == "INVALID_SECRET_KEY" -> HiddenError.InvalidSecretKey
        code == "INVALID_CHAT_PASSWORD" -> HiddenError.InvalidChatPassword
        code == "HIDDEN_CHATS_NOT_CONFIGURED" -> HiddenError.NotConfigured
        code == "HIDDEN_CHATS_UNAVAILABLE" || code == "UNAVAILABLE" -> HiddenError.Unavailable
        code == "NOT_FOUND" || status == 404 -> HiddenError.NotFound
        status == 400 || status == 422 -> HiddenError.Invalid(message)
        status == 405 || status == 501 || status == 502 || status == 503 -> HiddenError.Unavailable
        else -> HiddenError.Unknown(status, code.ifEmpty { null })
    }
}

/** The server's sliding window, used when a response names no expiry. */
const val HIDDEN_TOKEN_TTL_MS = 15 * 60_000L

/** The client re-locks sooner: one minute without activity. */
const val HIDDEN_IDLE_MS = 60_000L
