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
    /** A city NAME only — never coordinates. Omitted when the person set none. */
    val city: String = "",
    /** casual | serious | marriage — rendered through [DatingIntent] only. */
    val intent: String = "",
    /**
     * How recently they were here, as a COARSE bucket: today | this_week | a_while_ago.
     *
     * Both this and [lastActiveLabel] are ABSENT when the owner hides last active
     * — the default for a new profile — and the app renders nothing at all then.
     * There is no fallback string: hidden means hidden.
     */
    @SerialName("last_active_bucket") val lastActiveBucket: String? = null,
    @SerialName("last_active_label") val lastActiveLabel: String? = null,
    /**
     * Mechanic M8: they are on a trip, and [city] and [distanceBucket] are the
     * destination's. Omitted when false.
     */
    val travelling: Boolean = false,
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
    // Mechanic M6: interests and the lifestyle basics, as option CODES. The
    // labels come from `GET /profile/options`; an unknown code renders nothing.
    // Go omits each one while unset, so every field defaults to empty.
    val interests: List<String> = emptyList(),
    /** 0 = not set. */
    @SerialName("height_cm") val heightCm: Int = 0,
    val drinking: String = "",
    val smoking: String = "",
    val exercise: String = "",
    val diet: String = "",
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
    /** Mechanic M6: interest codes. Omitted while empty. */
    val interests: List<String>? = null,
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
    // Mechanic M6 ("About me"): option CODES only. A list is written whole, so
    // `[]` clears it; a basic set to "" is cleared ("Prefer not to say").
    val interests: List<String>? = null,
    @SerialName("language_prefs") val languagePrefs: List<String>? = null,
    val drinking: String? = null,
    val smoking: String? = null,
    val exercise: String? = null,
    val diet: String? = null,
)

@Serializable
data class PauseRequest(val paused: Boolean)

@Serializable
data class DeleteProfileRequest(val reason: String? = null)

@Serializable
data class StatusDto(val status: String = "")

// ── Preferences (fixtures: preferences_get_200, preferences_*_filters) ──────

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
    /**
     * Mechanic M6: the distance filter as a bucket code. Present only while the
     * server's filters flag is on, together with [passFilters].
     */
    @SerialName("distance_bucket") val distanceBucket: String? = null,
    /**
     * Mechanic M6: the filters that come with a pass. ABSENT means the server's
     * filters flag is off — the app then keeps its older screens.
     */
    @SerialName("pass_filters") val passFilters: PassFiltersDto? = null,
)

/**
 * `pass_filters` on `GET /preferences`. [active]: the caller holds a pass, so
 * these apply to the deck; without one they are stored but not applied.
 * Go omits the two heights while unset and sends `[]` for an empty list.
 */
@Serializable
data class PassFiltersDto(
    val active: Boolean = false,
    @SerialName("verified_only") val verifiedOnly: Boolean = false,
    @SerialName("min_height_cm") val minHeightCm: Int? = null,
    @SerialName("max_height_cm") val maxHeightCm: Int? = null,
    val languages: List<String> = emptyList(),
    val drinking: List<String> = emptyList(),
    val smoking: List<String> = emptyList(),
    val exercise: List<String> = emptyList(),
    val diet: List<String> = emptyList(),
)

/** `PUT /preferences` — a partial write: null is left out of the body and stays as it is. */
@Serializable
data class PreferencesRequest(
    @SerialName("min_age") val minAge: Int? = null,
    @SerialName("max_age") val maxAge: Int? = null,
    @SerialName("distance_km") val distanceKm: Int? = null,
    @SerialName("interested_in_gender") val interestedInGender: String? = null,
    /** `[]` clears the intent filter. */
    @SerialName("intent_filter") val intentFilter: List<String>? = null,
    /** Mechanic M6, flag on only: replaces [distanceKm]. */
    @SerialName("distance_bucket") val distanceBucket: String? = null,
    /** Mechanic M6, flag on only: the WHOLE pass filter set, replaced as one. */
    @SerialName("pass_filters") val passFilters: PassFiltersRequest? = null,
)

/**
 * `pass_filters` on `PUT /preferences`. Every member is always sent (no
 * defaults), because the server replaces the set as one. Setting any of them
 * without a pass is `403 FILTERS_REQUIRE_PASS`; an all-empty set clears them
 * and is always allowed.
 */
@Serializable
data class PassFiltersRequest(
    @SerialName("verified_only") val verifiedOnly: Boolean,
    @SerialName("min_height_cm") val minHeightCm: Int?,
    @SerialName("max_height_cm") val maxHeightCm: Int?,
    val languages: List<String>,
    val drinking: List<String>,
    val smoking: List<String>,
    val exercise: List<String>,
    val diet: List<String>,
)

// ── Profile options (mechanic M6; fixture: profile_options_get_200) ─────────

/** One choice: the stable code that goes on the wire, and the server's label that goes on screen. */
@Serializable
data class OptionDto(val code: String = "", val label: String = "")

@Serializable
data class OptionRangeDto(val min: Int = 0, val max: Int = 0)

/**
 * `GET /profile/options`: every list the new profile fields and filters draw
 * from, with OUR labels. The app shows these labels and never a code.
 */
@Serializable
data class ProfileOptionsDto(
    val interests: List<OptionDto> = emptyList(),
    @SerialName("max_interests") val maxInterests: Int = 0,
    val languages: List<OptionDto> = emptyList(),
    @SerialName("max_languages") val maxLanguages: Int = 0,
    @SerialName("height_cm") val heightCm: OptionRangeDto = OptionRangeDto(),
    val drinking: List<OptionDto> = emptyList(),
    val smoking: List<OptionDto> = emptyList(),
    val exercise: List<OptionDto> = emptyList(),
    val diet: List<OptionDto> = emptyList(),
    @SerialName("distance_buckets") val distanceBuckets: List<OptionDto> = emptyList(),
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
    // Mechanic M1 (the refilling deck). All three are ABSENT while the server's
    // DATING_DECK_REFILL_ENABLED is off, and then the deck behaves as before.
    /** The caller's card allowance per rolling 24 hours. 0 = the server sent none. */
    @SerialName("daily_limit") val dailyLimit: Int = 0,
    /** Cards left. Go omits 0, so "daily_limit > 0 and this absent" means none left. */
    @SerialName("remaining_today") val remainingToday: Int = 0,
    /** RFC 3339: when the allowance starts to come back. Absent while nothing is used. */
    @SerialName("resets_at") val resetsAt: String? = null,
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
    /**
     * Mechanic M8: on a trip; [city] and [distanceBucket] are the destination's.
     * Omitted when false. Their home location is never sent.
     */
    val travelling: Boolean = false,
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

/**
 * [source] names the surface the pass came from (mechanic M7): null is the
 * deck, and is left out of the body, so a deck pass is unchanged on the wire.
 */
@Serializable
data class PassRequest(val reason: String? = null, val source: String? = null)

/**
 * Where a spark or a pass was made (mechanic M7). Only a deck action spends a
 * deck card; the deck is the server's default, so [wire] is null for it and the
 * field stays out of the body. An unknown value is `400 INVALID_SOURCE`.
 */
enum class ActionSource(val wire: String?) {
    DECK(null),
    PICKS("picks"),
    LIKED_YOU("liked_you"),
    PROFILE("profile"),
}

// ── Daily picks (mechanic M7; fixtures picks_get_*) ────────────────────────

/**
 * `GET /picks` — NOT the envelope, the deck's own `{data, meta}` shape: up to
 * ten cards in the deck's card shape, the same all day, refreshed at local
 * midnight.
 */
@Serializable
data class PicksDto(
    val data: List<PulseCardDto> = emptyList(),
    val meta: PicksMetaDto? = null,
)

@Serializable
data class PicksMetaDto(
    /** The viewer's local date, YYYY-MM-DD. */
    val date: String = "",
    /** The IANA zone the day was cut in: the one sent, or the server's default. */
    val timezone: String = "",
    /** RFC 3339: the next local midnight, when a new set is chosen. */
    @SerialName("resets_at") val resetsAt: String? = null,
    val size: Int = 0,
)

// ── Travel mode (mechanic M8; fixtures travel_*) ───────────────────────────

/** `GET`, `PUT` and `DELETE /travel` all answer this. */
@Serializable
data class TravelDto(
    /** The trip in effect; omitted when there is none. */
    val active: TravelTripDto? = null,
    /** The caller holds a pass and may start a trip. Omitted when false. */
    val available: Boolean = false,
    val cities: List<TravelCityDto> = emptyList(),
    /** The longest trip in days. 0 = the server did not say. */
    @SerialName("max_days") val maxDays: Int = 0,
)

@Serializable
data class TravelTripDto(
    val city: TravelCityDto = TravelCityDto(),
    @SerialName("starts_at") val startsAt: String = "",
    @SerialName("ends_at") val endsAt: String = "",
)

@Serializable
data class TravelCityDto(val code: String = "", val label: String = "")

/** `PUT /travel`: a city code from [TravelDto.cities] and 1 to `max_days` days. */
@Serializable
data class TravelRequest(val city: String, val days: Int)

@Serializable
data class PassDto(
    val passed: Boolean = false,
    @SerialName("candidate_id") val candidateId: String = "",
    @SerialName("cooldown_until") val cooldownUntil: String = "",
)

// ── Pulse mechanics: allowances (M10), undo a pass (M2) ─────────────────────

/**
 * One daily allowance (`service.Allowance`). Go omits zero values, so every
 * field defaults: [remainingToday] absent means NONE left, and [unlimited]
 * true (a pass holder) omits the counts altogether. [resetsAt] is RFC 3339,
 * absent while nothing in the window is used.
 */
@Serializable
data class AllowanceDto(
    val unlimited: Boolean = false,
    @SerialName("daily_limit") val dailyLimit: Int = 0,
    @SerialName("remaining_today") val remainingToday: Int = 0,
    @SerialName("resets_at") val resetsAt: String? = null,
)

/** The Super Spark allowance: the daily one plus what packs bought, which is spent once the daily one is used. */
@Serializable
data class SuperSparkAllowanceDto(
    val unlimited: Boolean = false,
    @SerialName("daily_limit") val dailyLimit: Int = 0,
    @SerialName("remaining_today") val remainingToday: Int = 0,
    @SerialName("resets_at") val resetsAt: String? = null,
    /** Purchased Super Sparks left. Omitted at 0. */
    @SerialName("purchased_balance") val purchasedBalance: Int = 0,
)

/**
 * `GET /allowances`. A mechanic whose server flag is off is ABSENT (null
 * here): that is how the app learns whether to offer undo or Super Spark at
 * all. Sparks are always present.
 */
@Serializable
data class AllowancesDto(
    val sparks: AllowanceDto = AllowanceDto(),
    val deck: AllowanceDto? = null,
    val rewind: AllowanceDto? = null,
    @SerialName("super_spark") val superSpark: SuperSparkAllowanceDto? = null,
)

/**
 * `POST /pulse/rewind`: the last pass is undone. [card] is the person's deck
 * card again, absent when the server could not build it — the app then
 * refetches the deck instead.
 */
@Serializable
data class RewindDto(
    val rewound: Boolean = false,
    @SerialName("candidate_id") val candidateId: String = "",
    val card: PulseCardDto? = null,
    val allowance: AllowanceDto = AllowanceDto(),
)

// ── Sparks (fixtures: decline 200, create 404/429) ──────────────────────────

@Serializable
data class SparkRequest(
    @SerialName("to_user_id") val toUserId: String,
    @SerialName("target_kind") val targetKind: String,
    @SerialName("target_ref") val targetRef: String,
    val note: String? = null,
    /** True sends a Super Spark (mechanic M3). Null is left out of the body, so an ordinary spark is unchanged. */
    @SerialName("super") val superSpark: Boolean? = null,
    /** Mechanic M7: the surface, from [ActionSource.wire]. Null (the deck) is left out of the body. */
    val source: String? = null,
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
    /** A Super Spark. Omitted when false; incoming Super Sparks are listed first by the server. */
    @SerialName("super") val superSpark: Boolean = false,
    /**
     * The SENDER, on `GET /sparks/incoming`. Absent on a spark the app just
     * created, and on a LOCKED incoming spark (mechanic M4).
     */
    val person: DatingPersonDto? = null,
    /**
     * Locked incoming sparks only (mechanic M4, the gate on and no pass): the
     * blurred-image route `/v1/dating/liked-you/<id>/photo`. Such a row carries
     * no `from_user_id`, `person` or `note`.
     */
    @SerialName("photo_url") val photoUrl: String = "",
    /** True on an incoming spark the caller may not see the sender of. Omitted otherwise. */
    val locked: Boolean = false,
)

// ── Who liked you (mechanic M4; fixtures liked_you_get_200_locked/_unlocked) ─

/**
 * One card of `GET /liked-you`.
 *
 * LOCKED (the response's `unlocked` false): only [sparkId], [superSpark],
 * [createdAt] and [photoUrl] — the server-blurred route
 * `/v1/dating/liked-you/<spark_id>/photo`. UNLOCKED: [person] and [note] too,
 * and [photoUrl] is the person's own photo route. The app never shows a person
 * from a locked response, even if one arrived.
 */
@Serializable
data class LikedYouItemDto(
    @SerialName("spark_id") val sparkId: String = "",
    /** A Super Spark. Omitted when false; the server lists these first. */
    @SerialName("super") val superSpark: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("photo_url") val photoUrl: String = "",
    val person: DatingPersonDto? = null,
    val note: String? = null,
)

/** `GET /liked-you`. [total] counts every visible incoming spark across pages. */
@Serializable
data class LikedYouDto(
    val total: Int = 0,
    val unlocked: Boolean = false,
    val items: List<LikedYouItemDto> = emptyList(),
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
    /**
     * Mechanic M5: present only while the match waits for its first message
     * under the first-move rule. Absent means an ordinary match.
     */
    @SerialName("first_move") val firstMove: MatchFirstMoveDto? = null,
)

/**
 * The caller's view of a first-move match (`service.FirstMoveView`).
 *
 * [youMoveFirst] true: the caller writes first. False: the other person does,
 * and chat refuses the caller's own first message (`403 FIRST_MOVE_PENDING`);
 * they may answer one of [openingQuestions] instead, or use the free extend
 * while [canExtend]. Go omits the empty list and a nil deadline.
 */
@Serializable
data class MatchFirstMoveDto(
    @SerialName("you_move_first") val youMoveFirst: Boolean = false,
    /** RFC 3339: when the match ends if nobody has written. */
    val deadline: String? = null,
    @SerialName("opening_questions") val openingQuestions: List<OpeningQuestionDto> = emptyList(),
    @SerialName("can_extend") val canExtend: Boolean = false,
)

/** One opening question a first mover wrote. */
@Serializable
data class OpeningQuestionDto(
    val id: String = "",
    val text: String = "",
)

// ── First move (mechanic M5; fixtures first_move_*, match_opening_answer_*, match_extend_*) ─

/** `GET`/`PUT /first-move`. The limits are the server's; 0 means it sent none. */
@Serializable
data class FirstMoveSettingsDto(
    val enabled: Boolean = false,
    val questions: List<OpeningQuestionDto> = emptyList(),
    @SerialName("max_questions") val maxQuestions: Int = 0,
    @SerialName("max_length") val maxLength: Int = 0,
)

/**
 * `PUT /first-move`. A null field is left out of the body and the server
 * leaves it unchanged; `questions = []` removes every question.
 */
@Serializable
data class FirstMoveRequest(
    val enabled: Boolean? = null,
    val questions: List<String>? = null,
)

@Serializable
data class OpeningAnswerRequest(
    @SerialName("question_id") val questionId: String,
    val answer: String,
)

/** `POST /matches/:id/opening-answer` — the answer is now the chat's first message. */
@Serializable
data class OpeningAnswerDto(
    val sent: Boolean = false,
    @SerialName("conversation_id") val conversationId: String? = null,
)

/**
 * `POST /matches/:id/extend`. The free first-move extend sends [extraHours]
 * and [free]; the premium path sends [extraDays] too. Go omits zero values.
 */
@Serializable
data class ExtendDto(
    val extended: Boolean = false,
    @SerialName("extra_hours") val extraHours: Int = 0,
    @SerialName("extra_days") val extraDays: Int = 0,
    @SerialName("expires_at") val expiresAt: String? = null,
    val free: Boolean = false,
)

/** `details` of `OPENING_QUESTION_INVALID` and `OPENING_ANSWER_INVALID`. */
@Serializable
data class MaxLengthDetailsDto(@SerialName("max_length") val maxLength: Int = 0)

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
    /** How many Super Sparks a `super_spark` pack adds. Omitted (0) for every other kind. */
    val quantity: Int = 0,
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
    /** Purchased Super Sparks left. Omitted at 0. */
    @SerialName("super_spark_balance") val superSparkBalance: Int = 0,
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

/**
 * `details` of an allowance refusal: `SPARK_RATE_LIMITED`, `REWIND_LIMIT_REACHED`
 * and `SUPER_SPARK_LIMIT_REACHED` all carry the same three keys.
 */
@Serializable
data class RateLimitDetailsDto(
    val limit: Int = 0,
    @SerialName("window_hours") val windowHours: Int = 0,
    /** RFC 3339: when the allowance starts to come back. Preferred over the window. */
    @SerialName("resets_at") val resetsAt: String? = null,
)

/** `details` of a bounds refusal (`INVALID_AGE_RANGE`, `PROMPT_ANSWER_TOO_LONG`, …). Either end may be absent. */
@Serializable
data class RangeDetailsDto(val min: Int = 0, val max: Int = 0)

/** `details` of `UNKNOWN_PROMPT`: the catalogue's prompt ids. */
@Serializable
data class AllowedIdsDetailsDto(val allowed: List<Int> = emptyList())

/** `details` of `ONBOARDING_INCOMPLETE`: where the profile stands. */
@Serializable
data class OnboardingIncompleteDetailsDto(val status: String = "", val step: String = "")

@Serializable
data class AllowedDetailsDto(val allowed: List<String> = emptyList())

/**
 * `details` of a refused M6 field (`INVALID_INTEREST`, `TOO_MANY_LANGUAGE`,
 * `INVALID_HEIGHT`, `INVALID_LIFESTYLE`, …): [field] names the picker, and the
 * rest is whichever of the allowed codes or the limits the refusal carries.
 */
@Serializable
data class FieldRefusalDetailsDto(
    val field: String = "",
    val allowed: List<String> = emptyList(),
    val min: Int = 0,
    val max: Int = 0,
)

@Serializable
data class MovedDetailsDto(
    val catalogue: String? = null,
    @SerialName("moved_to") val movedTo: String = "",
)
