package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.home.AllowanceUi
import com.us.android.feature.dating.home.DeckCopy
import com.us.android.feature.dating.home.DeckUi
import com.us.android.feature.dating.home.FirstMoveCopy
import com.us.android.feature.dating.network.ExtendDto
import com.us.android.feature.dating.network.FirstMoveSettingsDto
import com.us.android.feature.dating.network.OpeningAnswerDto
import com.us.android.feature.dating.home.onto
import com.us.android.feature.dating.home.toUi
import com.us.android.feature.dating.home.PicksCopy
import com.us.android.feature.dating.home.parseInstant
import com.us.android.feature.dating.home.toCardUi
import com.us.android.feature.dating.network.PicksDto
import com.us.android.feature.dating.network.TravelCityDto
import com.us.android.feature.dating.network.TravelDto
import com.us.android.feature.dating.travel.TravelCopy
import com.us.android.feature.dating.travel.TravelRules
import java.time.Instant
import java.time.ZoneId
import com.us.android.feature.dating.network.AllowanceDto
import com.us.android.feature.dating.network.AllowancesDto
import com.us.android.feature.dating.network.AllowedDetailsDto
import com.us.android.feature.dating.network.RewindDto
import com.us.android.feature.dating.premium.packLabel
import com.us.android.feature.dating.network.AllowedIdsDetailsDto
import com.us.android.feature.dating.network.OnboardingIncompleteDetailsDto
import com.us.android.feature.dating.network.RangeDetailsDto
import com.us.android.feature.dating.network.BlockedDto
import com.us.android.feature.dating.network.BlocksDto
import com.us.android.feature.dating.network.ClosedDto
import com.us.android.feature.dating.network.ConsentRequiredDetailsDto
import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.DataExportDto
import com.us.android.feature.dating.network.DatingErrorEnvelopeDto
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.DatingPhotoDto
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.ExplainDto
import com.us.android.feature.dating.network.LocationRateLimitDetailsDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.MovedDetailsDto
import com.us.android.feature.dating.network.MyLocationSharesDto
import com.us.android.feature.dating.network.PanicDto
import com.us.android.feature.dating.network.PassDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PromptAnswerDto
import com.us.android.feature.dating.network.PremiumCatalogueDto
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.PremiumPaymentDto
import com.us.android.feature.dating.network.PremiumPurchaseRequest
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import com.us.android.feature.dating.network.PrivacyDto
import com.us.android.feature.dating.network.PulseTodayDto
import com.us.android.feature.dating.network.RateLimitDetailsDto
import com.us.android.feature.dating.network.ReportResultDto
import com.us.android.feature.dating.network.SelfieChallengeDto
import com.us.android.feature.dating.network.SelfieResultDto
import com.us.android.feature.dating.network.ShareLocationDto
import com.us.android.feature.dating.network.SharedLocationDto
import com.us.android.feature.dating.network.SharedWithMeDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.network.SparkDeclineDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.network.StashDto
import com.us.android.feature.dating.network.StopShareDto
import com.us.android.feature.dating.network.TrustedContactDto
import com.us.android.feature.dating.network.TrustedContactsDto
import com.us.android.feature.dating.network.VerificationStatusDto
import com.us.android.feature.dating.network.LikedYouDto
import com.us.android.feature.dating.network.FieldRefusalDetailsDto
import com.us.android.feature.dating.network.OptionDto
import com.us.android.feature.dating.network.OptionRangeDto
import com.us.android.feature.dating.network.ProfileOptionsDto
import com.us.android.feature.dating.filters.FiltersField
import com.us.android.feature.dating.filters.FiltersRules
import com.us.android.feature.dating.profile.AboutMeField
import com.us.android.feature.dating.profile.LifestyleBasic
import com.us.android.feature.dating.profile.ProfileOptionsUi
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.premium.toReading
import com.us.android.feature.dating.safety.MAX_TRUSTED_CONTACTS
import com.us.android.feature.dating.selfie.SelfieOutcomes
import com.us.android.feature.dating.selfie.SelfieState
import com.us.android.core.payments.PaymentStatusReading
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture from dating-service decodes into its DTO.
 *
 * The fixtures are dating-service's handler-test goldens
 * (internal/http/testdata/contracts, adfeb2fc), copied byte for byte into
 * src/test/resources/contracts. Decoding is STRICT — unknown keys fail — so a
 * key renamed on either side, or a field the server added that the DTO does
 * not declare, fails here rather than defaulting silently in production.
 *
 * [parsers] must name every fixture: adding a fixture without a parser, or a
 * parser for a fixture that does not exist, fails the coverage test. The HTTP
 * status of an error fixture is the three digits in its name.
 */
class DatingContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val production = NetworkModule.provideJson()

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (name: String, raw: String) -> Unit> = mapOf(
        "block_post_200.json" to data(BlockedDto.serializer()) {
            assertThat(it.blocked).isTrue()
        },
        "blocks_get_200.json" to data(BlocksDto.serializer()) {
            val blocked = it.items.single()
            assertThat(blocked.firstName).isEqualTo("Asha")
            assertThat(blocked.age).isEqualTo(30)
            assertThat(blocked.blockedAt).isNotEmpty()
        },
        "consents_get_200.json" to data(ConsentsDto.serializer()) {
            assertThat(it.currentPolicyVersion).isEqualTo("v1.0-2026-04-29")
            assertThat(it.consents.map { c -> c.consentType })
                .containsExactly("sensitive_religion", "sensitive_community", "biometric_selfie", "echoes").inOrder()
            assertThat(it.consents.first().granted).isTrue()
            assertThat(it.consents[1].policyVersion).isNull()
        },
        "consent_put_200_withdrawn.json" to data(ConsentsDto.serializer()) {
            assertThat(it.consents.first { c -> c.consentType == "sensitive_community" }.granted).isFalse()
            assertThat(it.consents.first { c -> c.consentType == "sensitive_community" }.updatedAt).isNotNull()
        },
        "consent_put_400_invalid_type.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_CONSENT_TYPE")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed).hasSize(4)
        },
        "data_export_me_get_200.json" to data(listSerializer(DataExportDto.serializer())) {
            assertThat(it.single().status).isEqualTo("pending")
        },
        "data_export_post_202.json" to data(DataExportDto.serializer()) {
            assertThat(it.status).isEqualTo("pending")
            assertThat(it.downloadUrl).isNull()
        },
        "match_close_post_200.json" to data(ClosedDto.serializer()) {
            assertThat(it.closed).isTrue()
        },
        "match_get_200.json" to data(MatchDto.serializer()) {
            assertThat(it.sparkTarget?.targetKind).isEqualTo("prompt")
            val person = checkNotNull(it.person)
            assertThat(person.firstName).isEqualTo("Asha")
            assertThat(person.age).isEqualTo(30)
            assertThat(person.photoState).isEqualTo("full")
            // Absent unless BOTH sides have a location.
            assertThat(person.distanceBucket).isNull()
        },
        "matches_get_200.json" to data(listSerializer(MatchDto.serializer())) {
            val match = it.single()
            assertThat(match.status).isEqualTo("matched")
            assertThat(match.conversationId).isEqualTo("<conversation>")
            assertThat(match.sparkTarget?.targetKind).isEqualTo("photo")
            assertThat(match.sparkTarget?.targetRef).isEqualTo("0")
            val person = checkNotNull(match.person)
            assertThat(person.userId).isEqualTo("<user_b>")
            assertThat(person.firstName).isEqualTo("Asha")
            assertThat(person.age).isEqualTo(30)
            assertThat(person.primaryPhotoUrl).isEqualTo("/v1/dating/photos/<uuid>/full")
            assertThat(person.photoState).isEqualTo("full")
            assertThat(person.verified).isFalse()
            assertThat(person.city).isEqualTo("Hyderabad")
            assertThat(person.intent).isEqualTo("casual")
            assertThat(person.lastActiveLabel).isNull()
            // The match list stays COMPACT: the server omits detail here on purpose.
            assertThat(person.detail).isNull()
        },
        "panic_post_200.json" to data(PanicDto.serializer()) {
            assertThat(it.recorded).isTrue()
            assertThat(it.status).isEqualTo("open")
            assertThat(it.deduplicated).isFalse()
        },
        "person_get_200.json" to data(DatingPersonDto.serializer()) {
            assertThat(it.firstName).isEqualTo("Asha")
            assertThat(it.age).isEqualTo(30)
            assertThat(it.photoState).isEqualTo("full")
            assertThat(it.trustTier).isEqualTo("phone")
            assertThat(it.verified).isFalse()
            // A city NAME, never a coordinate, and the intent as a CODE.
            assertThat(it.city).isEqualTo("Hyderabad")
            assertThat(it.intent).isEqualTo("casual")
            // The golden hides last active — the default — so BOTH fields are absent.
            assertThat(it.lastActiveBucket).isNull()
            assertThat(it.lastActiveLabel).isNull()
            // The person view decides too, so it carries the detail block.
            assertThat(checkNotNull(it.detail).photos.single().state).isEqualTo("full")
        },
        "photos_get_200.json" to data(listSerializer(DatingPhotoDto.serializer())) {
            // [] rather than null now.
            assertThat(it).isEmpty()
        },
        "photos_post_201.json" to data(DatingPhotoDto.serializer()) {
            assertThat(it.isPrimary).isTrue()
            assertThat(it.moderationStatus).isEqualTo("approved")
            assertThat(it.visibility).isEqualTo("public")
        },
        "preferences_get_200.json" to data(PreferencesDto.serializer()) {
            assertThat(it.interestedInGender).isEqualTo("everyone")
            assertThat(it.distanceKm).isEqualTo(25)
            assertThat(it.minAge).isNull()
        },
        "preferences_put_400_invalid_gender.json" to error { error, name ->
            // Its OWN code now, not a bare 400: the screen matches on this.
            assertThat(refusedCode(error)).isEqualTo("INVALID_INTERESTED_IN_GENDER")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed)
                .containsExactly("woman", "man", "nonbinary", "everyone").inOrder()
            assertThat(DatingCopy.forError(error)).isEqualTo(DatingCopy.INVALID_INTERESTED_IN_GENDER)
        },
        "preferences_put_200.json" to data(PreferencesDto.serializer()) {
            assertThat(it.interestedInGender).isEqualTo("everyone")
            assertThat(it.minAge).isEqualTo(25)
            assertThat(it.maxAge).isEqualTo(35)
            assertThat(it.intentFilter).containsExactly("casual")
        },
        "premium_catalogue_get_200.json" to data(PremiumCatalogueDto.serializer()) {
            assertThat(it.products.map { p -> p.id }).containsExactly("pass_30d", "pass_90d", "pass_365d", "boost").inOrder()
            assertThat(it.products.first().amountMinor).isEqualTo(39_900)
            assertThat(it.products.last().durationDays).isNull()
            assertThat(it.products.last().features).isEmpty()
        },
        "premium_checkout_post_410.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("PREMIUM_SUBSCRIPTIONS_REMOVED")
            assertThat(details(error, MovedDetailsDto.serializer(), name).movedTo).isEqualTo("/v1/dating/premium/purchases")
        },
        "premium_me_get_200.json" to data(PremiumMeDto.serializer()) {
            assertThat(it.isPremium).isTrue()
            assertThat(it.pass?.product).isEqualTo("pass_30d")
            assertThat(it.entitlements.map { e -> e.feature }).containsExactly("match_extend", "daily_boost")
            assertThat(it.boostBalance).isEqualTo(1)
        },
        "premium_payment_get_200_confirming.json" to data(PremiumPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("confirming")
            assertThat(it.refundStatus).isNull()
            assertThat(it.toReading()).isEqualTo(PaymentStatusReading.Confirming)
        },
        "premium_payment_get_200_paid.json" to data(PremiumPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
            assertThat(it.toReading()).isEqualTo(PaymentStatusReading.Paid)
        },
        "premium_payment_get_404_not_owner.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("PURCHASE_NOT_FOUND")
        },
        "premium_purchase_post_200_repeat.json" to data(PremiumPurchaseResultDto.serializer()) {
            assertThat(it.purchase.status).isEqualTo("confirming")
            assertThat(it.clientSession?.get("order_id")).isEqualTo("order_contract_1")
        },
        "premium_purchase_post_201.json" to data(PremiumPurchaseResultDto.serializer()) {
            assertThat(it.purchase.id).isEqualTo("<purchase>")
            assertThat(it.purchase.amountMinor).isEqualTo(39_900)
            assertThat(it.clientSession).containsExactly(
                "provider", "razorpay",
                "order_id", "order_contract_1",
                "key_id", "rzp_test_contract",
                "merchant_display_name", "Momentum Dating",
            )
        },
        "premium_purchase_post_400_client_price.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("CLIENT_PRICE_REFUSED")
        },
        "premium_purchase_post_409_key_reused.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("IDEMPOTENCY_KEY_REUSED")
        },
        "premium_purchase_post_503_unavailable.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("PREMIUM_UNAVAILABLE")
        },
        "privacy_get_200.json" to data(PrivacyDto.serializer()) {
            assertThat(it.hideLastActive).isTrue()
            assertThat(it.approximateLocation).isTrue()
            assertThat(it.echoesConsent).isFalse()
        },
        "profile_get_200.json" to data(DatingProfileDto.serializer()) {
            assertThat(it.firstName).isEqualTo("Asha")
            assertThat(it.profileStatus).isEqualTo("active")
            assertThat(it.trustTier).isEqualTo("phone")
            // Identity owns both; the app never sends them.
            assertThat(it.dobSource).isEqualTo("identity")
            assertThat(it.firstNameSource).isEqualTo("identity")
        },
        "profile_upsert_200.json" to data(DatingProfileDto.serializer()) {
            assertThat(it.bio).isEqualTo("Filter coffee and long walks.")
            assertThat(it.city).isEqualTo("Hyderabad")
            assertThat(it.country).isEqualTo("India")
        },
        "profile_upsert_400_invalid_location.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_LOCATION")
        },
        "profile_upsert_422_consent_required.json" to error { error, name ->
            assertThat(error).isEqualTo(DatingError.ConsentRequired("sensitive_religion", "v1.0-2026-04-29"))
            val raw = strict.decodeFromString(DatingErrorEnvelopeDto.serializer(), fixture(name)).error?.details
            assertThat(strict.decodeFromJsonElement(ConsentRequiredDetailsDto.serializer(), checkNotNull(raw)).consentType)
                .isEqualTo("sensitive_religion")
        },
        "profile_upsert_429_location_change_rate_limited.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("LOCATION_CHANGE_RATE_LIMITED")
            val limits = details(error, LocationRateLimitDetailsDto.serializer(), name)
            assertThat(limits.minIntervalMinutes).isEqualTo(15)
            assertThat(limits.maxChangesPerDay).isEqualTo(10)
            assertThat(limits.windowHours).isEqualTo(24)
        },
        "prompts_get_200.json" to data(listSerializer(PromptAnswerDto.serializer())) {
            // [] rather than null now.
            assertThat(it).isEmpty()
        },
        "prompts_put_200.json" to data(PromptAnswerDto.serializer()) {
            assertThat(it.promptId).isEqualTo(1)
            assertThat(it.answer).isEqualTo("Ask me about filter coffee.")
        },
        "pulse_explain_404_candidate_unavailable.json" to error { error, _ ->
            // A dating-service 404 carries meta: it is a refusal, NOT the pilot gate.
            assertThat(refusedCode(error)).isEqualTo("CANDIDATE_UNAVAILABLE")
        },
        "pulse_explain_get_200.json" to data(ExplainDto.serializer()) {
            assertThat(it.reasons.map { r -> r.kind }).containsExactly("distance", "age_match", "gender_pref").inOrder()
            assertThat(it.distanceBucket).isEqualTo("lt_5_km")
            assertThat(it.isPromoted).isFalse()
        },
        "pulse_pass_post_200.json" to data(PassDto.serializer()) {
            assertThat(it.passed).isTrue()
            assertThat(it.candidateId).isEqualTo("<candidate>")
        },
        "pulse_today_get_200.json" to { _, raw ->
            // {data, meta{generated_at,size,cohort_gated,request_id}}; cohort_gated
            // ALSO stays at the top level (omitted when false) for the shipped app.
            val today = strict.decodeFromString(PulseTodayDto.serializer(), raw)
            val card = today.data.single()
            assertThat(today.meta?.size).isEqualTo(1)
            assertThat(today.meta?.cohortGated).isFalse()
            assertThat(today.gated).isFalse()
            assertThat(card.matchReasons).hasSize(3)
            assertThat(card.profile.firstName).isEqualTo("Asha")
            assertThat(card.profile.distanceBucket).isEqualTo("lt_5_km")
            // Blur-by-default is OFF now: a new profile's photo shows openly.
            assertThat(card.profile.primaryPhotoUrl).isEqualTo("/v1/dating/photos/<uuid>/full")
            assertThat(card.profile.tuneSummary).isEmpty()
            assertThat(card.echoes?.topReelId).isNull()
            // The detail block is additive and every member is omitted when
            // empty: this card carries only the gallery.
            val detail = checkNotNull(card.profile.detail)
            assertThat(detail.bio).isEmpty()
            assertThat(detail.prompts).isEmpty()
            assertThat(detail.languages).isEmpty()
            assertThat(detail.photos.single().state).isEqualTo("full")
        },
        "pulse_today_get_200_rich_card.json" to { _, raw ->
            // The deck card someone actually decides on: bio, prompts,
            // languages and the whole gallery, each photo with its OWN state.
            val card = strict.decodeFromString(PulseTodayDto.serializer(), raw).data.single()
            assertThat(card.profile.firstName).isEqualTo("Asha")
            assertThat(card.profile.distanceBucket).isEqualTo("lt_5_km")
            assertThat(card.matchReasons).hasSize(3)
            val detail = checkNotNull(card.profile.detail)
            assertThat(detail.bio).isEqualTo("Filter coffee, long drives and a bad sense of direction.")
            assertThat(detail.prompts.map { p -> p.promptId }).containsExactly(1, 2, 9).inOrder()
            assertThat(detail.prompts.first().question).isEqualTo("My ideal Sunday is...")
            assertThat(detail.prompts.first().answer).isEqualTo("Dosa, a bookshop, and absolutely no alarm.")
            assertThat(detail.languages).containsExactly("telugu", "english").inOrder()
            // Primary first, and the match_only photo is blurred in the SAME gallery.
            assertThat(detail.photos.map { p -> p.state }).containsExactly("full", "full", "blurred").inOrder()
            assertThat(detail.photos.first().url).isEqualTo("/v1/dating/photos/<photo-primary>/full")
            assertThat(detail.photos.last().url).isEqualTo("/v1/dating/photos/<photo-match_only>/blurred")
        },
        // ── Fixtures the server had and the app had never copied ────────────
        "photos_post_400_invalid_visibility.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_VISIBILITY")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed)
                .containsExactly("public", "match_only", "sparked_only").inOrder()
        },
        "preferences_put_400_invalid_age_range.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_AGE_RANGE")
            assertThat(details(error, RangeDetailsDto.serializer(), name)).isEqualTo(RangeDetailsDto(min = 18, max = 120))
        },
        "preferences_put_400_invalid_distance_km.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_DISTANCE_KM")
            assertThat(details(error, RangeDetailsDto.serializer(), name)).isEqualTo(RangeDetailsDto(min = 1, max = 500))
        },
        "preferences_put_400_invalid_intent_filter.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_INTENT_FILTER")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed)
                .containsExactly("casual", "serious", "marriage").inOrder()
        },
        "profile_upsert_400_invalid_intent.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_INTENT")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed)
                .containsExactly("casual", "serious", "marriage").inOrder()
        },
        "prompts_put_400_answer_required.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("PROMPT_ANSWER_REQUIRED")
            assertThat(details(error, RangeDetailsDto.serializer(), name)).isEqualTo(RangeDetailsDto(min = 1, max = 280))
        },
        "prompts_put_400_answer_too_long.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("PROMPT_ANSWER_TOO_LONG")
            // Only the upper bound is sent.
            assertThat(details(error, RangeDetailsDto.serializer(), name)).isEqualTo(RangeDetailsDto(max = 280))
        },
        "prompts_put_400_unknown_prompt.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("UNKNOWN_PROMPT")
            // Prompt IDS — numbers, unlike every other `allowed`.
            assertThat(details(error, AllowedIdsDetailsDto.serializer(), name).allowed).isEqualTo((1..12).toList())
        },
        "pulse_pass_400_reason_too_long.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("PASS_REASON_TOO_LONG")
            assertThat(details(error, RangeDetailsDto.serializer(), name).max).isEqualTo(200)
        },
        "spark_create_409_onboarding_incomplete.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("ONBOARDING_INCOMPLETE")
            assertThat(details(error, OnboardingIncompleteDetailsDto.serializer(), name))
                .isEqualTo(OnboardingIncompleteDetailsDto(status = "pending_selfie", step = "pending_selfie"))
        },
        // ── Mechanic M1: the refilling deck ─────────────────────────────────
        "pulse_today_get_200_refill.json" to { _, raw ->
            // A batch, with the allowance in meta. Nothing has been used yet,
            // so resets_at is absent.
            val today = strict.decodeFromString(PulseTodayDto.serializer(), raw)
            assertThat(today.data).hasSize(1)
            val meta = checkNotNull(today.meta)
            assertThat(meta.dailyLimit).isEqualTo(2)
            assertThat(meta.remainingToday).isEqualTo(2)
            assertThat(meta.resetsAt).isNull()
            val deck = meta.onto(DeckUi())
            assertThat(deck.metered).isTrue()
            assertThat(deck.outOfCards).isFalse()
            assertThat(DeckCopy.cardsLeft(deck)).isEqualTo("2 cards left today")
        },
        "pulse_today_get_200_out_of_cards.json" to { _, raw ->
            // The allowance is spent: daily_limit present, remaining_today
            // OMITTED (Go drops the 0), and no cards.
            val today = strict.decodeFromString(PulseTodayDto.serializer(), raw)
            assertThat(today.data).isEmpty()
            val meta = checkNotNull(today.meta)
            assertThat(meta.dailyLimit).isEqualTo(2)
            assertThat(meta.remainingToday).isEqualTo(0)
            assertThat(meta.resetsAt).isNotNull()
            assertThat(meta.onto(DeckUi()).outOfCards).isTrue()
        },
        "report_post_201.json" to data(ReportResultDto.serializer()) {
            assertThat(it.reason).isEqualTo("harassment")
            assertThat(it.status).isEqualTo("submitted")
            // Reporting always blocks the target for the reporter.
            assertThat(it.autoBlocked).isTrue()
            assertThat(it.blocked).isTrue()
            assertThat(it.reporterAnonymised).isFalse()
        },
        "selfie_challenge_422_consent_required.json" to error { error, _ ->
            assertThat(error).isEqualTo(DatingError.ConsentRequired("biometric_selfie", "v1.0-2026-04-29"))
        },
        "selfie_challenge_post_200.json" to data(SelfieChallengeDto.serializer()) {
            assertThat(it.instruction).isEqualTo("blink_twice")
            assertThat(it.maxDurationMs).isEqualTo(4_000)
            // The recorder must stay inside the server's cap.
            assertThat(SelfieOutcomes.recordMillis(it.maxDurationMs) + SelfieOutcomes.RECORD_WATCHDOG_MS)
                .isAtMost(SelfieOutcomes.MAX_CLIP_MS.toLong())
        },
        "selfie_post_200_not_enough_blinks.json" to data(SelfieResultDto.serializer()) {
            assertThat(it.reason).isEqualTo("NOT_ENOUGH_BLINKS")
            assertThat(SelfieOutcomes.fromResult(it)).isEqualTo(
                SelfieState.Retry("NOT_ENOUGH_BLINKS", SelfieOutcomes.copyFor("NOT_ENOUGH_BLINKS"), 4),
            )
        },
        "selfie_post_200_passed.json" to data(SelfieResultDto.serializer()) {
            assertThat(it.passed).isTrue()
            assertThat(it.trustTier).isEqualTo("selfie")
            assertThat(SelfieOutcomes.fromResult(it)).isEqualTo(SelfieState.Passed)
        },
        "selfie_post_200_review.json" to data(SelfieResultDto.serializer()) {
            assertThat(it.status).isEqualTo("pending_review")
            assertThat(SelfieOutcomes.fromResult(it)).isEqualTo(SelfieState.InReview)
        },
        "selfie_post_409_media_not_ready.json" to error { error, _ ->
            // The clip is still processing: the attempt is NOT spent, so the
            // screen offers the same clip again rather than a new recording.
            assertThat(refusedCode(error)).isEqualTo(SelfieOutcomes.CODE_MEDIA_NOT_READY)
        },
        "share_location_delete_200.json" to data(StopShareDto.serializer()) {
            assertThat(it.stopped).isTrue()
            assertThat(it.shareId).isEqualTo("<share>")
        },
        "share_location_get_200.json" to data(MyLocationSharesDto.serializer()) {
            val share = it.items.single()
            assertThat(share.shareId).isEqualTo("<share>")
            assertThat(share.recipientKind).isEqualTo("trusted_contact")
            assertThat(share.recipient?.firstName).isEqualTo("Asha")
        },
        "share_location_post_200.json" to data(ShareLocationDto.serializer()) {
            assertThat(it.recipientKind).isEqualTo("trusted_contact")
            assertThat(it.expiresAt).isNotEmpty()
        },
        "shared_location_get_200.json" to data(SharedLocationDto.serializer()) {
            // The ONLY share shape that carries coordinates.
            assertThat(it.latitude).isEqualTo(17.44)
            assertThat(it.longitude).isEqualTo(78.39)
            assertThat(it.stoppedAt).isNull()
        },
        "shared_locations_get_200.json" to data(SharedWithMeDto.serializer()) {
            val share = it.items.single()
            assertThat(share.shareId).isEqualTo("<share>")
            assertThat(share.person?.firstName).isEqualTo("Asha")
        },
        "spark_accept_post_201.json" to data(SparkCreatedDto.serializer()) {
            assertThat(it.matched).isTrue()
            assertThat(it.matchId).isEqualTo("<uuid>")
            assertThat(it.spark?.targetKind).isEqualTo("photo")
        },
        "spark_create_404_candidate_unavailable.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("CANDIDATE_UNAVAILABLE")
        },
        "spark_create_429_rate_limited.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("SPARK_RATE_LIMITED")
            // resets_at is always sent now; the golden redacts it.
            assertThat(details(error, RateLimitDetailsDto.serializer(), name)).isEqualTo(RateLimitDetailsDto(50, 24, "<timestamp>"))
        },
        // ── Mechanic M3: Super Spark ────────────────────────────────────────
        "spark_create_post_201_super.json" to data(SparkCreatedDto.serializer()) {
            val spark = checkNotNull(it.spark)
            assertThat(spark.superSpark).isTrue()
            assertThat(it.matched).isFalse()
            assertThat(it.matchId).isNull()
        },
        "spark_create_429_super_limit_reached.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("SUPER_SPARK_LIMIT_REACHED")
            assertThat(details(error, RateLimitDetailsDto.serializer(), name)).isEqualTo(RateLimitDetailsDto(1, 24, "<timestamp>"))
        },
        "sparks_incoming_get_200_super_first.json" to data(listSerializer(SparkDto.serializer())) {
            // The server sorts Super Sparks first; an ordinary spark omits the key.
            assertThat(it.map { s -> s.superSpark }).containsExactly(true, false).inOrder()
            assertThat(it.first().person?.firstName).isEqualTo("Asha")
            assertThat(it.last().person?.distanceBucket).isNull()
        },
        "premium_catalogue_get_200_super_spark.json" to data(PremiumCatalogueDto.serializer()) {
            assertThat(it.products.map { p -> p.id })
                .containsExactly("pass_30d", "pass_90d", "pass_365d", "boost", "super_spark_5", "super_spark_15").inOrder()
            val packs = it.products.filter { p -> p.kind == "super_spark" }
            assertThat(packs.map { p -> p.quantity }).containsExactly(5, 15).inOrder()
            assertThat(packs.map { p -> packLabel(p) }).containsExactly("5 Super Sparks", "15 Super Sparks").inOrder()
            // Everything else omits quantity.
            assertThat(it.products.filter { p -> p.kind != "super_spark" }.map { p -> p.quantity }.toSet()).containsExactly(0)
            assertThat(packLabel(it.products.first())).isNull()
        },
        // ── Mechanic M2: undo a pass ────────────────────────────────────────
        "pulse_rewind_post_200.json" to data(RewindDto.serializer()) {
            assertThat(it.rewound).isTrue()
            assertThat(it.candidateId).isEqualTo("<candidate>")
            // The card is the deck's own shape, so it goes back on the stack as is.
            val card = checkNotNull(it.card)
            assertThat(card.candidateId).isEqualTo("<candidate>")
            assertThat(card.profile.firstName).isEqualTo("Asha")
            assertThat(card.profile.lastActiveLabel).isEqualTo("Active today")
            assertThat(checkNotNull(card.profile.detail).photos.single().state).isEqualTo("full")
            // The allowance after the undo: remaining_today omitted means none left.
            assertThat(it.allowance.dailyLimit).isEqualTo(1)
            assertThat(it.allowance.remainingToday).isEqualTo(0)
            assertThat(it.allowance.toUi()).isEqualTo(AllowanceUi(dailyLimit = 1, remaining = 0, resetsAt = null))
        },
        "pulse_rewind_404_not_enabled.json" to error { error, _ ->
            // Written by dating-service (it carries meta): a refusal, not the pilot gate.
            assertThat(refusedCode(error)).isEqualTo("MECHANIC_NOT_ENABLED")
        },
        "pulse_rewind_409_nothing_to_undo.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("REWIND_NOTHING_TO_UNDO")
        },
        "pulse_rewind_429_limit_reached.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("REWIND_LIMIT_REACHED")
            assertThat(details(error, RateLimitDetailsDto.serializer(), name)).isEqualTo(RateLimitDetailsDto(1, 24, "<timestamp>"))
        },
        // ── Mechanic M10: allowances ────────────────────────────────────────
        "allowances_get_200.json" to data(AllowancesDto.serializer()) {
            assertThat(it.sparks).isEqualTo(AllowanceDto(dailyLimit = 50, remainingToday = 49, resetsAt = "<timestamp>"))
            assertThat(it.deck?.remainingToday).isEqualTo(23)
            // Nothing used yet: no resets_at.
            assertThat(it.rewind).isEqualTo(AllowanceDto(dailyLimit = 1, remainingToday = 1))
            val superSpark = checkNotNull(it.superSpark)
            assertThat(superSpark.remainingToday).isEqualTo(1)
            // purchased_balance is omitted at 0.
            assertThat(superSpark.purchasedBalance).isEqualTo(0)
        },
        "allowances_get_200_mechanics_off.json" to data(AllowancesDto.serializer()) {
            // Every mechanic flag off: only sparks, and the rest ABSENT.
            assertThat(it.sparks.remainingToday).isEqualTo(50)
            assertThat(it.deck).isNull()
            assertThat(it.rewind).isNull()
            assertThat(it.superSpark).isNull()
        },
        // ── Mechanic M4: who liked you ──────────────────────────────────────
        "liked_you_get_200_locked.json" to data(LikedYouDto.serializer()) {
            assertThat(it.total).isEqualTo(2)
            assertThat(it.unlocked).isFalse()
            // Super Sparks first; an ordinary spark omits the key.
            assertThat(it.items.map { i -> i.superSpark }).containsExactly(true, false).inOrder()
            it.items.forEach { item ->
                assertThat(item.sparkId).isEqualTo("<uuid>")
                // Nothing that identifies the sender, and only the blurred route.
                assertThat(item.person).isNull()
                assertThat(item.note).isNull()
                assertThat(item.photoUrl).isEqualTo("/v1/dating/liked-you/<uuid>/photo")
                assertThat(PhotoRules.likedYouPath(item.photoUrl)).isEqualTo(item.photoUrl)
                assertThat(PhotoRules.photoIdOf(item.photoUrl)).isNull()
            }
        },
        "liked_you_get_200_unlocked.json" to data(LikedYouDto.serializer()) {
            assertThat(it.total).isEqualTo(2)
            assertThat(it.unlocked).isTrue()
            assertThat(it.items.map { i -> i.superSpark }).containsExactly(true, false).inOrder()
            assertThat(it.items.map { i -> checkNotNull(i.person).userId }).containsExactly("<super_sender>", "<sender>").inOrder()
            it.items.forEach { item ->
                val person = checkNotNull(item.person)
                assertThat(person.firstName).isEqualTo("Asha")
                assertThat(person.age).isEqualTo(30)
                assertThat(person.photoState).isEqualTo("full")
                // The item's photo is the person's own route.
                assertThat(item.photoUrl).isEqualTo(person.primaryPhotoUrl)
                assertThat(item.note).isEqualTo("Loved your answer")
                assertThat(checkNotNull(person.detail).photos.single().state).isEqualTo("full")
            }
        },
        "sparks_incoming_get_200_locked.json" to data(listSerializer(SparkDto.serializer())) {
            assertThat(it.map { s -> s.superSpark }).containsExactly(true, false).inOrder()
            it.forEach { spark ->
                assertThat(spark.locked).isTrue()
                assertThat(spark.id).isEqualTo("<uuid>")
                // No sender at all: the old list path must cope with that.
                assertThat(spark.fromUserId).isEmpty()
                assertThat(spark.person).isNull()
                assertThat(spark.note).isNull()
                assertThat(spark.photoUrl).isEqualTo("/v1/dating/liked-you/<uuid>/photo")
            }
        },
        "spark_accept_403_liked_you_locked.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("LIKED_YOU_LOCKED")
            assertThat((error as DatingError.Refused).status).isEqualTo(403)
            assertThat(DatingCopy.forError(error)).isEqualTo("You'll need a Premium pass to see who sparked you.")
        },
        "spark_create_post_201_matched.json" to data(SparkCreatedDto.serializer()) {
            assertThat(it.matched).isTrue()
            assertThat(it.matchId).isEqualTo("<uuid>")
            assertThat(it.spark?.targetKind).isEqualTo("prompt")
        },
        "spark_decline_post_200.json" to data(SparkDeclineDto.serializer()) {
            assertThat(it.declined).isTrue()
            assertThat(it.sparkId).isEqualTo("<spark>")
        },
        "sparks_incoming_get_200.json" to data(listSerializer(SparkDto.serializer())) {
            val spark = it.single()
            assertThat(spark.note).isEqualTo("Loved your answer")
            val person = checkNotNull(spark.person)
            assertThat(person.userId).isEqualTo(spark.fromUserId)
            assertThat(person.firstName).isEqualTo("Asha")
            assertThat(person.age).isEqualTo(30)
            // Blur-by-default is OFF: an unmatched spark sender shows openly.
            assertThat(person.photoState).isEqualTo("full")
            assertThat(person.city).isEqualTo("Hyderabad")
            assertThat(person.intent).isEqualTo("casual")
            assertThat(person.lastActiveBucket).isNull()
            // A spark is decided on, so it carries the detail block too.
            assertThat(checkNotNull(person.detail).photos.single().state).isEqualTo("full")
        },
        "stash_get_200.json" to data(listSerializer(StashDto.serializer())) {
            assertThat(it.single().candidateId).isEqualTo("<candidate>")
        },
        "stash_post_201.json" to data(StashDto.serializer()) {
            assertThat(it.candidateId).isEqualTo("<candidate>")
            assertThat(it.expiresAt).isNotEmpty()
        },
        "trusted_contact_put_200.json" to data(TrustedContactDto.serializer()) {
            assertThat(it.shareLocationOnPanic).isTrue()
        },
        "trusted_contacts_get_200.json" to data(TrustedContactsDto.serializer()) {
            assertThat(it.max).isEqualTo(MAX_TRUSTED_CONTACTS)
            val contact = it.items.single()
            assertThat(contact.contactId).isEqualTo("<contact>")
            // The contact carries its own card: no match row is consulted.
            val person = checkNotNull(contact.person)
            assertThat(person.userId).isEqualTo(contact.contactId)
            assertThat(person.firstName).isEqualTo("Asha")
            assertThat(person.age).isEqualTo(30)
            assertThat(person.photoState).isEqualTo("full")
            // The wire carries city and intent here too, because it is the same
            // compact card. The safety screens render NEITHER: who someone is
            // looking for is no part of reaching them in an emergency.
            assertThat(person.intent).isEqualTo("casual")
            // A safety surface carries no bio and no gallery.
            assertThat(person.detail).isNull()
        },
        "trusted_contacts_get_200_profile_gone.json" to data(TrustedContactsDto.serializer()) {
            // The field is always PRESENT and null when the profile was purged.
            val contact = it.items.single()
            assertThat(contact.contactId).isEqualTo("<contact>")
            assertThat(contact.person).isNull()
        },
        // ── Mechanic M5: first move ─────────────────────────────────────────
        "first_move_get_200.json" to data(FirstMoveSettingsDto.serializer()) {
            assertThat(it.enabled).isTrue()
            assertThat(it.questions.map { q -> q.text })
                .containsExactly("What does your perfect Sunday look like?", "Tea or coffee, and why?").inOrder()
            assertThat(it.questions.map { q -> q.id }.toSet()).containsExactly("<uuid>")
            assertThat(it.maxQuestions).isEqualTo(3)
            assertThat(it.maxLength).isEqualTo(140)
        },
        "first_move_put_200.json" to data(FirstMoveSettingsDto.serializer()) {
            // The PUT answers with the same shape as the GET.
            assertThat(it.enabled).isTrue()
            assertThat(it.questions).hasSize(2)
            assertThat(it.maxQuestions).isEqualTo(3)
            assertThat(it.maxLength).isEqualTo(140)
        },
        "first_move_put_400_too_many_questions.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("OPENING_QUESTIONS_TOO_MANY")
            assertThat(details(error, RangeDetailsDto.serializer(), name).max).isEqualTo(3)
            assertThat(DatingCopy.forError(error, strict)).isEqualTo("You can have up to 3 opening questions.")
        },
        "first_move_get_404_not_enabled.json" to error { error, _ ->
            // Written by dating-service (it carries meta): the flag is off, not the pilot gate.
            assertThat(refusedCode(error)).isEqualTo("MECHANIC_NOT_ENABLED")
            assertThat((error as DatingError.Refused).status).isEqualTo(404)
        },
        "match_get_200_first_move_waiting.json" to data(MatchDto.serializer()) {
            assertThat(it.status).isEqualTo("matched")
            assertThat(it.person?.firstName).isEqualTo("Asha")
            val move = checkNotNull(it.firstMove)
            assertThat(move.youMoveFirst).isFalse()
            assertThat(move.deadline).isEqualTo("<timestamp>")
            assertThat(move.canExtend).isTrue()
            assertThat(move.openingQuestions.map { q -> q.text })
                .containsExactly("What does your perfect Sunday look like?", "Tea or coffee, and why?").inOrder()
            val ui = checkNotNull(move.toUi())
            assertThat(ui.waiting).isTrue()
            assertThat(ui.questions).hasSize(2)
            assertThat(ui.canExtend).isTrue()
            // The golden redacts the time; an unparseable deadline is simply absent.
            assertThat(ui.deadline).isNull()
        },
        "match_get_200_first_move_yours.json" to data(MatchDto.serializer()) {
            val move = checkNotNull(it.firstMove)
            assertThat(move.youMoveFirst).isTrue()
            assertThat(move.deadline).isEqualTo("<timestamp>")
            // The first mover gets no questions and no extend: Go omits the list.
            assertThat(move.openingQuestions).isEmpty()
            assertThat(move.canExtend).isFalse()
            val ui = checkNotNull(move.toUi())
            assertThat(ui.youMoveFirst).isTrue()
            assertThat(ui.waiting).isFalse()
        },
        "match_opening_answer_post_201.json" to data(OpeningAnswerDto.serializer()) {
            assertThat(it.sent).isTrue()
            assertThat(it.conversationId).isEqualTo("<uuid>")
        },
        "match_opening_answer_409_not_pending.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("FIRST_MOVE_NOT_PENDING")
            assertThat((error as DatingError.Refused).status).isEqualTo(409)
            assertThat(DatingCopy.forError(error)).isEqualTo("This match isn't waiting for an answer from you any more.")
        },
        "match_extend_post_200_free.json" to data(ExtendDto.serializer()) {
            assertThat(it.extended).isTrue()
            assertThat(it.extraHours).isEqualTo(24)
            // The free extend omits extra_days.
            assertThat(it.extraDays).isEqualTo(0)
            assertThat(it.expiresAt).isEqualTo("<timestamp>")
            assertThat(it.free).isTrue()
            assertThat(FirstMoveCopy.extended(it.free, it.extraHours, it.extraDays)).isEqualTo("Done. They have 24 more hours.")
        },
        "match_extend_429_limit_reached.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("EXTEND_LIMIT_REACHED")
            assertThat(details(error, RateLimitDetailsDto.serializer(), name)).isEqualTo(RateLimitDetailsDto(1, 24, "<timestamp>"))
        },
        "verification_status_get_200.json" to data(VerificationStatusDto.serializer()) {
            assertThat(it.selfie.state).isEqualTo("passed")
            assertThat(it.selfie.attemptsLeftToday).isEqualTo(4)
            assertThat(it.selfie.attemptsPerDay).isEqualTo(5)
            assertThat(it.selfie.windowHours).isEqualTo(24)
            assertThat(it.verified).isTrue()
            assertThat(it.trustTier).isEqualTo("selfie")
            assertThat(it.nextStep).isEqualTo("none")
        },
        // ── Mechanic M6: profile basics and filters ─────────────────────────
        "profile_options_get_200.json" to data(ProfileOptionsDto.serializer()) {
            assertThat(it.interests).hasSize(40)
            assertThat(it.interests.first()).isEqualTo(OptionDto("art", "Art"))
            assertThat(it.maxInterests).isEqualTo(10)
            assertThat(it.languages.first { o -> o.code == "te" }.label).isEqualTo("Telugu")
            assertThat(it.maxLanguages).isEqualTo(8)
            assertThat(it.heightCm).isEqualTo(OptionRangeDto(min = 120, max = 230))
            assertThat(it.drinking.map { o -> o.code }).containsExactly("never", "rarely", "socially", "regularly").inOrder()
            assertThat(it.smoking.last().label).isEqualTo("Trying to quit")
            assertThat(it.exercise).hasSize(4)
            assertThat(it.diet.first { o -> o.code == "non_vegetarian" }.label).isEqualTo("Non-vegetarian")
            assertThat(it.distanceBuckets.map { o -> o.code }).containsExactly("lt_5_km", "km_5_10", "km_10_25", "gt_25_km").inOrder()
            // The display model keeps the server's labels and limits as sent.
            val ui = ProfileOptionsUi.from(it)
            assertThat(ui.heightRange).isEqualTo(120..230)
            assertThat(ui.distanceLabel("gt_25_km")).isEqualTo("Any distance")
            assertThat(ui.basicLabel(LifestyleBasic.EXERCISE, "often")).isEqualTo("Often")
        },
        "preferences_get_200_filters.json" to data(PreferencesDto.serializer()) {
            assertThat(it.distanceBucket).isEqualTo("km_5_10")
            val pass = checkNotNull(it.passFilters)
            assertThat(pass.active).isTrue()
            assertThat(pass.verifiedOnly).isTrue()
            assertThat(pass.minHeightCm).isEqualTo(160)
            assertThat(pass.maxHeightCm).isEqualTo(190)
            assertThat(pass.languages).containsExactly("en", "te").inOrder()
            assertThat(pass.drinking).containsExactly("never", "socially").inOrder()
            // Go sends [] for an empty filter, never null.
            assertThat(pass.exercise).isEmpty()
            assertThat(pass.diet).containsExactly("vegetarian")
        },
        "preferences_put_200_filters.json" to data(PreferencesDto.serializer()) {
            // The PUT answers with the same view as the GET.
            assertThat(it.distanceBucket).isEqualTo("km_5_10")
            assertThat(it.passFilters?.active).isTrue()
            assertThat(it.intentFilter).containsExactly("serious")
        },
        "preferences_put_403_filters_require_pass.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("FILTERS_REQUIRE_PASS")
            assertThat((error as DatingError.Refused).status).isEqualTo(403)
            assertThat(DatingCopy.forError(error)).isEqualTo(DatingCopy.FILTERS_REQUIRE_PASS)
        },
        "preferences_put_400_invalid_distance_bucket.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_DISTANCE_BUCKET")
            assertThat(details(error, AllowedDetailsDto.serializer(), name).allowed)
                .containsExactly("lt_5_km", "km_5_10", "km_10_25", "gt_25_km").inOrder()
            assertThat(FiltersRules.fieldFor(error, strict)).isEqualTo(FiltersField.DISTANCE)
        },
        "profile_upsert_400_invalid_interest.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_INTEREST")
            val details = details(error, FieldRefusalDetailsDto.serializer(), name)
            assertThat(details.field).isEqualTo("interests")
            assertThat(details.allowed).hasSize(40)
            assertThat(AboutMeField.fromWire(details.field)).isEqualTo(AboutMeField.INTERESTS)
        },
        "profile_upsert_400_invalid_height.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_HEIGHT")
            assertThat(details(error, FieldRefusalDetailsDto.serializer(), name))
                .isEqualTo(FieldRefusalDetailsDto(field = "height_cm", min = 120, max = 230))
            assertThat(DatingCopy.forError(error, strict)).isEqualTo("Height needs to be between 120 and 230 cm.")
        },
        "privacy_patch_403_filters_require_pass.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("FILTERS_REQUIRE_PASS")
            assertThat((error as DatingError.Refused).status).isEqualTo(403)
        },
        // ── Mechanic M7: daily picks ────────────────────────────────────────
        "picks_get_200.json" to { _, raw ->
            // NOT the envelope: the deck's {data, meta}, meta carrying the local day.
            val picks = strict.decodeFromString(PicksDto.serializer(), raw)
            val card = picks.data.single()
            assertThat(card.candidateId).isEqualTo("<candidate>")
            assertThat(card.matchReasons).isEmpty()
            assertThat(card.profile.firstName).isEqualTo("Asha")
            assertThat(card.profile.lastActiveLabel).isEqualTo("Active today")
            assertThat(card.profile.travelling).isFalse()
            assertThat(card.profile.detail?.photos?.single()?.state).isEqualTo("full")
            val meta = checkNotNull(picks.meta)
            assertThat(meta.date).isEqualTo("<date>")
            assertThat(meta.timezone).isEqualTo("UTC")
            assertThat(meta.resetsAt).isEqualTo("<timestamp>")
            assertThat(meta.size).isEqualTo(1)
            // The card reads exactly as a deck card does, with no travelling marker.
            val ui = card.toCardUi(photoUrls())
            assertThat(ui.name).isEqualTo("Asha")
            assertThat(ui.visiting).isNull()
            // The golden redacts the time: the header falls back to general words.
            assertThat(PicksCopy.resetLine(parseInstant(meta.resetsAt), Instant.EPOCH, ZoneId.of("UTC")))
                .isEqualTo("New picks every day at midnight")
        },
        "picks_get_400_invalid_timezone.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_TIMEZONE")
            assertThat((error as DatingError.Refused).status).isEqualTo(400)
        },
        "picks_get_404_not_enabled.json" to error { error, _ ->
            // Written by dating-service (it carries meta): the flag is off, not the pilot gate.
            assertThat(refusedCode(error)).isEqualTo("MECHANIC_NOT_ENABLED")
            assertThat((error as DatingError.Refused).status).isEqualTo(404)
        },
        // ── Mechanic M8: travel ─────────────────────────────────────────────
        "travel_get_200.json" to data(TravelDto.serializer()) {
            // No pass, no trip: `active` omitted, `available` false.
            assertThat(it.active).isNull()
            assertThat(it.available).isFalse()
            assertThat(it.maxDays).isEqualTo(7)
            assertThat(it.cities).hasSize(22)
            assertThat(it.cities.first()).isEqualTo(TravelCityDto("ahmedabad", "Ahmedabad"))
            assertThat(it.cities.first { c -> c.code == "new_york" }.label).isEqualTo("New York")
            val cities = TravelRules.cities(it.cities)
            assertThat(cities.map { c -> c.label }).isInOrder(String.CASE_INSENSITIVE_ORDER)
            assertThat(TravelRules.trip(it.active)).isNull()
        },
        "travel_put_200.json" to data(TravelDto.serializer()) {
            assertThat(it.available).isTrue()
            val active = checkNotNull(it.active)
            assertThat(active.city).isEqualTo(TravelCityDto("mumbai", "Mumbai"))
            assertThat(active.startsAt).isEqualTo("<timestamp>")
            assertThat(active.endsAt).isEqualTo("<timestamp>")
            assertThat(it.cities).hasSize(22)
            val trip = checkNotNull(TravelRules.trip(active))
            assertThat(trip.cityLabel).isEqualTo("Mumbai")
            // The golden redacts the times; an unparseable end is simply left off.
            assertThat(trip.endsAt).isNull()
            assertThat(TravelCopy.browsingUntil(trip, ZoneId.of("UTC"))).isEqualTo("Browsing Mumbai")
        },
        "travel_put_403_requires_pass.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("TRAVEL_REQUIRES_PASS")
            assertThat((error as DatingError.Refused).status).isEqualTo(403)
            assertThat(DatingCopy.forError(error)).isEqualTo(DatingCopy.TRAVEL_REQUIRES_PASS)
        },
        "travel_put_400_invalid_city.json" to error { error, name ->
            assertThat(refusedCode(error)).isEqualTo("INVALID_CITY")
            val allowed = details(error, AllowedDetailsDto.serializer(), name).allowed
            assertThat(allowed).hasSize(22)
            assertThat(allowed).containsAtLeast("mumbai", "new_york", "visakhapatnam")
        },
        "travel_get_404_not_enabled.json" to error { error, _ ->
            assertThat(refusedCode(error)).isEqualTo("MECHANIC_NOT_ENABLED")
            assertThat((error as DatingError.Refused).status).isEqualTo(404)
        },
        "pulse_today_get_200_travelling.json" to { _, raw ->
            // Someone on a trip in the viewer's deck: `travelling` true, city the destination.
            val today = strict.decodeFromString(PulseTodayDto.serializer(), raw)
            val card = today.data.single()
            assertThat(card.profile.travelling).isTrue()
            assertThat(card.profile.city).isEqualTo("Hyderabad")
            assertThat(card.profile.distanceBucket).isEqualTo("lt_5_km")
            assertThat(today.meta?.dailyLimit).isEqualTo(50)
            assertThat(card.toCardUi(photoUrls()).visiting).isEqualTo("Visiting Hyderabad")
        },
        "person_get_200_basics.json" to data(DatingPersonDto.serializer()) {
            val detail = checkNotNull(it.detail)
            assertThat(detail.languages).containsExactly("en", "te").inOrder()
            assertThat(detail.interests).containsExactly("books", "cricket", "yoga").inOrder()
            assertThat(detail.heightCm).isEqualTo(172)
            assertThat(detail.drinking).isEqualTo("socially")
            assertThat(detail.smoking).isEqualTo("never")
            assertThat(detail.exercise).isEqualTo("often")
            assertThat(detail.diet).isEqualTo("vegetarian")
            assertThat(it.lastActiveLabel).isEqualTo("Active today")
        },
    )

    @Test
    fun `every fixture has a parser and every parser has a fixture`() {
        val files = contractsDir.listFiles { f -> f.extension == "json" }.orEmpty().map { it.name }.toSet()
        assertThat(files).isNotEmpty()
        assertThat(parsers.keys).containsExactlyElementsIn(files)
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        parsers.forEach { (name, parse) -> parse(name, fixture(name)) }
    }

    @Test
    fun `every fixture also decodes with the production json`() {
        // `/pulse/today` is not the envelope; each fixture and the cards it carries.
        val pulse = mapOf(
            "pulse_today_get_200.json" to 1,
            "pulse_today_get_200_rich_card.json" to 1,
            "pulse_today_get_200_refill.json" to 1,
            "pulse_today_get_200_out_of_cards.json" to 0,
            "pulse_today_get_200_travelling.json" to 1,
        )
        // `/picks` is not the envelope either.
        val picks = mapOf("picks_get_200.json" to 1)
        parsers.keys.filter { statusOf(it) < 300 && it !in pulse && it !in picks }.forEach { name ->
            val envelope = production.decodeFromString(ApiEnvelope.serializer(kotlinx.serialization.json.JsonElement.serializer()), fixture(name))
            assertThat(envelope.data).isNotNull()
        }
        picks.forEach { (name, cards) ->
            assertThat(production.decodeFromString(PicksDto.serializer(), fixture(name)).data).hasSize(cards)
        }
        pulse.forEach { (name, cards) ->
            assertThat(production.decodeFromString(PulseTodayDto.serializer(), fixture(name)).data).hasSize(cards)
        }
    }

    @Test
    fun `the gateway's pilot 404 has no meta and is not available, a dating 404 is a refusal`() {
        val gateway = """{"error":{"code":"NOT_FOUND","message":"Not found"}}"""
        assertThat(DatingError.from(404, gateway, production)).isEqualTo(DatingError.NotAvailable)
        assertThat(DatingError.from(404, "404 page not found", production)).isEqualTo(DatingError.NotAvailable)
        assertThat(DatingError.from(404, null, production)).isEqualTo(DatingError.NotAvailable)

        val profileMissing = """{"error":{"code":"NOT_FOUND","message":"profile not found"},"meta":{"request_id":"r"}}"""
        assertThat(refusedCode(DatingError.from(404, profileMissing, production))).isEqualTo("NOT_FOUND")
    }

    @Test
    fun `a premium purchase request never carries a price`() {
        val body = production.encodeToJsonElement(
            PremiumPurchaseRequest.serializer(),
            PremiumPurchaseRequest(product = "pass_30d", idempotencyKey = "k-1"),
        ).jsonObject
        assertThat(body.keys).containsExactly("product", "idempotency_key")
    }

    // ── helpers ──────────────────────────────────────────────────────────────

    private fun fixture(name: String): String = File(contractsDir, name).readText()

    private fun statusOf(name: String): Int =
        Regex("_(\\d{3})(?:_|\\.json)").find(name)?.groupValues?.get(1)?.toInt() ?: error("no status in $name")

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String, String) -> Unit = { name, raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        check(checkNotNull(envelope.data) { "$name has no data" })
    }

    private fun error(check: (DatingError, String) -> Unit): (String, String) -> Unit = { name, raw ->
        val envelope = strict.decodeFromString(DatingErrorEnvelopeDto.serializer(), raw)
        assertThat(envelope.error).isNotNull()
        assertThat(envelope.meta).isNotNull()
        check(DatingError.from(statusOf(name), raw, strict), name)
    }

    private fun refusedCode(error: DatingError): String? = (error as? DatingError.Refused)?.code

    private fun <T> details(error: DatingError, serializer: KSerializer<T>, name: String): T {
        val details = checkNotNull((error as DatingError.Refused).details) { "$name has no details" }
        return strict.decodeFromJsonElement(serializer, details)
    }

    private fun <T> listSerializer(element: KSerializer<T>) = kotlinx.serialization.builtins.ListSerializer(element)
}
