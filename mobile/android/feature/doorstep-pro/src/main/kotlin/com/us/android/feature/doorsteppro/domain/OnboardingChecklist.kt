package com.us.android.feature.doorsteppro.domain

import com.us.android.feature.doorsteppro.data.ProReadinessDto

/**
 * The onboarding steps, in the contract's order (OnboardingStep enum,
 * prokyc.RequiredSteps): profile → Aadhaar via DigiLocker (sets gender) →
 * selfie face match → skills → service area → weekly hours → bank → police
 * certificate → agreement; PAN is recommended, never required.
 *
 * The CHECKLIST ORDER IS THIS ENUM'S, never the order the server happened to
 * list steps in: a professional who comes back finds the same list in the
 * same place.
 */
enum class OnboardingStep(val wire: String, val title: String, val blurb: String) {
    PROFILE("profile", "Your name and photo", "How customers will see you"),
    AADHAAR("aadhaar_digilocker", "Aadhaar via DigiLocker", "Confirms who you are"),
    SELFIE("selfie_face_match", "Selfie", "Matched with your Aadhaar photo"),
    SKILLS("skills", "Your skills", "And a trade certificate where a skill needs one"),
    SERVICE_AREA("service_area", "Where you work", "Your home point and how far you travel"),
    WEEKLY_HOURS("weekly_hours", "Working hours", "When you take jobs each week"),
    BANK("bank", "Bank account", "Where your earnings go"),
    POLICE_CERTIFICATE("police_certificate", "Police clearance certificate", "Reviewed by Doorstep"),
    AGREEMENT("agreement", "Professional agreement", "Read and accept"),
    PAN("pan", "PAN", "Recommended, not required"),
    ;

    companion object {
        fun of(wire: String): OnboardingStep? = entries.firstOrNull { it.wire == wire }
    }
}

enum class StepState {
    /** In completed_steps. */
    DONE,

    /** Missing and the professional can do it now. */
    TO_DO,

    /** Missing, but the professional has done their part: an admin review remains. */
    IN_REVIEW,

    /** Missing, but an earlier step must come first (the selfie is matched against the Aadhaar photo). */
    WAITING,

    /** PAN, not yet given. Never blocks approval. */
    OPTIONAL,
}

data class ChecklistItem(val step: OnboardingStep, val state: StepState) {
    /** Whether tapping the row opens the step's screen. A done step stays editable; a review in progress does not. */
    val actionable: Boolean get() = state == StepState.TO_DO || state == StepState.OPTIONAL || state == StepState.DONE
}

/** The professional's account status (ProStatus). */
enum class ProStatus(val wire: String) {
    DRAFT("draft"),
    PENDING_VERIFICATION("pending_verification"),
    APPROVED("approved"),
    SUSPENDED("suspended"),
    REJECTED("rejected"),
    BLOCKED("blocked"),
    UNKNOWN(""),
    ;

    companion object {
        fun of(wire: String?): ProStatus = entries.firstOrNull { it.wire == wire && it != UNKNOWN } ?: UNKNOWN
    }
}

data class Checklist(
    val status: ProStatus,
    val items: List<ChecklistItem>,
    /** Server steps this build does not know — shown as plain rows so nothing is hidden. */
    val unknownMissing: List<String>,
    val canGoOnDuty: Boolean,
) {
    /** The first step the professional can act on now; null when nothing is left for them to do. */
    val next: OnboardingStep? get() = items.firstOrNull { it.state == StepState.TO_DO }?.step

    val requiredLeft: Int get() = items.count { it.step != OnboardingStep.PAN && it.state != StepState.DONE } + unknownMissing.size

    val requiredTotal: Int get() = items.count { it.step != OnboardingStep.PAN } + unknownMissing.size

    /** Everything the professional can do is done; only Doorstep's reviews remain. */
    val waitingForReview: Boolean
        get() = status == ProStatus.PENDING_VERIFICATION ||
            (requiredLeft > 0 && items.none { it.state == StepState.TO_DO || it.state == StepState.WAITING } && unknownMissing.isEmpty())
}

/**
 * The checklist a professional sees, from `GET /pro/readiness` (pure
 * MissingSteps on the server) plus what this device knows is under review.
 *
 *  - order: [OnboardingStep] order, always;
 *  - a step in `completed_steps` is DONE;
 *  - a missing step is IN_REVIEW when the account is pending_verification (the
 *    server moves a draft there exactly when every missing step awaits an
 *    admin — prokyc.AwaitingReviewOnly) or when this device uploaded its
 *    document ([submittedForReview]); the selfie WAITS for Aadhaar; anything
 *    else is TO_DO;
 *  - PAN is DONE or OPTIONAL, never TO_DO.
 */
object OnboardingChecklist {

    fun of(readiness: ProReadinessDto, submittedForReview: Set<OnboardingStep> = emptySet()): Checklist {
        val status = ProStatus.of(readiness.status)
        val completed = readiness.completedSteps.mapNotNull(OnboardingStep::of).toSet()
        val missing = readiness.missingSteps.mapNotNull(OnboardingStep::of).toSet()
        val reviewOnly = status == ProStatus.PENDING_VERIFICATION
        val aadhaarDone = OnboardingStep.AADHAAR in completed || OnboardingStep.AADHAAR !in missing
        val items = OnboardingStep.entries.map { step ->
            val state = when {
                step in completed -> StepState.DONE
                step == OnboardingStep.PAN -> StepState.OPTIONAL
                step !in missing -> StepState.DONE
                reviewOnly || step in submittedForReview -> StepState.IN_REVIEW
                step == OnboardingStep.SELFIE && !aadhaarDone -> StepState.WAITING
                else -> StepState.TO_DO
            }
            ChecklistItem(step, state)
        }
        val unknown = readiness.missingSteps.filter { OnboardingStep.of(it) == null }
        return Checklist(status = status, items = items, unknownMissing = unknown, canGoOnDuty = readiness.canGoOnDuty)
    }
}
