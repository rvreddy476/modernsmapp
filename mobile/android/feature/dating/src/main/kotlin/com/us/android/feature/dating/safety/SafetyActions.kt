package com.us.android.feature.dating.safety

import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.map
import com.us.android.feature.dating.network.ReportEvidenceDto
import com.us.android.feature.dating.network.ReportRequest
import com.us.android.feature.dating.network.ReportResultDto
import javax.inject.Inject
import javax.inject.Singleton

/** The nine report reasons dating-service accepts (store/reports.go), in the order they are offered. */
enum class ReportReason(val wire: String, val label: String) {
    HARASSMENT("harassment", "Harassment or bullying"),
    FAKE_PROFILE("fake_profile", "Fake profile or catfishing"),
    UNDERAGE("underage", "They may be under 18"),
    NUDITY("nudity", "Nudity or sexual content"),
    SCAM("scam", "Scam or asking for money"),
    HATE("hate", "Hate speech"),
    VIOLENCE("violence", "Violence or threats"),
    SPAM("spam", "Spam or advertising"),
    OTHER("other", "Something else"),
}

/** What the report sheet collects. Evidence ids come from where the report was opened. */
data class ReportDraft(
    val targetId: String,
    val reason: ReportReason? = null,
    val details: String = "",
    val photoIds: List<String> = emptyList(),
    val sparkIds: List<String> = emptyList(),
    val messageIds: List<String> = emptyList(),
    val includeEvidence: Boolean = true,
)

/** The server's report limits, checked before sending so the sheet can say what is wrong. Pure. */
object ReportRules {
    /** Runes after trimming, as the server counts. */
    const val MAX_DETAILS = 500
    const val MAX_PHOTO_IDS = 10
    const val MAX_SPARK_IDS = 10
    const val MAX_MESSAGE_IDS = 20

    fun detailsLength(details: String): Int = details.trim().let { it.codePointCount(0, it.length) }

    /** Null when [draft] can be sent; otherwise the sentence to show. */
    fun problem(draft: ReportDraft): String? = when {
        draft.reason == null -> "Choose a reason."
        draft.reason == ReportReason.OTHER && draft.details.isBlank() -> "Tell us what happened."
        detailsLength(draft.details) > MAX_DETAILS -> "Keep the details under $MAX_DETAILS characters."
        else -> null
    }

    fun toRequest(draft: ReportDraft): ReportRequest {
        val reason = checkNotNull(draft.reason) { "a report needs a reason" }
        val evidence = if (draft.includeEvidence) {
            ReportEvidenceDto(
                photoIds = draft.photoIds.take(MAX_PHOTO_IDS).takeIf { it.isNotEmpty() },
                sparkIds = draft.sparkIds.take(MAX_SPARK_IDS).takeIf { it.isNotEmpty() },
                messageIds = draft.messageIds.take(MAX_MESSAGE_IDS).takeIf { it.isNotEmpty() },
            ).takeIf { it.photoIds != null || it.sparkIds != null || it.messageIds != null }
        } else {
            null
        }
        return ReportRequest(
            targetId = draft.targetId,
            reason = reason.wire,
            details = draft.details.trim().takeIf { it.isNotEmpty() },
            evidence = evidence,
        )
    }
}

/**
 * Block and report, shared by every screen that shows a person.
 *
 * Both remove the person from EVERY list through [DatingSession.removePerson]
 * the moment the server confirms: a block closes the match and deletes sparks
 * both ways server-side, and a report blocks the target automatically
 * (`blocked: true`).
 */
@Singleton
class SafetyActions @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
) {

    suspend fun block(userId: String): DatingResult<Unit> {
        val result = repository.block(userId)
        if (result is DatingResult.Success && result.value.blocked) session.removePerson(userId)
        return result.map { }
    }

    suspend fun report(draft: ReportDraft): DatingResult<ReportResultDto> {
        ReportRules.problem(draft)?.let { return DatingResult.Failure(DatingError.Unexpected(null, it)) }
        val result = repository.report(ReportRules.toRequest(draft))
        if (result is DatingResult.Success && (result.value.blocked || result.value.autoBlocked)) {
            session.removePerson(draft.targetId)
        }
        return result
    }
}
