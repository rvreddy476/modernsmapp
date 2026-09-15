package com.us.android.feature.dating

import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.UpsertProfileRequest

/**
 * How far away someone is — as a BUCKET, never a number (D7).
 *
 * The app renders only these four labels, mapped from the bucket CODE. The
 * server's own `distance_label` is ignored, and an unknown code renders
 * nothing: a future server that sent "3.2 km" must not reach the screen.
 */
enum class DistanceBucket(val code: String, val label: String) {
    UNDER_5_KM("lt_5_km", "< 5 km"),
    FROM_5_TO_10_KM("km_5_10", "5–10 km"),
    FROM_10_TO_25_KM("km_10_25", "10–25 km"),
    OVER_25_KM("gt_25_km", "25+ km"),
    ;

    companion object {
        fun fromCode(code: String?): DistanceBucket? = entries.firstOrNull { it.code == code?.trim() }

        /** The label for [code], or null. The only way distance text is produced in this module. */
        fun labelFor(code: String?): String? = fromCode(code)?.label
    }
}

/** The server's profile status machine, as the app routes it. */
enum class OnboardingStep {
    /** No dating profile yet. */
    CREATE,

    /** draft: intent and gender (name and birth date come from identity). */
    BASICS,

    /** draft: a point or a city. */
    LOCATION,

    /** draft: who you're interested in. */
    PREFERENCES,

    /** pending_photo: an approved primary photo. */
    PHOTOS,

    /** pending_selfie: the blink-twice selfie video. */
    SELFIE,

    /** pending_review: moderators are looking. */
    REVIEW,

    /** paused by the person. */
    PAUSED,

    /** restricted, suspended, or anything this app does not know. */
    HELD,

    /** active: Pulse, sparks and matches. */
    READY,
}

/**
 * The onboarding gate: `draft → pending_photo → pending_selfie → active`, plus
 * paused and the holds. The SERVER advances the status; this only decides
 * which screen a status means, and inside draft which field is still missing.
 *
 * The one rule a mistake here would break: nothing but `active` opens Pulse.
 * An unknown status is a hold, never READY.
 */
object OnboardingGate {

    fun stepFor(profile: DatingProfileDto?, preferences: PreferencesDto?): OnboardingStep {
        if (profile == null) return OnboardingStep.CREATE
        return when (profile.profileStatus) {
            STATUS_DRAFT -> draftStep(profile, preferences)
            STATUS_PENDING_PHOTO -> OnboardingStep.PHOTOS
            STATUS_PENDING_SELFIE -> OnboardingStep.SELFIE
            STATUS_PENDING_REVIEW -> OnboardingStep.REVIEW
            STATUS_ACTIVE -> OnboardingStep.READY
            STATUS_PAUSED -> OnboardingStep.PAUSED
            else -> OnboardingStep.HELD
        }
    }

    /** Identity supplies these; when either is missing the server can never leave draft. */
    fun identityIncomplete(profile: DatingProfileDto): Boolean =
        profile.firstName.isNullOrBlank() || profile.birthDate.isNullOrBlank()

    private fun draftStep(profile: DatingProfileDto, preferences: PreferencesDto?): OnboardingStep = when {
        profile.intent.isBlank() || profile.gender.isNullOrBlank() -> OnboardingStep.BASICS
        !hasLocation(profile) -> OnboardingStep.LOCATION
        preferences?.interestedInGender.isNullOrBlank() -> OnboardingStep.PREFERENCES
        // Everything the app can supply is there and the server still says
        // draft: identity is missing a name or birth date. Basics explains.
        else -> OnboardingStep.BASICS
    }

    private fun hasLocation(profile: DatingProfileDto): Boolean =
        (profile.latitude != null && profile.longitude != null) || !profile.city.isNullOrBlank()

    const val STATUS_DRAFT = "draft"
    const val STATUS_PENDING_PHOTO = "pending_photo"
    const val STATUS_PENDING_SELFIE = "pending_selfie"
    const val STATUS_PENDING_REVIEW = "pending_review"
    const val STATUS_ACTIVE = "active"
    const val STATUS_PAUSED = "paused"
}

/** The four consent types (D9), with the words the app asks with. */
enum class ConsentType(val wire: String, val title: String, val body: String) {
    SENSITIVE_RELIGION(
        wire = "sensitive_religion",
        title = "Share your religion?",
        body = "Religion is sensitive personal data. With your consent we store it encrypted and use it only to show " +
            "it on your profile and to match you. You can withdraw consent any time in Privacy; we then delete it.",
    ),
    SENSITIVE_COMMUNITY(
        wire = "sensitive_community",
        title = "Share your community?",
        body = "Community is sensitive personal data. With your consent we store it encrypted and use it only to show " +
            "it on your profile and to match you. You can withdraw consent any time in Privacy; we then delete it.",
    ),
    BIOMETRIC_SELFIE(
        wire = "biometric_selfie",
        title = "Allow a face check?",
        body = "To verify it's really you, we compare a short selfie video with your main photo. The video is used " +
            "only for this check. You can withdraw consent any time in Privacy.",
    ),
    ECHOES(
        wire = "echoes",
        title = "Show your Momentum activity?",
        body = "Echoes shows a little of your public Momentum activity on your dating profile. It's off unless you turn it on.",
    ),
    ;

    companion object {
        fun fromWire(wire: String?): ConsentType? = entries.firstOrNull { it.wire == wire }
    }
}

/**
 * Consent is asked IN FLOW, before the data it covers leaves the device:
 * religion and community before the profile save that carries them, the
 * biometric consent before a selfie challenge is even requested.
 */
object ConsentGate {

    fun granted(consents: ConsentsDto?, type: ConsentType): Boolean =
        consents?.consents?.any { it.consentType == type.wire && it.granted } == true

    /** The consents [request] needs that [consents] has not granted, in the order they are asked. */
    fun missingFor(request: UpsertProfileRequest, consents: ConsentsDto?): List<ConsentType> = buildList {
        if (!request.religion.isNullOrBlank() && !granted(consents, ConsentType.SENSITIVE_RELIGION)) {
            add(ConsentType.SENSITIVE_RELIGION)
        }
        if (!request.community.isNullOrBlank() && !granted(consents, ConsentType.SENSITIVE_COMMUNITY)) {
            add(ConsentType.SENSITIVE_COMMUNITY)
        }
    }

    /** [request] without the fields whose consent was declined. */
    fun withoutDeclined(request: UpsertProfileRequest, declined: Set<ConsentType>): UpsertProfileRequest = request.copy(
        religion = request.religion.takeUnless { ConsentType.SENSITIVE_RELIGION in declined },
        community = request.community.takeUnless { ConsentType.SENSITIVE_COMMUNITY in declined },
    )

    fun selfieNeedsConsent(consents: ConsentsDto?): Boolean = !granted(consents, ConsentType.BIOMETRIC_SELFIE)
}
