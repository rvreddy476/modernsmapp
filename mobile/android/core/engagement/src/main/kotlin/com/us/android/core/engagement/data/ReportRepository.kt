package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/** What filing a report came to. */
sealed interface ReportOutcome {
    /** Filed. */
    data object Filed : ReportOutcome

    /** `409 ACTIVE_REPORT_EXISTS`: the viewer already has one open on this content. */
    data object AlreadyReported : ReportOutcome

    data class Failed(val error: AppError) : ReportOutcome
}

/**
 * Files reports against content, mapping the one status the UI must tell
 * apart — "you already reported this" — out of the generic failure path.
 */
@Singleton
class ReportRepository @Inject constructor(
    private val api: ReportApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * Reports one post. [reason] must be one of [REASONS]; [details] is free
     * text and is sent even when blank, because the handler accepts either.
     */
    suspend fun reportPost(postId: String, reason: String, details: String): ReportOutcome =
        file(ENTITY_POST, postId, reason, details)

    /**
     * Reports an account, from its profile (2026-10-14, audit T-03): entity
     * type `user`, the profile's user id. Same reasons, same 409 handling as
     * a post — trust-safety's intake treats them alike.
     */
    suspend fun reportUser(userId: String, reason: String, details: String): ReportOutcome =
        file(ENTITY_USER, userId, reason, details)

    /**
     * Reports one comment, from its row's ⋮ in the comments sheet
     * (2026-10-14, audit T-17): entity type `comment` — in trust-safety's
     * `oneof=user post comment reel video` — and the comment's id.
     */
    suspend fun reportComment(commentId: String, reason: String, details: String): ReportOutcome =
        file(ENTITY_COMMENT, commentId, reason, details)

    private suspend fun file(entityType: String, entityId: String, reason: String, details: String): ReportOutcome {
        require(reason in REASONS) { "unknown report reason: $reason" }
        val request = FileReportRequest(
            entityType = entityType,
            entityId = entityId,
            reason = reason,
            details = details,
        )
        val result = apiCall(errorMapper) { api.fileReport(request) }
        return when (result) {
            is AppResult.Success -> ReportOutcome.Filed
            is AppResult.Failure -> {
                val error = result.error
                // The mapper files a 409-with-code as Unknown; the code is the
                // contract and is what the UI needs to branch on.
                if (error is AppError.Unknown && error.code == CODE_ACTIVE_REPORT_EXISTS) {
                    ReportOutcome.AlreadyReported
                } else {
                    ReportOutcome.Failed(error)
                }
            }
        }
    }

    companion object {
        const val ENTITY_POST = "post"
        const val ENTITY_USER = "user"
        const val ENTITY_COMMENT = "comment"
        const val CODE_ACTIVE_REPORT_EXISTS = "ACTIVE_REPORT_EXISTS"

        /** The reason tokens trust-safety accepts, in the sheet's order. */
        val REASONS: List<String> = listOf(
            "spam",
            "harassment",
            "nudity",
            "violence",
            "hate",
            "false_info",
            "scam_fraud",
            "impersonation",
            "self_harm",
            "intellectual_property",
            "other",
        )
    }
}
