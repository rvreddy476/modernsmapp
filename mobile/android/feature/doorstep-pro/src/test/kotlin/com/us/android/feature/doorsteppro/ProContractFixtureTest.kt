package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.doorsteppro.data.DayOffDto
import com.us.android.feature.doorsteppro.data.DigiLockerStartDto
import com.us.android.feature.doorsteppro.data.DutyStateDto
import com.us.android.feature.doorsteppro.data.EarningsDto
import com.us.android.feature.doorsteppro.data.ExtraDto
import com.us.android.feature.doorsteppro.data.ExtraOptionDto
import com.us.android.feature.doorsteppro.data.IncidentDto
import com.us.android.feature.doorsteppro.data.KycCheckDto
import com.us.android.feature.doorsteppro.data.MessageDto
import com.us.android.feature.doorsteppro.data.MessagePageDto
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.PayoutAccountDto
import com.us.android.feature.doorsteppro.data.PhotoDto
import com.us.android.feature.doorsteppro.data.ProAreaDto
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProDocumentDto
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProErrorEnvelopeDto
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.data.ProJobPageDto
import com.us.android.feature.doorsteppro.data.ProListDto
import com.us.android.feature.doorsteppro.data.ProReadinessDto
import com.us.android.feature.doorsteppro.data.ProSkillDto
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import com.us.android.feature.doorsteppro.data.RatingDto
import com.us.android.feature.doorsteppro.data.RealtimeTokenDto
import com.us.android.feature.doorsteppro.data.ServiceabilityDto
import com.us.android.feature.doorsteppro.data.SkillDto
import com.us.android.feature.doorsteppro.data.WeeklyHoursDto
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.detailInt
import com.us.android.feature.doorsteppro.data.detailText
import com.us.android.feature.doorsteppro.data.field
import com.us.android.feature.doorsteppro.deeplink.DigiLockerReturnLink
import com.us.android.feature.doorsteppro.domain.AreaRules
import com.us.android.feature.doorsteppro.domain.HoursRules
import com.us.android.feature.doorsteppro.domain.OnboardingChecklist
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.domain.ProAgreement
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.domain.StepState
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture doorstep-service publishes for the
 * professional routes decodes into its DTO and maps into what the screens show.
 *
 * The fixtures are doorstep-service's handler-test goldens
 * (internal/http/testdata/contracts/pro_*.json, plus the two serviceability
 * goldens the service-area step reads), copied BYTE FOR BYTE into
 * src/test/resources/contracts — asserted below against the backend tree when
 * it is checked out beside this one. Decoding is STRICT on two counts:
 * unknown keys fail, and every key the OpenAPI schema requires has no default
 * in the DTO, so a dropped key fails too — the contract emits explicit nulls.
 *
 * [parsers] must name every fixture in the directory and every parser must
 * have its fixture; [pending] names the routes the app calls whose fixtures
 * doorstep-service has not published yet (lanes A4 and A5, contract-only on
 * 2026-10-04). The admin and customer fixtures are not copied.
 */
class ProContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    /** The backend's goldens, when the monorepo is checked out whole. */
    private val backendDir = File("../../../../Architecture/services/doorstep-service/internal/http/testdata/contracts")

    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // ── Apply and profile ──
        "pro_apply_201.json" to data(ProfessionalDto.serializer()) { dto ->
            assertThat(ProStatus.of(dto.status)).isEqualTo(ProStatus.DRAFT)
            assertThat(dto.gender).isNull()
            assertThat(dto.ratingAvg).isNull()
            assertThat(dto.cityCode).isEqualTo("HYD")
        },
        "pro_apply_400_gender_field.json" to error(400) { assertThat(it.code).isEqualTo(ProCodes.INVALID_REQUEST) },
        "pro_apply_409_exists.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.PRO_EXISTS) },
        "pro_me_get_200.json" to data(ProfessionalDto.serializer()) { assertThat(it.photoMediaId).isNotNull() },
        "pro_me_get_404.json" to error(404) { assertThat(it.code).isEqualTo(ProCodes.PRO_NOT_FOUND) },
        "pro_me_patch_200.json" to data(ProfessionalDto.serializer()) { assertThat(it.displayName).isEqualTo("Lakshmi Devi") },
        "pro_me_patch_409_blocked.json" to error(409) {
            assertThat(it.code).isEqualTo(ProCodes.INVALID_TRANSITION)
            assertThat(it.detailText("status")).isEqualTo("blocked")
        },
        // ── Readiness ──
        "pro_readiness_200_draft.json" to data(ProReadinessDto.serializer()) { dto ->
            val checklist = OnboardingChecklist.of(dto)
            assertThat(checklist.status).isEqualTo(ProStatus.DRAFT)
            assertThat(checklist.next).isEqualTo(OnboardingStep.PROFILE)
            assertThat(checklist.items.first { it.step == OnboardingStep.SKILLS }.state).isEqualTo(StepState.DONE)
            // The selfie waits for Aadhaar.
            assertThat(checklist.items.first { it.step == OnboardingStep.SELFIE }.state).isEqualTo(StepState.WAITING)
            assertThat(checklist.items.last().state).isEqualTo(StepState.OPTIONAL)
            assertThat(checklist.canGoOnDuty).isFalse()
        },
        "pro_readiness_200_pending_review.json" to data(ProReadinessDto.serializer()) { dto ->
            val checklist = OnboardingChecklist.of(dto)
            assertThat(checklist.waitingForReview).isTrue()
            assertThat(checklist.items.first { it.step == OnboardingStep.POLICE_CERTIFICATE }.state).isEqualTo(StepState.IN_REVIEW)
            assertThat(checklist.next).isNull()
            assertThat(dto.recommendedSteps).isEmpty()
        },
        "pro_readiness_200_approved.json" to data(ProReadinessDto.serializer()) { dto ->
            val checklist = OnboardingChecklist.of(dto)
            assertThat(checklist.status).isEqualTo(ProStatus.APPROVED)
            assertThat(checklist.items.all { it.state == StepState.DONE }).isTrue()
            assertThat(checklist.canGoOnDuty).isTrue()
        },
        // ── DigiLocker ──
        "pro_digilocker_start_200.json" to data(DigiLockerStartDto.serializer()) { dto ->
            // Mock mode: the authorization URL is the return link itself, which the app claims.
            val link = DigiLockerReturnLink.parse(dto.authorizationUrl, allowCustomScheme = false)
            assertThat(link).isNotNull()
            assertThat(link!!.state).isEqualTo(dto.state)
            assertThat(link.code).isEqualTo("mock-female")
        },
        "pro_digilocker_start_503.json" to error(503) { assertThat(it.code).isEqualTo(ProCodes.DIGILOCKER_UNAVAILABLE) },
        "pro_digilocker_callback_200.json" to data(ProReadinessDto.serializer()) { dto ->
            val checklist = OnboardingChecklist.of(dto)
            assertThat(checklist.items.first { it.step == OnboardingStep.AADHAAR }.state).isEqualTo(StepState.DONE)
            assertThat(checklist.items.first { it.step == OnboardingStep.SELFIE }.state).isEqualTo(StepState.TO_DO)
            assertThat(checklist.next).isEqualTo(OnboardingStep.SELFIE)
        },
        "pro_digilocker_callback_400_state.json" to error(400) { assertThat(it.field).isEqualTo("state") },
        // ── Selfie ──
        "pro_selfie_200.json" to data(KycCheckDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("passed")
            assertThat(dto.score).isEqualTo(96.0)
        },
        "pro_selfie_200_pending.json" to data(KycCheckDto.serializer()) { assertThat(it.verifiedAt).isNull() },
        "pro_selfie_422_failed.json" to error(422) { assertThat(it.code).isEqualTo(ProCodes.FACE_MATCH_FAILED) },
        "pro_selfie_422_no_aadhaar.json" to error(422) { assertThat(it.code).isEqualTo(ProCodes.ONBOARDING_INCOMPLETE) },
        // ── Skills ──
        "pro_skills_list_200.json" to data(ProListDto.serializer(SkillDto.serializer())) { dto ->
            assertThat(dto.items.first { it.code == "electrician" }.requiresCertificate).isTrue()
            assertThat(dto.items.first { it.code == "deep_cleaning" }.requiresCertificate).isFalse()
        },
        "pro_skills_put_200.json" to data(ProListDto.serializer(ProSkillDto.serializer())) { dto ->
            assertThat(dto.items.map { it.status }).containsExactly("verified", "pending").inOrder()
        },
        "pro_skills_put_400_unknown.json" to error(400) { assertThat(it.field).isEqualTo("skill_codes") },
        "pro_skills_put_403_gender.json" to error(403) { assertThat(it.code).isEqualTo(ProCodes.GENDER_RULE) },
        "pro_trade_certificate_201.json" to data(ProDocumentDto.serializer()) { dto ->
            assertThat(dto.kind).isEqualTo("trade_certificate")
            assertThat(dto.skillCode).isEqualTo("electrician")
            assertThat(dto.expiresOn).isNull()
        },
        "pro_trade_certificate_400_not_required.json" to error(400) { assertThat(it.field).isEqualTo("code") },
        // ── Area ──
        "pro_area_put_200.json" to data(ProAreaDto.serializer()) { dto ->
            assertThat(AreaRules.kmOf(dto.radiusM)).isEqualTo(8)
            assertThat(dto.zoneIds).hasSize(1)
        },
        "pro_area_put_400_radius.json" to error(400) { err ->
            assertThat(err.field).isEqualTo("radius_m")
            // The app's own clamp agrees with the server's 1–15 km.
            assertThat(AreaRules.radiusMeters(AreaRules.MAX_KM)).isEqualTo(15_000)
            assertThat(AreaRules.radiusMeters(AreaRules.MIN_KM)).isEqualTo(1_000)
        },
        "pro_area_put_400_zone.json" to error(400) { assertThat(it.field).isEqualTo("zone_ids") },
        "serviceability_in_200.json" to data(ServiceabilityDto.serializer()) { dto ->
            assertThat(dto.serviceable).isTrue()
            assertThat(dto.zone?.id).isEqualTo("ab4daa10-2d55-51d8-ac2a-f7ca4179e957")
        },
        "serviceability_out_200.json" to data(ServiceabilityDto.serializer()) { dto ->
            assertThat(dto.serviceable).isFalse()
            assertThat(dto.zone).isNull()
        },
        // ── Hours and days off ──
        "pro_hours_get_200.json" to data(WeeklyHoursDto.serializer()) { dto ->
            val windows = dto.items.mapNotNull(HoursRules::fromDto)
            assertThat(windows).hasSize(3)
            // Two windows on Monday, as the editor allows.
            assertThat(windows.count { it.weekday == 1 }).isEqualTo(2)
            assertThat(HoursRules.validate(windows)).isNull()
        },
        "pro_hours_put_200.json" to data(WeeklyHoursDto.serializer()) { dto ->
            assertThat(dto.items.mapNotNull(HoursRules::fromDto).map { it.toDto() }).isEqualTo(dto.items)
        },
        "pro_hours_put_400_overlap.json" to error(400) { assertThat(it.field).isEqualTo("items") },
        "pro_days_off_list_200.json" to data(ProListDto.serializer(DayOffDto.serializer())) { dto ->
            assertThat(dto.items.map { it.reason }).containsExactly("Family function", null).inOrder()
        },
        "pro_days_off_post_201.json" to data(DayOffDto.serializer()) { assertThat(it.date).isEqualTo("2026-10-10") },
        "pro_days_off_post_400_past.json" to error(400) { assertThat(it.field).isEqualTo("date") },
        "pro_days_off_post_409_job.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.CONFLICT) },
        "pro_days_off_delete_404.json" to error(404) { assertThat(it.code).isEqualTo(ProCodes.NOT_FOUND) },
        // ── Bank, police certificate, agreement, PAN ──
        "pro_bank_put_200.json" to data(PayoutAccountDto.serializer()) { dto ->
            assertThat(dto.accountLast4).hasLength(4)
            assertThat(dto.status).isEqualTo("pending")
        },
        "pro_bank_put_400_ifsc.json" to error(400) { assertThat(it.field).isEqualTo("ifsc") },
        "pro_bank_put_503_pii.json" to error(503) { assertThat(it.code).isEqualTo(ProCodes.PII_UNAVAILABLE) },
        "pro_police_certificate_201.json" to data(ProDocumentDto.serializer()) { dto ->
            assertThat(dto.kind).isEqualTo("police_certificate")
            assertThat(dto.skillCode).isNull()
            assertThat(dto.expiresOn).isEqualTo("2027-09-20")
        },
        "pro_police_certificate_400_old.json" to error(400) { assertThat(it.field).isEqualTo("issued_on") },
        "pro_police_certificate_409_pending.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.CONFLICT) },
        "pro_agreement_200.json" to data(ProReadinessDto.serializer()) { dto ->
            assertThat(OnboardingStep.AGREEMENT.wire).isIn(dto.completedSteps)
        },
        "pro_agreement_400_version.json" to error(400) { err ->
            assertThat(err.field).isEqualTo("version")
            // The app sends the version the server currently requires.
            assertThat(err.detailText("current_version")).isEqualTo(ProAgreement.VERSION)
        },
        "pro_pan_put_200.json" to data(ProReadinessDto.serializer()) { dto ->
            val pan = OnboardingChecklist.of(dto).items.first { it.step == OnboardingStep.PAN }
            assertThat(pan.state).isEqualTo(StepState.DONE)
        },
        "pro_pan_put_400.json" to error(400) { assertThat(it.field).isEqualTo("pan") },
    )

    /**
     * Routes the app calls whose fixtures doorstep-service has NOT published
     * yet (lanes A4/A5 are contract-only as of 2026-10-04, and two routes are
     * PROPOSED, not even in openapi.yaml). The names are what we expect;
     * rename here when the backend publishes. Each is ASSUMED away until its
     * file appears, then it must decode strictly.
     *
     *  A4 — POST /pro/duty/on 200 · /duty/off 200     pro_duty_on_200.json, pro_duty_off_200.json,
     *                                                   pro_duty_on_403_not_approved.json
     *       POST /pro/location 409                      pro_location_409_not_on_duty.json
     *       GET  /pro/offers 200                        pro_offers_list_200.json
     *       POST /pro/offers/{id}/accept 200/409/410    pro_offer_accept_200.json, pro_offer_accept_409_taken.json,
     *                                                   pro_offer_accept_410_expired.json
     *       GET  /pro/jobs 200 · /pro/jobs/{id} 200     pro_jobs_list_200.json, pro_job_get_200.json
     *       POST /pro/realtime/token 200                pro_realtime_token_200.json
     *  A5 — POST /pro/jobs/{id}/en-route|arrived 200    pro_job_en_route_200.json, pro_job_arrived_200.json,
     *                                                   pro_job_arrived_422_geo.json
     *       POST /pro/jobs/{id}/photos 201              pro_job_photo_201.json
     *       POST /pro/jobs/{id}/start 200/422/423       pro_job_start_200.json, pro_job_start_422_otp.json,
     *                                                   pro_job_start_422_photos.json, pro_job_start_423_locked.json
     *       POST /pro/jobs/{id}/extras 201              pro_job_extra_post_201.json
     *       POST /pro/jobs/{id}/finish 200/409          pro_job_finish_200.json, pro_job_finish_409_extras_pending.json
     *       POST /pro/jobs/{id}/complete 200            pro_job_complete_200.json
     *       POST /pro/jobs/{id}/no-show 200             pro_job_no_show_200.json
     *       POST /pro/jobs/{id}/sos|unsafe-exit 201     pro_job_sos_201.json, pro_job_unsafe_exit_201.json
     *       POST /pro/jobs/{id}/rating 201              pro_job_rating_201.json
     *       GET|POST /pro/jobs/{id}/messages            pro_job_messages_200.json, pro_job_message_post_201.json
     *       GET  /pro/earnings 200                      pro_earnings_200.json
     *  PROPOSED (not in openapi.yaml):
     *       GET  /pro/jobs/{id}/extras/options 200      pro_job_extra_options_200.json
     *       GET  /pro/jobs/{id}/extras 200              pro_job_extras_200.json
     */
    private val pending: Map<String, (raw: String) -> Unit> = mapOf(
        "pro_duty_on_200.json" to data(DutyStateDto.serializer()) { assertThat(it.onDuty).isTrue() },
        "pro_duty_off_200.json" to data(DutyStateDto.serializer()) { assertThat(it.onDuty).isFalse() },
        "pro_duty_on_403_not_approved.json" to error(403) { assertThat(it.code).isEqualTo(ProCodes.PRO_NOT_APPROVED) },
        "pro_location_409_not_on_duty.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.NOT_ON_DUTY) },
        "pro_offers_list_200.json" to data(ProListDto.serializer(OfferDto.serializer())) {},
        "pro_offer_accept_200.json" to data(ProJobDto.serializer()) { assertThat(it.address).isNotNull() },
        "pro_offer_accept_409_taken.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.OFFER_TAKEN) },
        "pro_offer_accept_410_expired.json" to error(410) { assertThat(it.code).isEqualTo(ProCodes.OFFER_EXPIRED) },
        "pro_jobs_list_200.json" to data(ProJobPageDto.serializer()) {},
        "pro_job_get_200.json" to data(ProJobDto.serializer()) {},
        "pro_realtime_token_200.json" to data(RealtimeTokenDto.serializer()) { dto ->
            assertThat(dto.topics.any { it.startsWith("doorstep.pro.") }).isTrue()
        },
        "pro_job_en_route_200.json" to data(ProJobDto.serializer()) { assertThat(it.status).isEqualTo("en_route") },
        "pro_job_arrived_200.json" to data(ProJobDto.serializer()) { assertThat(it.status).isEqualTo("arrived") },
        "pro_job_arrived_422_geo.json" to error(422) { err ->
            assertThat(err.code).isEqualTo(ProCodes.GEO_CHECK_FAILED)
            assertThat(err.detailInt("max_distance_m")).isNotNull()
        },
        "pro_job_photo_201.json" to data(PhotoDto.serializer()) {},
        "pro_job_start_200.json" to data(ProJobDto.serializer()) { assertThat(it.status).isEqualTo("in_progress") },
        "pro_job_start_422_otp.json" to error(422) { err ->
            assertThat(err.code).isEqualTo(ProCodes.OTP_INVALID)
            assertThat(err.detailInt("attempts_left")).isNotNull()
        },
        "pro_job_start_422_photos.json" to error(422) { err ->
            assertThat(err.code).isEqualTo(ProCodes.PHOTOS_REQUIRED)
            assertThat(err.detailText("phase")).isNotNull()
        },
        "pro_job_start_423_locked.json" to error(423) { err ->
            assertThat(err.code).isEqualTo(ProCodes.OTP_LOCKED)
            assertThat(err.detailText("locked_until")).isNotNull()
        },
        "pro_job_extra_post_201.json" to data(ExtraDto.serializer()) { assertThat(it.status).isEqualTo("proposed") },
        "pro_job_finish_200.json" to data(ProJobDto.serializer()) {},
        "pro_job_finish_409_extras_pending.json" to error(409) { assertThat(it.code).isEqualTo(ProCodes.EXTRAS_PENDING) },
        "pro_job_complete_200.json" to data(ProJobDto.serializer()) { assertThat(it.status).isEqualTo("completed") },
        "pro_job_no_show_200.json" to data(ProJobDto.serializer()) { assertThat(it.status).isEqualTo("customer_no_show") },
        "pro_job_sos_201.json" to data(IncidentDto.serializer()) { assertThat(it.raisedByKind).isEqualTo("pro") },
        "pro_job_unsafe_exit_201.json" to data(IncidentDto.serializer()) { assertThat(it.kind).isEqualTo("unsafe_exit") },
        "pro_job_rating_201.json" to data(RatingDto.serializer()) { assertThat(it.raterKind).isEqualTo("pro") },
        "pro_job_messages_200.json" to data(MessagePageDto.serializer()) {},
        "pro_job_message_post_201.json" to data(MessageDto.serializer()) { assertThat(it.senderKind).isEqualTo("pro") },
        "pro_earnings_200.json" to data(EarningsDto.serializer()) {},
        "pro_job_extra_options_200.json" to data(ProListDto.serializer(ExtraOptionDto.serializer())) {},
        "pro_job_extras_200.json" to data(ProListDto.serializer(ExtraDto.serializer())) {},
    )

    private fun fixtures(): Set<String> = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser and every parser a fixture`() {
        val onDisk = fixtures()
        assertThat(onDisk).isNotEmpty()
        val known = parsers.keys + pending.keys
        assertThat(onDisk - known).isEmpty()
        assertThat(parsers.keys - onDisk).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        for ((name, parse) in parsers) {
            val raw = File(contractsDir, name).readText()
            try {
                parse(raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    @Test
    fun `pending fixtures decode once the backend publishes them`() {
        val published = pending.keys.filter { File(contractsDir, it).exists() }
        assumeTrue("none of the pending fixtures is published yet: ${pending.keys}", published.isNotEmpty())
        for (name in published) {
            try {
                pending.getValue(name)(File(contractsDir, name).readText())
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    /** Byte for byte what doorstep-service's handler tests write — and every pro_* golden the backend has is copied. */
    @Test
    fun `the copies are byte-identical to doorstep-service's goldens`() {
        assumeTrue("backend tree not checked out beside the Android tree", backendDir.isDirectory)
        for (name in fixtures()) {
            val golden = File(backendDir, name)
            assertThat(golden.exists()).isTrue()
            assertThat(File(contractsDir, name).readBytes().contentEquals(golden.readBytes())).isTrue()
        }
        val backendPro = backendDir.listFiles { f -> f.name.startsWith("pro_") && f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()
        assertThat(backendPro - fixtures()).isEmpty()
    }

    /** The strictness itself: a dropped required key and an unknown key both fail. */
    @Test
    fun `a dropped required key or an unknown key fails the strict decode`() {
        val raw = File(contractsDir, "pro_me_get_200.json").readText()
        val dropped = raw.replace(""","gender":null""", "")
        assertThat(dropped).isNotEqualTo(raw)
        assertThat(runCatching { data(ProfessionalDto.serializer()) {}(dropped) }.isFailure).isTrue()
        val added = raw.replace(""""status":"draft"""", """"status":"draft","surprise":1""")
        assertThat(added).isNotEqualTo(raw)
        assertThat(runCatching { data(ProfessionalDto.serializer()) {}(added) }.isFailure).isTrue()
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        check(checkNotNull(envelope.data))
    }

    /** [status] is the HTTP status the Go test asserts for the route; the fixture body carries the envelope only. */
    private fun error(status: Int, check: (ProError) -> Unit): (String) -> Unit = { raw ->
        strict.decodeFromString(ProErrorEnvelopeDto.serializer(), raw)
        val err = ProError.from(status, raw, strict)
        assertThat(err).isInstanceOf(ProError.Refused::class.java)
        check(err)
    }
}
