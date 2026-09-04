package com.us.android.core.engagement.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.POST

/**
 * trust-safety-service's report intake — `POST /v1/reports`.
 *
 * Lives beside the engagement endpoints rather than in `:feature:feed`
 * because a report is filed from wherever content is seen: a feed card, a
 * reel, later a comment or a profile. One interface, one reason vocabulary.
 *
 * Verified on the dev gateway on 2026-09-04: `{"entity_type":"post",
 * "entity_id":…,"reason":"hate","details":…}` answers 200 with the stored
 * report; a second active report on the same entity answers
 * `409 ACTIVE_REPORT_EXISTS`, which the repository turns into
 * [ReportOutcome.AlreadyReported] rather than a failure — the viewer's
 * intent is already on file, and that is what they need to hear.
 */
interface ReportApi {

    @POST("v1/reports")
    suspend fun fileReport(@Body body: FileReportRequest): ApiEnvelope<ReportDto>
}

@Serializable
data class FileReportRequest(
    /** `user`, `post`, `comment`, `reel`, `video` — the handler's `oneof`. */
    @SerialName("entity_type") val entityType: String,
    @SerialName("entity_id") val entityId: String,
    /** One of the reason tokens in [ReportRepository.REASONS]. */
    val reason: String,
    val details: String = "",
)

/** The stored report. Only its existence matters to the client. */
@Serializable
data class ReportDto(
    val id: String = "",
    val status: String = "",
)
