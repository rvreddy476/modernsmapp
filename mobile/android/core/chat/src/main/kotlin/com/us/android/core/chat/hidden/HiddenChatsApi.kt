package com.us.android.core.chat.hidden

import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import okhttp3.Headers
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query
import java.io.IOException

/**
 * The hidden-chat routes (see HiddenChatsWire.kt for the table).
 *
 * Every call answers `Response<…>` rather than throwing: a refusal's body is
 * the point here — `attempts_left`, `retry_after`, the code that tells a
 * wrong password from a lockout — and the shared [com.us.android.core.network.ErrorMapper]
 * keeps none of it for a 401 or a 429.
 *
 * Built on the chat Retrofit, whose client adds `X-Device-Id` to these
 * routes and never refreshes-and-replays a refused secret (HiddenChatsHttp.kt).
 */
interface HiddenChatsApi {

    @GET("v1/chat/hidden/settings")
    suspend fun settings(): Response<ApiEnvelope<HiddenSettingsDto>>

    @PUT("v1/chat/hidden/settings")
    suspend fun updateSettings(@Body body: JsonObject): Response<ApiEnvelope<HiddenSettingsDto>>

    /** [stepUpToken]: the identity step-up access token; null when the bearer itself is a fresh sign-in. */
    @POST("v1/chat/hidden/reset")
    suspend fun reset(
        @Header(STEP_UP_TOKEN_HEADER) stepUpToken: String?,
        @Body body: JsonObject,
    ): Response<ApiEnvelope<JsonElement>>

    @POST("v1/chat/hidden/reveal")
    suspend fun reveal(@Body body: JsonObject): Response<ApiEnvelope<HiddenTokenDto>>

    @GET("v1/chat/hidden/conversations")
    suspend fun conversations(
        @Header(REVEAL_HEADER) revealToken: String,
        @Query("limit") limit: Int = HIDDEN_LIST_LIMIT,
        @Query("cursor") cursor: String? = null,
    ): Response<ApiEnvelope<List<ConversationDto>>>

    @POST("v1/chat/hidden/open")
    suspend fun open(
        @Header(REVEAL_HEADER) revealToken: String,
        @Body body: JsonObject,
    ): Response<ApiEnvelope<HiddenTokenDto>>

    /** This device's reveal and open tokens end now (the client re-locked). */
    @POST("v1/chat/hidden/lock")
    suspend fun lock(): Response<ApiEnvelope<JsonElement>>

    @POST("v1/chat/conversations/{id}/hide")
    suspend fun hide(@Path("id") conversationId: String): Response<ApiEnvelope<JsonElement>>

    /** X-Chat-Open is added by the chat client for a known hidden chat; passed here as well, explicitly. */
    @POST("v1/chat/conversations/{id}/unhide")
    suspend fun unhide(
        @Path("id") conversationId: String,
        @Header(OPEN_HEADER) openToken: String?,
    ): Response<ApiEnvelope<JsonElement>>

    /**
     * identity's in-session step-up: a TOTP/recovery code for an access token
     * carrying a fresh `step_up_at`, valid 5 minutes. Never replayed on a 401
     * (a one-time code replayed is refused as reused and counted twice).
     */
    @POST("v1/auth/step-up")
    suspend fun stepUp(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: JsonObject,
    ): Response<ApiEnvelope<StepUpTokenDto>>
}

/** The server's page cap; the hidden list is read whole, a page at a time. */
const val HIDDEN_LIST_LIMIT = 100

/** A hidden-chat call's outcome: the payload (and response headers), or what the refusal means. */
sealed interface HiddenResult<out T> {
    data class Ok<T>(
        val value: T,
        val headers: Headers = Headers.headersOf(),
        /** `meta.next_cursor` on a paged list; null on the last page and everywhere else. */
        val nextCursor: String? = null,
    ) : HiddenResult<T>

    data class Err(val error: HiddenError) : HiddenResult<Nothing>
}

/** Runs one call; a refusal is decoded from its body, a dropped connection is [HiddenError.Network]. */
@Suppress("TooGenericExceptionCaught") // Any transport failure is the same answer: no response.
suspend fun <T> hiddenCall(
    nowMs: () -> Long = System::currentTimeMillis,
    block: suspend () -> Response<ApiEnvelope<T>>,
): HiddenResult<T?> = try {
    val response = block()
    if (response.isSuccessful) {
        val envelope = response.body()
        HiddenResult.Ok(envelope?.data, response.headers(), envelope?.meta?.nextCursor?.takeIf { it.isNotBlank() })
    } else {
        val raw = runCatching { response.errorBody()?.string() }.getOrNull()
        HiddenResult.Err(hiddenErrorOf(response.code(), raw, response.headers()["Retry-After"], nowMs()))
    }
} catch (e: CancellationException) {
    throw e
} catch (e: IOException) {
    HiddenResult.Err(HiddenError.Network)
} catch (e: Exception) {
    HiddenResult.Err(HiddenError.Unknown(status = -1, code = e.javaClass.simpleName))
}
