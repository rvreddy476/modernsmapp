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
 * Settings › Account › Request verification › Official mark (2026-11-15), against
 * profile-service internal/http/official_verification.go (commit 0782f6619):
 *
 *     GET  /v1/profiles/me/verification/organisation               → 200 OrgStatus
 *     POST /v1/profiles/me/verification/organisation               → 201 OrgStatus (next = "email_code")
 *     POST /v1/profiles/me/verification/organisation/confirm-email → 200 OrgStatus
 *     POST /v1/profiles/me/verification/government/accept          → 200 OrgStatus
 *
 * Both marks are off by default: a disabled mark answers 503 `ORG_VERIFICATION_DISABLED`
 * or `GOVERNMENT_VERIFICATION_DISABLED`. The optional `document_media_id` (contract §6,
 * 16 Nov 2026) is the registration certificate, uploaded as media-service's private
 * `document` kind; with it the registry is not asked and the application goes to human
 * review after the e-mail code.
 */
interface OfficialMarkApi {

    @GET("v1/profiles/me/verification/organisation")
    suspend fun status(): ApiEnvelope<OrgStatus>

    @POST("v1/profiles/me/verification/organisation")
    suspend fun apply(@Body body: OrgApplicationBody): ApiEnvelope<OrgStatus>

    @POST("v1/profiles/me/verification/organisation/confirm-email")
    suspend fun confirmEmail(@Body body: OrgEmailCodeBody): ApiEnvelope<OrgStatus>

    @POST("v1/profiles/me/verification/government/accept")
    suspend fun acceptGovernment(@Body body: GovernmentInviteBody): ApiEnvelope<OrgStatus>
}

/**
 * `officialverify.OrgApplicationRequest`; exactly one of [gstin] and [cin] is set, the other
 * omitted. [documentMediaId] is omitted when no certificate is attached.
 */
@Serializable
data class OrgApplicationBody(
    @SerialName("legal_name") val legalName: String,
    val gstin: String? = null,
    val cin: String? = null,
    @SerialName("official_domain") val officialDomain: String,
    @SerialName("signatory_email") val signatoryEmail: String,
    @SerialName("document_media_id") val documentMediaId: String? = null,
)

@Serializable
data class OrgEmailCodeBody(val code: String)

@Serializable
data class GovernmentInviteBody(@SerialName("invite_code") val inviteCode: String)

/** `officialverify.OrgStatus`: the applicant's view of their newest application. */
@Serializable
data class OrgStatus(
    @SerialName("is_verified") val isVerified: Boolean = false,
    /** "person" | "organisation" | "government" | "". */
    @SerialName("verification_level") val verificationLevel: String = "",
    @SerialName("badge_flags") val badgeFlags: Int = 0,
    @SerialName("organisation_name") val organisationName: String = "",
    @SerialName("application_id") val applicationId: String? = null,
    /** "email_pending" | "in_review" | "approved" | "rejected" | "refused" | "superseded". */
    val status: String? = null,
    @SerialName("refusal_code") val refusalCode: String? = null,
    /** RFC 3339. */
    @SerialName("code_expires_at") val codeExpiresAt: String? = null,
    /** The step to show: "apply", "email_code", "review" or "" (done). */
    val next: String = "",
)

/** What the Official mark screens act through; a seam so their ViewModels test against a fake. */
interface OfficialMarkSource {
    suspend fun status(): AppResult<OrgStatus>
    suspend fun apply(body: OrgApplicationBody): AppResult<OrgStatus>
    suspend fun confirmEmail(code: String): AppResult<OrgStatus>
    suspend fun acceptGovernment(inviteCode: String): AppResult<OrgStatus>
}

/**
 * The repository. Every HTTP refusal keeps the server's `error.code` AND `error.message`
 * (as [AppError.Unknown]): the shared [ErrorMapper] drops the message on 403, 429 and 5xx,
 * and on these routes the message is the one written for people.
 */
@Singleton
class OfficialMarkRepository @Inject constructor(
    private val api: OfficialMarkApi,
    private val errorMapper: ErrorMapper,
    private val json: Json,
) : OfficialMarkSource {

    override suspend fun status() = call { api.status() }

    override suspend fun apply(body: OrgApplicationBody) = call { api.apply(body) }

    override suspend fun confirmEmail(code: String) = call { api.confirmEmail(OrgEmailCodeBody(code)) }

    override suspend fun acceptGovernment(inviteCode: String) =
        call { api.acceptGovernment(GovernmentInviteBody(inviteCode)) }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun call(block: suspend () -> ApiEnvelope<OrgStatus>): AppResult<OrgStatus> = try {
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

/** The fallback when a refusal carries no sentence of the server's. */
const val OFFICIAL_FALLBACK_MESSAGE = "Something went wrong. Try again."

/** The server's `error.code`, when the refusal carried one. */
fun AppError.officialCode(): String? = (this as? AppError.Unknown)?.code

/** The server's own sentence, shown as is; [OFFICIAL_FALLBACK_MESSAGE] when there is none. */
fun AppError.officialMessage(): String = (this as? AppError.Unknown)?.message ?: OFFICIAL_FALLBACK_MESSAGE
