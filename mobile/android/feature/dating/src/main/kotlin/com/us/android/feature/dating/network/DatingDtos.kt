package com.us.android.feature.dating.network

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/*
 * dating-service wire types.
 *
 * Shapes with a golden fixture (Architecture/services/dating-service/internal/
 * http/testdata/contracts, copied byte-identical into src/test/resources) are
 * pinned by DatingContractFixtureTest, which decodes STRICTLY. Shapes without a
 * fixture were read from the Go structs (store.go, the service package, handler_ files)
 * and are marked "no fixture" — production decodes leniently, so a drift there
 * degrades rather than crashes, and each is reported as a backend gap.
 *
 * Timestamps stay Strings: the fixtures redact them to "<timestamp>", and no
 * screen does date arithmetic the server has not already done.
 */

// ── People (the compact person card) ────────────────────────────────────────

/**
 * Who someone is, in the only shape the server hands out about another person:
 * `GET /people/:userId`, and inline on matches, match detail and incoming sparks.
 *
 * [photoState] is the server's D6 verdict — `full` or `blurred`. The app renders
 * the variant it names and NEVER upgrades a blurred card to the full image;
 * anything the app does not recognise is treated as blurred (PhotoRules).
 *
 * [distanceBucket] / [distanceLabel] are present only when both sides have a
 * location, and only the BUCKET code is ever rendered (DistanceBucket).
 */
@Serializable
data class DatingPersonDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("first_name") val firstName: String = "",
    val age: Int = 0,
    @SerialName("primary_photo_id") val primaryPhotoId: String? = null,
    @SerialName("primary_photo_url") val primaryPhotoUrl: String? = null,
    /** full | blurred */
    @SerialName("photo_state") val photoState: String = "",
    val verified: Boolean = false,
    @SerialName("trust_tier") val trustTier: String = "",
    @SerialName("distance_bucket") val distanceBucket: String? = null,
    /** The server's own label. Never rendered: the app maps the bucket code itself. */
    @SerialName("distance_label") val distanceLabel: String? = null,
    /**
     * The pre-match "enough to decide" block, present only where the viewer is
     * deciding about this person: `GET /people/:userId` and an incoming spark.
     * The match list, trusted contacts and the location shares stay compact —
     * the server omits it there, and the app never asks for or synthesises it.
     */
    val detail: ProfileDetailDto? = null,
)

/**
 * What a viewer may see about someone BEFORE matching: the description they
 * wrote, their prompt answers, their languages and their whole approved
 * gallery. Additive — every member is omitted when empty and the whole block
 * is omitted when a person has written nothing, so every field defaults.
 *
 * Still sealed until a match, and deliberately absent here: religion,
 * community, exact location, birth date and a hidden last-active.
 */
@Serializable
data class ProfileDetailDto(
    val bio: String = "",
    val prompts: List<DetailPromptDto> = emptyList(),
    val languages: List<String> = emptyList(),
    /** The whole approved gallery, primary first. Each entry carries its OWN variant. */
    val photos: List<CardPhotoDto> = emptyList(),
)

/** One catalogue question and this person's answer; the question text is resolved server-side. */
@Serializable
data class DetailPromptDto(
    @SerialName("prompt_id") val promptId: Int = 0,
    val question: String = "",
    val answer: String = "",
)

/**
 * One photo in a card's swipeable gallery.
 *
 * [state] is this photo's OWN D6 verdict — the server applies the rule per
 * photo, so a public photo and a match_only one in the same gallery differ.
 * The app obeys it through PhotoRules and never upgrades a blurred entry.
 */
@Serializable
data class CardPhotoDto(
    val id: String = "",
    val url: String = "",
    /** full | blurred */
    val state: String = "",
)

// ── Consent (D9) ────────────────────────────────────────────────────────────

@Serializable
data class ConsentsDto(
    @SerialName("current_policy_version") val currentPolicyVersion: String = "",
    val consents: List<ConsentStateDto> = emptyList(),
)

@Serializable
data class ConsentStateDto(
    @SerialName("consent_type") val consentType: String,
    val granted: Boolean = false,
    @SerialName("policy_version") val policyVersion: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

@Serializable
data class ConsentRequest(val granted: Boolean)

// ── Profile (D2), no fixture for the 200 ────────────────────────────────────

@Serializable
data class DatingProfileDto(
    @SerialName("user_id") val userId: String = "",
    val intent: String = "",
    val bio: String = "",
    val gender: String? = null,
    /** From identity; read-only in the app. */
    @SerialName("birth_date") val birthDate: String? = null,
    val city: String? = null,
    val state: String? = null,
    val country: String? = null,
    val latitude: Double? = null,
    val longitude: Double? = null,
    @SerialName("location_geohash") val locationGeohash: String? = null,
    @SerialName("height_cm") val heightCm: Int? = null,
    val religion: String? = null,
    val community: String? = null,
    val occupation: String? = null,
    val education: String? = null,
    val drinking: String? = null,
    val smoking: String? = null,
    val exercise: String? = null,
    val diet: String? = null,
    @SerialName("wants_children") val wantsChildren: String? = null,
    @SerialName("family_plans") val familyPlans: String? = null,
    @SerialName("blur_mode") val blurMode: Boolean = false,
    @SerialName("visible_to_public") val visibleToPublic: Boolean = false,
    val paused: Boolean = false,
    @SerialName("language_prefs") val languagePrefs: List<String>? = null,
    @SerialName("trust_tier") val trustTier: String = "",
    @SerialName("profile_status") val profileStatus: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    @SerialName("deleted_at") val deletedAt: String? = null,
    /** From identity; read-only in the app. */
    @SerialName("first_name") val firstName: String? = null,
    @SerialName("prior_status") val priorStatus: String? = null,
    @SerialName("dob_source") val dobSource: String? = null,
    @SerialName("first_name_source") val firstNameSource: String? = null,
)

/**
 * `POST /v1/dating/profile` — a partial upsert: null means "don't write".
 *
 * There is deliberately NO first_name and NO birth_date: both come from
 * identity and the app never sends them.
 */
@Serializable
data class UpsertProfileRequest(
    val intent: String? = null,
    val bio: String? = null,
    val gender: String? = null,
    val city: String? = null,
    val state: String? = null,
    val country: String? = null,
    val latitude: Double? = null,
    val longitude: Double? = null,
    @SerialName("height_cm") val heightCm: Int? = null,
    val religion: String? = null,
    val community: String? = null,
    val occupation: String? = null,
    val education: String? = null,
)

@Serializable
data class PauseRequest(val paused: Boolean)

@Serializable
data class DeleteProfileRequest(val reason: String? = null)

@Serializable
data class StatusDto(val status: String = "")

// ── Preferences, no fixture ─────────────────────────────────────────────────

@Serializable
data class PreferencesDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("min_age") val minAge: Int? = null,
    @SerialName("max_age") val maxAge: Int? = null,
    @SerialName("distance_km") val distanceKm: Int = 0,
    @SerialName("interested_in_gender") val interestedInGender: String? = null,
    @SerialName("intent_filter") val intentFilter: List<String>? = null,
    @SerialName("blur_mode_pref") val blurModePref: Boolean = false,
    @SerialName("language_filter") val languageFilter: List<String>? = null,
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class PreferencesRequest(
    @SerialName("min_age") val minAge: Int? = null,
    @SerialName("max_age") val maxAge: Int? = null,
    @SerialName("distance_km") val distanceKm: Int? = null,
    @SerialName("interested_in_gender") val interestedInGender: String? = null,
)

// ── Privacy (fixture: privacy_get_200) ──────────────────────────────────────

@Serializable
data class PrivacyDto(
    val incognito: Boolean = false,
    @SerialName("hide_last_active") val hideLastActive: Boolean = false,
    @SerialName("approximate_location") val approximateLocation: Boolean = true,
    @SerialName("verified_only_filter") val verifiedOnlyFilter: Boolean = false,
    @SerialName("blur_photos_until_match") val blurPhotosUntilMatch: Boolean = false,
    @SerialName("echoes_consent") val echoesConsent: Boolean = false,
)

@Serializable
data class PrivacyUpdateRequest(
    val incognito: Boolean? = null,
    @SerialName("hide_last_active") val hideLastActive: Boolean? = null,
    @SerialName("verified_only_filter") val verifiedOnlyFilter: Boolean? = null,
    @SerialName("blur_photos_until_match") val blurPhotosUntilMatch: Boolean? = null,
)

// ── Photos (D6), no fixture ─────────────────────────────────────────────────

@Serializable
data class DatingPhotoDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("media_id") val mediaId: String = "",
    @SerialName("sort_order") val sortOrder: Int = 0,
    @SerialName("is_primary") val isPrimary: Boolean = false,
    val visibility: String = "",
    @SerialName("moderation_status") val moderationStatus: String = "",
    @SerialName("moderation_reason") val moderationReason: String? = null,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class AttachPhotoRequest(
    @SerialName("media_id") val mediaId: String,
    @SerialName("sort_order") val sortOrder: Int? = null,
    @SerialName("is_primary") val isPrimary: Boolean? = null,
)

@Serializable
data class UpdatePhotoRequest(
    @SerialName("sort_order") val sortOrder: Int? = null,
    @SerialName("is_primary") val isPrimary: Boolean? = null,
)

// ── Prompts, no fixture ─────────────────────────────────────────────────────

@Serializable
data class PromptCatalogItemDto(val id: Int, val question: String = "")

@Serializable
data class PromptAnswerDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("prompt_id") val promptId: Int = 0,
    val answer: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class PromptAnswerRequest(val answer: String)

// ── Selfie liveness (D5); only the consent refusal has a fixture ────────────

@Serializable
data class SelfieChallengeDto(
    @SerialName("challenge_id") val challengeId: String = "",
    /** Always `blink_twice` today. */
    val instruction: String = "",
    @SerialName("max_duration_ms") val maxDurationMs: Int = 0,
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class SelfieSubmitRequest(
    @SerialName("challenge_id") val challengeId: String,
    @SerialName("video_media_id") val videoMediaId: String,
)

@Serializable
data class SelfieResultDto(
    /** passed | failed | pending_review */
    val status: String = "",
    val passed: Boolean = false,
    val reason: String? = null,
    @SerialName("trust_tier") val trustTier: String? = null,
    @SerialName("profile_status") val profileStatus: String? = null,
    @SerialName("attempts_remaining") val attemptsRemaining: Int = 0,
)

/**
 * `GET /verification/status` — where the face check stands, from the SERVER
 * rather than inferred from the last verdict the app happened to see.
 */
@Serializable
data class VerificationStatusDto(
    val selfie: SelfieStatusDto = SelfieStatusDto(),
    @SerialName("trust_tier") val trustTier: String = "",
    val verified: Boolean = false,
    @SerialName("profile_status") val profileStatus: String = "",
    /** submit_selfie | wait_for_review | retry_tomorrow | none */
    @SerialName("next_step") val nextStep: String = "",
)

@Serializable
data class SelfieStatusDto(
    /** none | pending | review | passed | failed */
    val state: String = "",
    @SerialName("attempts_left_today") val attemptsLeftToday: Int = 0,
    @SerialName("attempts_per_day") val attemptsPerDay: Int = 0,
    @SerialName("window_hours") val windowHours: Int = 0,
)

// ── Pulse (D3/D7; fixtures: pulse_today, pulse_explain, pulse_pass) ─────────

/**
 * `GET /pulse/today` — `{data, meta}` with `cohort_gated` in [PulseMetaDto].
 *
 * The server ALSO keeps `cohort_gated` at the top level (omitted when false)
 * for the shipped app, and the older shape carried it only there, so [gated]
 * reads whichever of the two says yes. Nothing else reads the raw fields.
 */
@Serializable
data class PulseTodayDto(
    val data: List<PulseCardDto> = emptyList(),
    val meta: PulseMetaDto? = null,
    @SerialName("cohort_gated") val cohortGated: Boolean = false,
) {
    /** Pulse is closed for this cohort: an empty deck that is NOT "all caught up". */
    val gated: Boolean get() = cohortGated || meta?.cohortGated == true
}

@Serializable
data class PulseMetaDto(
    @SerialName("generated_at") val generatedAt: String = "",
    val size: Int = 0,
    @SerialName("cohort_gated") val cohortGated: Boolean = false,
    @SerialName("request_id") val requestId: String? = null,
)

@Serializable
data class PulseCardDto(
    @SerialName("candidate_id") val candidateId: String,
    val score: Double = 0.0,
    @SerialName("match_reasons") val matchReasons: List<MatchReasonDto> = emptyList(),
    val profile: PulseProfileDto,
    val echoes: PulseEchoesDto? = null,
)

@Serializable
data class MatchReasonDto(val kind: String = "", val summary: String = "")

@Serializable
data class PulseProfileDto(
    @SerialName("user_id") val userId: String,
    @SerialName("first_name") val firstName: String = "",
    val age: Int = 0,
    val intent: String = "",
    val city: String = "",
    /** lt_5_km | km_5_10 | km_10_25 | gt_25_km — rendered through DistanceBucket only. */
    @SerialName("distance_bucket") val distanceBucket: String? = null,
    /** The server's own label. Never rendered: the app maps the bucket code itself. */
    @SerialName("distance_label") val distanceLabel: String? = null,
    @SerialName("primary_photo_url") val primaryPhotoUrl: String = "",
    @SerialName("primary_photo_blurred") val primaryPhotoBlurred: Boolean = false,
    @SerialName("tune_summary") val tuneSummary: Map<String, JsonElement> = emptyMap(),
    @SerialName("trust_tier") val trustTier: String = "",
    @SerialName("last_active_bucket") val lastActiveBucket: String? = null,
    @SerialName("last_active_label") val lastActiveLabel: String? = null,
    /** The pre-match detail block: the deck is where someone decides to spark. */
    val detail: ProfileDetailDto? = null,
)

@Serializable
data class PulseEchoesDto(
    @SerialName("top_qa_answer_id") val topQaAnswerId: String? = null,
    @SerialName("top_reel_id") val topReelId: String? = null,
    @SerialName("top_community") val topCommunity: String? = null,
    @SerialName("recent_post_id") val recentPostId: String? = null,
)

@Serializable
data class ExplainDto(
    val reasons: List<ExplainReasonDto> = emptyList(),
    @SerialName("distance_bucket") val distanceBucket: String? = null,
    @SerialName("distance_label") val distanceLabel: String? = null,
    @SerialName("is_promoted") val isPromoted: Boolean = false,
)

@Serializable
data class ExplainReasonDto(val kind: String = "", val detail: String = "")

@Serializable
data class PassRequest(val reason: String? = null)

@Serializable
data class PassDto(
    val passed: Boolean = false,
    @SerialName("candidate_id") val candidateId: String = "",
    @SerialName("cooldown_until") val cooldownUntil: String = "",
)

// ── Sparks (fixtures: decline 200, create 404/429) ──────────────────────────

@Serializable
data class SparkRequest(
    @SerialName("to_user_id") val toUserId: String,
    @SerialName("target_kind") val targetKind: String,
    @SerialName("target_ref") val targetRef: String,
    val note: String? = null,
)

@Serializable
data class SparkDto(
    val id: String = "",
    @SerialName("from_user_id") val fromUserId: String = "",
    @SerialName("to_user_id") val toUserId: String = "",
    @SerialName("target_kind") val targetKind: String = "",
    @SerialName("target_ref") val targetRef: String = "",
    val note: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    /** The SENDER, on `GET /sparks/incoming`. Absent on a spark the app just created. */
    val person: DatingPersonDto? = null,
)

/** `match_id` and `matched` are present only when a mutual match formed. */
@Serializable
data class SparkCreatedDto(
    val spark: SparkDto? = null,
    @SerialName("match_id") val matchId: String? = null,
    val matched: Boolean = false,
)

@Serializable
data class SparkDeclineDto(
    val declined: Boolean = false,
    @SerialName("spark_id") val sparkId: String = "",
)

// ── Stash, no fixture ───────────────────────────────────────────────────────

@Serializable
data class StashRequest(@SerialName("candidate_id") val candidateId: String)

@Serializable
data class StashDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("candidate_id") val candidateId: String = "",
    @SerialName("stashed_at") val stashedAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("reactivation_signal") val reactivationSignal: String? = null,
)

@Serializable
data class RemovedDto(val removed: Boolean = false)

// ── Matches (fixture: matches_get_200) ──────────────────────────────────────

@Serializable
data class MatchDto(
    val id: String,
    @SerialName("user_a") val userA: String,
    @SerialName("user_b") val userB: String,
    /** matched | conversing | quiet | expired | closed */
    val status: String = "",
    @SerialName("conversation_id") val conversationId: String? = null,
    @SerialName("spark_target") val sparkTarget: SparkTargetDto? = null,
    @SerialName("matched_at") val matchedAt: String = "",
    @SerialName("first_message_at") val firstMessageAt: String? = null,
    @SerialName("last_message_at") val lastMessageAt: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    @SerialName("closed_by") val closedBy: String? = null,
    /** The OTHER participant, as the server resolved them for this viewer. */
    val person: DatingPersonDto? = null,
)

@Serializable
data class SparkTargetDto(
    @SerialName("target_kind") val targetKind: String = "",
    @SerialName("target_ref") val targetRef: String = "",
)

@Serializable
data class ClosedDto(val closed: Boolean = false)

// ── Safety (D8), no fixtures ────────────────────────────────────────────────

@Serializable
data class BlockRequest(@SerialName("target_user_id") val targetUserId: String)

@Serializable
data class BlockedDto(val blocked: Boolean = false)

/** `GET /blocks` — who I have blocked. A compact person with NO photo. */
@Serializable
data class BlocksDto(val items: List<BlockedPersonDto> = emptyList())

@Serializable
data class BlockedPersonDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("first_name") val firstName: String = "",
    val age: Int = 0,
    @SerialName("blocked_at") val blockedAt: String = "",
)

/** `DELETE /blocks/:userId` — idempotent; `removed` is false when there was nothing to lift. */
@Serializable
data class UnblockedDto(
    val unblocked: Boolean = false,
    val removed: Boolean = false,
)

@Serializable
data class ReportRequest(
    @SerialName("target_id") val targetId: String,
    val reason: String,
    val details: String? = null,
    val evidence: ReportEvidenceDto? = null,
)

@Serializable
data class ReportEvidenceDto(
    @SerialName("photo_ids") val photoIds: List<String>? = null,
    @SerialName("message_ids") val messageIds: List<String>? = null,
    @SerialName("spark_ids") val sparkIds: List<String>? = null,
)

/** `ReportResult` embeds the report, so its fields sit beside `blocked`. */
@Serializable
data class ReportResultDto(
    val id: String = "",
    @SerialName("reporter_id") val reporterId: String = "",
    @SerialName("target_id") val targetId: String = "",
    val category: String = "",
    val reason: String = "",
    val details: String? = null,
    val status: String = "",
    val evidence: ReportEvidenceDto? = null,
    @SerialName("auto_blocked") val autoBlocked: Boolean = false,
    @SerialName("reporter_anonymised") val reporterAnonymised: Boolean = false,
    val blocked: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class PanicRequest(
    val latitude: Double? = null,
    val longitude: Double? = null,
)

@Serializable
data class PanicDto(
    val recorded: Boolean = false,
    @SerialName("incident_id") val incidentId: String = "",
    val status: String = "",
    val deduplicated: Boolean = false,
)

@Serializable
data class TrustedContactsDto(
    val items: List<TrustedContactDto> = emptyList(),
    val max: Int = 3,
)

@Serializable
data class TrustedContactDto(
    @SerialName("contact_id") val contactId: String = "",
    @SerialName("share_location_on_panic") val shareLocationOnPanic: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    /**
     * Who the contact is, on `GET /safety/trusted-contacts`. The field is always
     * present there and is NULL when that profile was deleted or purged — such a
     * row still lists, unnamed, so it can be removed. Absent on the PUT.
     */
    val person: DatingPersonDto? = null,
)

@Serializable
data class TrustedContactRequest(
    @SerialName("share_location_on_panic") val shareLocationOnPanic: Boolean? = null,
)

@Serializable
data class ShareLocationRequest(
    @SerialName("recipient_id") val recipientId: String,
    @SerialName("duration_minutes") val durationMinutes: Int,
    val latitude: Double,
    val longitude: Double,
)

@Serializable
data class ShareLocationDto(
    @SerialName("share_id") val shareId: String = "",
    @SerialName("recipient_id") val recipientId: String = "",
    @SerialName("recipient_kind") val recipientKind: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class StopShareDto(
    val stopped: Boolean = false,
    @SerialName("share_id") val shareId: String = "",
    @SerialName("stopped_at") val stoppedAt: String = "",
)

/**
 * `GET /safety/share-location` — the shares I am sending, so Stop survives a
 * restart. NO coordinates: this is the sharer's own list, not a location read.
 */
@Serializable
data class MyLocationSharesDto(val items: List<MyLocationShareDto> = emptyList())

@Serializable
data class MyLocationShareDto(
    @SerialName("share_id") val shareId: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("recipient_id") val recipientId: String = "",
    @SerialName("recipient_kind") val recipientKind: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
    /** Who it goes to. Absent when the server cannot resolve a card. */
    val recipient: DatingPersonDto? = null,
)

/**
 * `GET /safety/shared-locations` — shares sent TO me. Also carries no
 * coordinates: those come from the single read by `share_id`.
 */
@Serializable
data class SharedWithMeDto(val items: List<SharedWithMeItemDto> = emptyList())

@Serializable
data class SharedWithMeItemDto(
    @SerialName("share_id") val shareId: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("recipient_id") val recipientId: String = "",
    @SerialName("recipient_kind") val recipientKind: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
    /** The SHARER. */
    val person: DatingPersonDto? = null,
)

@Serializable
data class SharedLocationDto(
    @SerialName("share_id") val shareId: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("recipient_id") val recipientId: String = "",
    @SerialName("recipient_kind") val recipientKind: String = "",
    val latitude: Double? = null,
    val longitude: Double? = null,
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("stopped_at") val stoppedAt: String? = null,
    @SerialName("created_at") val createdAt: String = "",
)

// ── Data rights, no fixture ─────────────────────────────────────────────────

@Serializable
data class DataExportDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("requested_at") val requestedAt: String = "",
    @SerialName("completed_at") val completedAt: String? = null,
    @SerialName("download_url") val downloadUrl: String? = null,
    @SerialName("download_expires_at") val downloadExpiresAt: String? = null,
    /** pending | processing | ready | failed | expired */
    val status: String = "",
)

// ── Premium (P2; fixtures: catalogue, purchase, payment, me) ────────────────

@Serializable
data class PremiumCatalogueDto(val products: List<PremiumProductDto> = emptyList())

@Serializable
data class PremiumProductDto(
    val id: String,
    val kind: String = "",
    val name: String = "",
    @SerialName("amount_minor") val amountMinor: Long = 0,
    val currency: String = "",
    @SerialName("duration_days") val durationDays: Int? = null,
    val features: List<String> = emptyList(),
)

/** There is deliberately no amount, price or currency: the server refuses them (CLIENT_PRICE_REFUSED). */
@Serializable
data class PremiumPurchaseRequest(
    val product: String,
    @SerialName("idempotency_key") val idempotencyKey: String,
)

@Serializable
data class PremiumPurchaseResultDto(
    val purchase: PremiumPurchaseDto,
    /** Present only while the purchase can still be paid. */
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
)

@Serializable
data class PremiumPurchaseDto(
    val id: String,
    val product: String = "",
    @SerialName("amount_minor") val amountMinor: Long = 0,
    val currency: String = "",
    val method: String = "",
    @SerialName("idempotency_key") val idempotencyKey: String = "",
    @SerialName("payment_intent_id") val paymentIntentId: String? = null,
    @SerialName("provider_ref") val providerRef: String? = null,
    /** created | confirming | paid | failed | refunded | partially_refunded */
    val status: String = "",
    @SerialName("refunded_minor") val refundedMinor: Long = 0,
    @SerialName("pass_expires_at") val passExpiresAt: String? = null,
    @SerialName("paid_at") val paidAt: String? = null,
    @SerialName("failed_at") val failedAt: String? = null,
    @SerialName("refunded_at") val refundedAt: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class PremiumPaymentDto(
    @SerialName("purchase_id") val purchaseId: String,
    val product: String = "",
    /** confirming | paid | failed */
    val status: String = "",
    @SerialName("amount_minor") val amountMinor: Long = 0,
    val currency: String = "",
    /** null | partially_refunded | refunded */
    @SerialName("refund_status") val refundStatus: String? = null,
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class PremiumMeDto(
    @SerialName("is_premium") val isPremium: Boolean = false,
    val pass: PremiumPassDto? = null,
    val entitlements: List<PremiumEntitlementDto> = emptyList(),
    @SerialName("boost_balance") val boostBalance: Int = 0,
)

@Serializable
data class PremiumPassDto(
    val product: String = "",
    val active: Boolean = false,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class PremiumEntitlementDto(
    val feature: String = "",
    val active: Boolean = false,
    @SerialName("expires_at") val expiresAt: String? = null,
)

// ── Error details (fixtures) ────────────────────────────────────────────────

@Serializable
data class DatingErrorEnvelopeDto(
    val error: DatingErrorBodyDto? = null,
    /** Present on every dating-service error; ABSENT on the gateway's pilot-allowlist 404. */
    val meta: JsonElement? = null,
)

@Serializable
data class DatingErrorBodyDto(
    val code: String = "",
    val message: String = "",
    val details: JsonElement? = null,
)

@Serializable
data class ConsentRequiredDetailsDto(
    @SerialName("consent_type") val consentType: String,
    @SerialName("policy_version") val policyVersion: String? = null,
)

@Serializable
data class LocationRateLimitDetailsDto(
    @SerialName("max_changes_per_day") val maxChangesPerDay: Int = 0,
    @SerialName("min_interval_minutes") val minIntervalMinutes: Int = 0,
    @SerialName("window_hours") val windowHours: Int = 0,
)

@Serializable
data class RateLimitDetailsDto(
    val limit: Int = 0,
    @SerialName("window_hours") val windowHours: Int = 0,
)

@Serializable
data class AllowedDetailsDto(val allowed: List<String> = emptyList())

@Serializable
data class MovedDetailsDto(
    val catalogue: String? = null,
    @SerialName("moved_to") val movedTo: String = "",
)
