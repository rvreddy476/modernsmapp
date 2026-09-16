package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.network.AllowedDetailsDto
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
 * (internal/http/testdata/contracts, 55b06b4a), copied byte for byte into
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
            assertThat(card.profile.primaryPhotoUrl).isEqualTo("/v1/dating/photos/<uuid>/full")
            assertThat(card.profile.tuneSummary).isEmpty()
            assertThat(card.echoes?.topReelId).isNull()
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
            assertThat(details(error, RateLimitDetailsDto.serializer(), name)).isEqualTo(RateLimitDetailsDto(50, 24))
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
            assertThat(person.photoState).isEqualTo("full")
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
        },
        "trusted_contacts_get_200_profile_gone.json" to data(TrustedContactsDto.serializer()) {
            // The field is always PRESENT and null when the profile was purged.
            val contact = it.items.single()
            assertThat(contact.contactId).isEqualTo("<contact>")
            assertThat(contact.person).isNull()
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
        parsers.keys.filter { statusOf(it) < 300 && it != "pulse_today_get_200.json" }.forEach { name ->
            val envelope = production.decodeFromString(ApiEnvelope.serializer(kotlinx.serialization.json.JsonElement.serializer()), fixture(name))
            assertThat(envelope.data).isNotNull()
        }
        assertThat(production.decodeFromString(PulseTodayDto.serializer(), fixture("pulse_today_get_200.json")).data).hasSize(1)
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
