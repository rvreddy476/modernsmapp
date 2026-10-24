package com.us.android.core.profile.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import okhttp3.ResponseBody
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Streaming
import java.io.OutputStream
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Settings › Download your data and Request verification (2026-10-24, the
 * web's /settings/data and /settings/verification):
 *
 *     GET  /v1/auth/data-export          auth-service handler.go:326 (ExportUserData :1311-1326):
 *                                        the account's data as a JSON FILE — no envelope,
 *                                        `Content-Disposition: attachment; filename=data-export.json`
 *     POST /v1/verification-requests     trust-safety-service handler.go:122 (SubmitVerificationRequest,
 *                                        trust_extras_handler.go:241-264): `{type, docs?}` → 201 the request;
 *                                        400 BAD_REQUEST with the server's sentence (a pending one exists, a bad type)
 */
interface AccountDataApi {

    @Streaming
    @GET("v1/auth/data-export")
    suspend fun dataExport(): ResponseBody

    @POST("v1/verification-requests")
    suspend fun requestVerification(@Body body: VerificationRequestBody): ApiEnvelope<VerificationRequestDto>
}

@Serializable
data class VerificationRequestBody(
    val type: String,
    val docs: Map<String, String>? = null,
)

/** `postgres.VerificationRequest` (trust-safety store/postgres/trust_extras.go:86-96). */
@Serializable
data class VerificationRequestDto(
    val id: String = "",
    val type: String = "",
    val status: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

/** The four kinds trust-safety accepts (service/trust_extras.go:14-16), in the web's words. */
enum class VerificationKind(val wire: String, val label: String, val detail: String) {
    CREATOR("creator", "Creator", "A content creator, influencer, or artist with an established audience."),
    BUSINESS("business", "Business", "A company or brand with a commercial presence."),
    ORGANIZATION("organization", "Organization", "A non-profit, institution, club or other organization."),
    GOVERNMENT("government", "Government", "A government body, agency or official."),
}

/** What the two settings pages act through; a seam so their ViewModels test against a fake. */
interface AccountDataSource {
    /** Streams the export into [output]; the bytes written, or why not. */
    suspend fun exportTo(output: OutputStream): AppResult<Long>

    /** Submits a request; the status the server stored (`pending`). */
    suspend fun requestVerification(kind: VerificationKind, note: String = ""): AppResult<String>
}

@Singleton
class AccountDataRepository @Inject constructor(
    private val api: AccountDataApi,
    private val errorMapper: ErrorMapper,
) : AccountDataSource {

    @Suppress("TooGenericExceptionCaught")
    override suspend fun exportTo(output: OutputStream): AppResult<Long> = try {
        val body = api.dataExport()
        withContext(Dispatchers.IO) { body.use { it.byteStream().use { input -> AppResult.Success(input.copyTo(output)) } } }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    override suspend fun requestVerification(kind: VerificationKind, note: String): AppResult<String> =
        apiCall(errorMapper) {
            api.requestVerification(
                VerificationRequestBody(type = kind.wire, docs = note.trim().takeIf { it.isNotEmpty() }?.let { mapOf("note" to it) }),
            )
        }.map { it.status.ifBlank { "pending" } }
}

/** A refused verification request, in words (the server's own sentence when it sent one). */
fun verificationRefusal(error: AppError): String = when (error) {
    is AppError.NoNetwork, is AppError.Timeout -> "You're offline. Try again when you're connected."
    is AppError.RateLimited -> "Too many requests. Wait a moment and try again."
    is AppError.Unknown -> when {
        error.message?.contains("pending", ignoreCase = true) == true -> "You already have a request waiting for review."
        else -> error.message ?: "Your request couldn't be sent. Try again."
    }
    else -> "Your request couldn't be sent. Try again."
}
