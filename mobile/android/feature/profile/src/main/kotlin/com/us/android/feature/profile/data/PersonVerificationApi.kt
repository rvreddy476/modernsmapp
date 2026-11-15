package com.us.android.feature.profile.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import retrofit2.HttpException
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Request verification › Verify you're a real person (2026-11-15), against
 * profile-service internal/http/person_verification.go and personverify.Status:
 *
 *     GET  /v1/profiles/me/verification/person           → 200 PersonStatus
 *     POST /v1/profiles/me/verification/person/start     → 201 {attempt_id, authorize_url, expires_at}
 *     POST /v1/profiles/me/verification/person/callback  {state, code} → 200 PersonStatus
 *     POST /v1/profiles/me/verification/person/selfie    {selfie_media_id} → 200 PersonStatus
 *
 * Off by default: an unwired server answers 503 `PERSON_VERIFICATION_DISABLED`.
 * The DigiLocker `code` and `state` are credentials: never logged, never put in a route.
 */
interface PersonVerificationApi {

    @GET("v1/profiles/me/verification/person")
    suspend fun status(): ApiEnvelope<PersonStatus>

    @POST("v1/profiles/me/verification/person/start")
    suspend fun start(@Body body: PersonStartBody): ApiEnvelope<PersonStart>

    @POST("v1/profiles/me/verification/person/callback")
    suspend fun callback(@Body body: PersonCallbackBody): ApiEnvelope<PersonStatus>

    @POST("v1/profiles/me/verification/person/selfie")
    suspend fun selfie(@Body body: PersonSelfieBody): ApiEnvelope<PersonStatus>
}

/** The start body is `{}`. */
@Serializable
class PersonStartBody

@Serializable
data class PersonCallbackBody(val state: String, val code: String)

@Serializable
data class PersonSelfieBody(@SerialName("selfie_media_id") val selfieMediaId: String)

/** `personverify.StartResult`. */
@Serializable
data class PersonStart(
    @SerialName("attempt_id") val attemptId: String = "",
    @SerialName("authorize_url") val authorizeUrl: String = "",
    /** RFC 3339. */
    @SerialName("expires_at") val expiresAt: String = "",
)

/** `personverify.AttemptStatus`: the newest attempt. */
@Serializable
data class PersonAttempt(
    val id: String = "",
    /** "started" | "identity_checked" | "approved" | "refused". */
    val status: String = "",
    @SerialName("refusal_code") val refusalCode: String? = null,
    /** RFC 3339. */
    @SerialName("expires_at") val expiresAt: String = "",
)

/** `personverify.Status`. */
@Serializable
data class PersonStatus(
    @SerialName("is_verified") val isVerified: Boolean = false,
    @SerialName("verification_level") val verificationLevel: String = "",
    @SerialName("badge_flags") val badgeFlags: Int = 0,
    val attempt: PersonAttempt? = null,
    /** The step to show: "digilocker", "selfie" or "" (done). */
    val next: String = "",
)

/** What the person-verification screens act through; a seam so their ViewModels test against a fake. */
interface PersonVerificationSource {
    suspend fun status(): AppResult<PersonStatus>
    suspend fun start(): AppResult<PersonStart>
    suspend fun callback(state: String, code: String): AppResult<PersonStatus>
    suspend fun selfie(mediaId: String): AppResult<PersonStatus>
}

/**
 * The repository. Like [OfficialMarkRepository], every HTTP refusal keeps the server's
 * `error.code` AND `error.message` (as [AppError.Unknown]); read them with [officialCode]
 * and [officialMessage].
 */
@Singleton
class PersonVerificationRepository @Inject constructor(
    private val api: PersonVerificationApi,
    private val errorMapper: ErrorMapper,
    private val json: Json,
) : PersonVerificationSource {

    override suspend fun status() = call { api.status() }

    override suspend fun start() = call { api.start(PersonStartBody()) }

    override suspend fun callback(state: String, code: String) = call { api.callback(PersonCallbackBody(state, code)) }

    override suspend fun selfie(mediaId: String) = call { api.selfie(PersonSelfieBody(mediaId)) }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun <T : Any> call(block: suspend () -> ApiEnvelope<T>): AppResult<T> = try {
        val envelope = block()
        val data = envelope.data
        when {
            envelope.error != null -> AppResult.Failure(
                AppError.Unknown(
                    code = envelope.error?.code,
                    statusCode = null,
                    message = envelope.error?.message?.takeIf { it.isNotBlank() },
                ),
            )
            data != null -> AppResult.Success(data)
            else -> AppResult.Failure(AppError.Malformed("2xx response carried neither `data` nor `error`"))
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: HttpException) {
        AppResult.Failure(refusal(e))
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    private fun refusal(e: HttpException): AppError {
        val raw = runCatching { e.response()?.errorBody()?.string() }.getOrNull()
        val error = raw?.let { body ->
            runCatching { json.decodeFromString<ApiEnvelope<JsonObject>>(body) }.getOrNull()
        }?.error
        return AppError.Unknown(
            code = error?.code?.takeIf { it.isNotBlank() },
            statusCode = e.code(),
            message = error?.message?.takeIf { it.isNotBlank() },
        )
    }
}
