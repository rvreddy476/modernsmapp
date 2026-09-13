package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.OnboardingStep
import com.us.android.core.food.network.AcceptingDto
import com.us.android.core.food.network.ComplianceDto
import com.us.android.core.food.network.FoodErrorEnvelopeDto
import com.us.android.core.food.network.FssaiDto
import com.us.android.core.food.network.LocationDto
import com.us.android.core.food.network.OperatingHoursDto
import com.us.android.core.food.network.PayoutAccountDto
import com.us.android.core.food.network.RestaurantDocumentDto
import com.us.android.core.food.network.SubmitDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture from food-service decodes into its DTO.
 *
 * The fixtures are food-service's handler-test goldens
 * (internal/http/testdata/contracts, bd903746), copied byte for byte into
 * src/test/resources/contracts. Decoding is STRICT — unknown keys fail — so a
 * key renamed on either side, or a field the server added that the DTO does
 * not declare, fails here rather than defaulting silently in production.
 *
 * [parsers] must name every fixture: adding a fixture without a parser, or a
 * parser for a fixture that does not exist, fails the coverage test.
 */
class FoodContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (name: String, raw: String) -> Unit> = mapOf(
        "accepting_patch_200.json" to data(AcceptingDto.serializer()) {
            assertThat(it.status).isEqualTo("ACTIVE")
            assertThat(it.isAcceptingOrders).isTrue()
        },
        "accepting_patch_422_fssai_required.json" to error { assertThat(it).isEqualTo(FoodError.FssaiRequired) },
        "accepting_patch_422_not_live.json" to error { assertThat(it).isEqualTo(FoodError.NotLive) },
        "admin_document_decide_200.json" to data(RestaurantDocumentDto.serializer()) {
            assertThat(it.status).isEqualTo("APPROVED")
            assertThat(it.rejectionReason).isNull()
            assertThat(it.verifiedBy).isEqualTo("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0003")
        },
        "admin_document_decide_200_rejected.json" to data(RestaurantDocumentDto.serializer()) {
            assertThat(it.status).isEqualTo("REJECTED")
            assertThat(it.rejectionReason).isEqualTo("Licence photo is unreadable")
        },
        "admin_document_decide_422_expired.json" to error { assertThat(it).isEqualTo(FoodError.DocumentExpired) },
        "admin_document_decide_422_reason_required.json" to error {
            assertThat(it).isEqualTo(field("FOOD_REJECTION_REASON_REQUIRED", "reason"))
        },
        "compliance_put_200.json" to data(ComplianceDto.serializer()) {
            assertThat(it.gstin).isEqualTo("29ZZZPZ0000Z1Z6")
            assertThat(it.gstinStateCode).isEqualTo("29")
            assertThat(it.panMasked).isEqualTo("****000Z")
            assertThat(it.gstLiability).isEqualTo("SUPPLIER")
            assertThat(it.gstinRequired).isTrue()
            assertThat(it.specifiedPremisesDeclaredAt).isEqualTo("2026-09-01")
        },
        "compliance_put_200_eco_without_gstin.json" to data(ComplianceDto.serializer()) {
            assertThat(it.gstin).isNull()
            assertThat(it.gstLiability).isEqualTo("ECO_SECTION_9_5")
            assertThat(it.gstinRequired).isFalse()
        },
        "compliance_put_422_gstin_pan_mismatch.json" to error {
            assertThat(it).isEqualTo(field("FOOD_GSTIN_PAN_MISMATCH", "gstin"))
        },
        "compliance_put_422_gstin_required.json" to error {
            assertThat(it).isEqualTo(field("FOOD_GSTIN_REQUIRED", "gstin"))
        },
        "compliance_put_503_pii_not_configured.json" to error { assertThat(it).isEqualTo(FoodError.PiiNotConfigured) },
        "delivery_payout_account_get_200.json" to data(PayoutAccountDto.serializer()) {
            assertThat(it.ownerType).isEqualTo("DELIVERY_PARTNER")
            assertThat(it.accountNumberMasked).isEqualTo("****6789")
            assertThat(it.verifiedAt).isNull()
        },
        "delivery_payout_account_put_200.json" to data(PayoutAccountDto.serializer()) {
            assertThat(it.holderName).isEqualTo("Test Rider")
            assertThat(it.ifsc).isEqualTo("SBIN0001234")
        },
        "fssai_put_200.json" to data(FssaiDto.serializer()) {
            assertThat(it.fssaiExpiresAt).isEqualTo("2027-03-31")
            assertThat(it.document?.status).isEqualTo("PENDING")
            assertThat(it.document?.verifiedBy).isNull()
        },
        "fssai_put_422_expiry.json" to error {
            assertThat(it).isEqualTo(field("FOOD_FSSAI_EXPIRY_INVALID", "expires_at"))
        },
        "location_put_200.json" to data(LocationDto.serializer()) {
            assertThat(it.latitude).isEqualTo(12.9716)
            assertThat(it.deliveryRadiusKm).isEqualTo(6.5)
            assertThat(it.serviceAreaId).isEqualTo("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0005")
        },
        "location_put_422_radius.json" to error {
            assertThat(it).isEqualTo(field("FOOD_DELIVERY_RADIUS_OUT_OF_RANGE", "delivery_radius_km"))
        },
        "onboarding_400_invalid_body.json" to error { assertThat(it).isEqualTo(FoodError.InvalidBody) },
        "onboarding_404_not_owner.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "operating_hours_put_200.json" to data(OperatingHoursDto.serializer()) {
            assertThat(it.timezone).isEqualTo("Asia/Kolkata")
            assertThat(it.windows).hasSize(4)
            assertThat(it.windows.first().isClosed).isTrue()
            assertThat(it.windows.last().overnight).isTrue()
            assertThat(it.windows.last().closesAt).isEqualTo("02:00")
        },
        "operating_hours_put_422_day.json" to error {
            assertThat(it).isEqualTo(field("FOOD_OPERATING_HOURS_DAY_INVALID", "windows[0].day_of_week"))
        },
        "payout_account_get_404.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "payout_account_put_422_ifsc.json" to error { assertThat(it).isEqualTo(field("INVALID_IFSC", "ifsc")) },
        "payout_account_put_503_pii_not_configured.json" to error {
            assertThat(it).isEqualTo(FoodError.PiiNotConfigured)
        },
        "restaurant_payout_account_get_200.json" to data(PayoutAccountDto.serializer()) {
            assertThat(it.ownerType).isEqualTo("RESTAURANT")
            assertThat(it.verificationReason).isEqualTo("verification_pending_ops")
        },
        "restaurant_payout_account_put_200.json" to data(PayoutAccountDto.serializer()) {
            assertThat(it.ownerType).isEqualTo("RESTAURANT")
            assertThat(it.verificationStatus).isEqualTo("NOT_VERIFIED")
        },
        "submit_post_200.json" to data(SubmitDto.serializer()) {
            assertThat(it.status).isEqualTo("PENDING_REVIEW")
            assertThat(it.missing).isEmpty()
        },
        "submit_post_409_not_draft.json" to error { assertThat(it).isEqualTo(FoodError.NotDraft) },
        "submit_post_422_not_ready.json" to error {
            assertThat(it).isEqualTo(FoodError.NotReady(listOf("fssai_document", "payout_account")))
            val checklist = (it as FoodError.NotReady).checklist
            assertThat(checklist.isReady).isFalse()
            assertThat(checklist.nextStep).isEqualTo(OnboardingStep.FSSAI_DOCUMENT)
        },
    )

    @Test
    fun `every fixture has a parser and every parser has a fixture`() {
        assertThat(contractsDir.isDirectory).isTrue()
        val fixtures = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

        assertThat(fixtures).isNotEmpty()
        assertThat(fixtures - parsers.keys).isEmpty()
        assertThat(parsers.keys - fixtures).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        for ((name, parse) in parsers) {
            val raw = File(contractsDir, name).readText()
            try {
                parse(name, raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    @Test
    fun `the copies are byte-identical to the food-service goldens`() {
        val source = File("../../../../Architecture/services/food-service/internal/http/testdata/contracts")
        assumeTrue("food-service is not checked out beside the app", source.isDirectory)

        for (copy in contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty()) {
            val original = File(source, copy.name)
            assertThat(original.exists()).isTrue()
            assertThat(copy.readBytes()).isEqualTo(original.readBytes())
        }
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String, String) -> Unit = { _, raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        assertThat(envelope.error).isNull()
        check(checkNotNull(envelope.data))
    }

    private fun error(check: (FoodError) -> Unit): (String, String) -> Unit = { name, raw ->
        val status = checkNotNull(STATUS.find(name)) { "fixture name $name carries no HTTP status" }
            .groupValues[1].toInt()
        val envelope = strict.decodeFromString(FoodErrorEnvelopeDto.serializer(), raw)
        check(FoodError.from(status, checkNotNull(envelope.error)))
    }

    private fun field(code: String, field: String): FoodError = FoodError.InvalidField(
        code = code,
        field = field,
        message = MESSAGES.getValue(code),
    )

    private companion object {
        val STATUS = Regex("""_(\d{3})(?:_|\.json)""")
        val MESSAGES = mapOf(
            "FOOD_REJECTION_REASON_REQUIRED" to "reason is required when rejecting a document",
            "FOOD_GSTIN_PAN_MISMATCH" to "the PAN inside gstin does not match pan",
            "FOOD_GSTIN_REQUIRED" to "gstin is required: this tax category leaves the restaurant liable for GST",
            "FOOD_FSSAI_EXPIRY_INVALID" to "expires_at must be after today",
            "FOOD_DELIVERY_RADIUS_OUT_OF_RANGE" to "delivery_radius_km must be between 1 and 15",
            "FOOD_OPERATING_HOURS_DAY_INVALID" to "day_of_week must be 0 (Sunday) to 6 (Saturday)",
            "INVALID_IFSC" to "ifsc is not a well-formed IFSC",
        )
    }
}
