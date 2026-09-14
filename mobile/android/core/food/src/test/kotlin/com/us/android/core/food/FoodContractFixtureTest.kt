package com.us.android.core.food

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.OnboardingStep
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.AcceptingDto
import com.us.android.core.food.network.ComplianceDto
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastInvoiceDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.OrderPaymentDto
import com.us.android.core.food.network.PaymentIntentDto
import com.us.android.core.food.network.DeliveryDocumentDto
import com.us.android.core.food.network.DeliveryKycDto
import com.us.android.core.food.network.DeliveryLocationDto
import com.us.android.core.food.network.DigiLockerStartDto
import com.us.android.core.food.network.RealtimeTokenDto
import com.us.android.core.food.network.VerifyDeliveryDto
import com.us.android.core.food.repository.code
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

        // ── Scoped realtime token (B5) ────────────────────────────────────
        "realtime_token_post_200_delivery.json" to data(RealtimeTokenDto.serializer()) {
            assertThat(it.scope).isEqualTo("delivery")
            assertThat(it.topics).containsExactly("food.delivery_partner.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0008.assignments")
            assertThat(it.ttlSeconds).isEqualTo(300)
            assertThat(it.expiresAt).isEqualTo("2026-09-13T06:35:00Z")
        },
        "realtime_token_post_200_order.json" to data(RealtimeTokenDto.serializer()) {
            assertThat(it.scope).isEqualTo("order")
            assertThat(it.topics).containsExactly("food.order.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015")
        },
        "realtime_token_post_200_restaurant.json" to data(RealtimeTokenDto.serializer()) {
            assertThat(it.scope).isEqualTo("restaurant")
            assertThat(it.topics).containsExactly(
                "food.restaurant.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002.orders",
                "food.restaurant.0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002",
            ).inOrder()
        },
        "realtime_token_post_400_invalid_body.json" to error { assertThat(it).isEqualTo(FoodError.InvalidBody) },
        "realtime_token_post_404_foreign_order.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "realtime_token_post_404_foreign_restaurant.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "realtime_token_post_404_not_delivery_partner.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "realtime_token_post_422_id_invalid.json" to coded("FOOD_REALTIME_ID_INVALID", FoodError.InvalidField::class.java),
        "realtime_token_post_422_id_not_allowed.json" to coded("FOOD_REALTIME_ID_NOT_ALLOWED", FoodError.InvalidField::class.java),
        "realtime_token_post_422_id_required.json" to coded("FOOD_REALTIME_ID_REQUIRED", FoodError.InvalidField::class.java),
        "realtime_token_post_422_scope_invalid.json" to coded("FOOD_REALTIME_SCOPE_INVALID", FoodError.InvalidField::class.java),
        "realtime_token_post_503_not_configured.json" to coded("FOOD_REALTIME_NOT_CONFIGURED", FoodError.Unexpected::class.java),

        // ── Rider: the job (B5c) ──────────────────────────────────────────
        "delivery_assignment_current_get_200_accepted.json" to data(DeliveryAssignmentDto.serializer()) {
            assertThat(it.status).isEqualTo("ACCEPTED")
            assertThat(it.pickupCode).isEqualTo("4821")
            assertThat(it.deliveryFee).isEqualTo(Paise(2_900))
            assertThat(it.deliveryPartnerPayout).isEqualTo(Paise(2_320))
        },
        "delivery_assignment_current_get_200_assigned.json" to data(DeliveryAssignmentDto.serializer()) {
            assertThat(it.status).isEqualTo("ASSIGNED")
            assertThat(it.pickupCode).isNull()
        },
        "delivery_location_post_200.json" to data(DeliveryLocationDto.serializer()) {
            assertThat(it.heading).isEqualTo(90.0)
            assertThat(it.accuracyMeters).isEqualTo(8.5)
            assertThat(it.assignmentIds).hasSize(2)
        },
        "delivery_verify_delivery_post_200.json" to data(VerifyDeliveryDto.serializer()) {
            assertThat(it.status).isEqualTo("DELIVERED")
        },
        "delivery_verify_delivery_post_400_invalid_assignment_id.json" to
            coded("INVALID_ASSIGNMENT_ID", FoodError.Unexpected::class.java),
        "delivery_verify_delivery_post_400_invalid_body.json" to error { assertThat(it).isEqualTo(FoodError.InvalidBody) },
        "delivery_verify_delivery_post_403_partner_not_active.json" to
            coded("FOOD_DELIVERY_PARTNER_NOT_ACTIVE", FoodError.Unexpected::class.java),
        "delivery_verify_delivery_post_404_not_your_assignment.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "delivery_verify_delivery_post_409_not_picked_up.json" to
            coded("FOOD_DELIVERY_ASSIGNMENT_NOT_READY", FoodError.Unexpected::class.java),
        "delivery_verify_delivery_post_422_code_invalid.json" to
            coded("FOOD_DELIVERY_CODE_INVALID", FoodError.InvalidField::class.java),
        "delivery_verify_delivery_post_429_attempts_exceeded.json" to
            coded("FOOD_DELIVERY_CODE_ATTEMPTS_EXCEEDED", FoodError.Unexpected::class.java),

        // ── Rider: documents (B4) ─────────────────────────────────────────
        "delivery_document_post_201_driving_licence.json" to data(DeliveryDocumentDto.serializer()) {
            assertThat(it.documentType).isEqualTo("DRIVING_LICENCE")
            assertThat(it.numberMasked).isEqualTo("****0001")
            assertThat(it.status).isEqualTo("PENDING")
        },
        "delivery_document_post_201_selfie.json" to data(DeliveryDocumentDto.serializer()) {
            assertThat(it.documentType).isEqualTo("SELFIE")
            assertThat(it.numberMasked).isNull()
        },
        "delivery_document_post_422_aadhaar.json" to error {
            assertThat(it).isEqualTo(
                FoodError.InvalidField("AADHAAR_NOT_ALLOWED", "document_number", "an Aadhaar number must not be submitted here"),
            )
        },
        "delivery_document_post_422_aadhaar_use_digilocker.json" to error {
            assertThat((it as FoodError.InvalidField).field).isEqualTo("document_type")
            assertThat(it.code).isEqualTo("FOOD_AADHAAR_USE_DIGILOCKER")
        },
        "delivery_document_post_422_selfie_number.json" to error {
            assertThat((it as FoodError.InvalidField).field).isEqualTo("document_number")
            assertThat(it.code).isEqualTo("FOOD_SELFIE_NUMBER_NOT_ALLOWED")
        },

        // ── Rider: DigiLocker and KYC status (B4) ─────────────────────────
        "kyc_digilocker_start_200.json" to data(DigiLockerStartDto.serializer()) {
            assertThat(it.state).isEqualTo("<state>")
            assertThat(it.authorizeUrl).contains("/v1/food/dev/digilocker/authorize?state=")
        },
        "kyc_digilocker_start_200_http.json" to data(DigiLockerStartDto.serializer()) {
            assertThat(it.authorizeUrl).contains("redirect_uri=https%3A%2F%2Fapi.example.test%2Fv1%2Ffood%2Fpublic%2Fdigilocker%2Freturn")
        },
        "kyc_digilocker_start_404_no_profile.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "kyc_digilocker_start_503_not_configured.json" to
            coded("FOOD_DIGILOCKER_NOT_CONFIGURED", FoodError.Unexpected::class.java),
        "kyc_digilocker_start_503_pii_not_configured.json" to error { assertThat(it).isEqualTo(FoodError.PiiNotConfigured) },
        "kyc_digilocker_callback_200.json" to data(DeliveryKycDto.serializer()) { kyc(it) },
        "kyc_digilocker_callback_403_state_not_yours.json" to
            coded("FOOD_DIGILOCKER_STATE_NOT_YOURS", FoodError.Unexpected::class.java),
        "kyc_digilocker_callback_409_document_in_use.json" to
            coded("FOOD_DOCUMENT_NUMBER_IN_USE", FoodError.Unexpected::class.java),
        "kyc_digilocker_callback_409_state_used.json" to
            coded("FOOD_DIGILOCKER_STATE_USED", FoodError.Unexpected::class.java),
        "kyc_digilocker_callback_410_state_expired.json" to
            coded("FOOD_DIGILOCKER_STATE_EXPIRED", FoodError.Unexpected::class.java),
        "kyc_digilocker_callback_422_code_required.json" to error {
            assertThat(it).isEqualTo(FoodError.InvalidField("FOOD_DIGILOCKER_CODE_REQUIRED", "code", "code is required"))
        },
        "kyc_digilocker_callback_422_state_invalid.json" to
            coded("FOOD_DIGILOCKER_STATE_INVALID", FoodError.InvalidField::class.java),
        "kyc_digilocker_callback_502_provider_failed.json" to
            coded("FOOD_DIGILOCKER_PROVIDER_FAILED", FoodError.Unexpected::class.java),
        "kyc_status_get_200.json" to data(DeliveryKycDto.serializer()) { kyc(it) },
        "kyc_status_get_404_no_profile.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        // ── Customer: cart, bill and pricing (Feast A5) ────────────────────
        "cart_get_200_section_9_5.json" to data(FeastCartDto.serializer()) {
            assertThat(it.totalsPaise?.finalAmountPaise).isEqualTo(Paise(64_912))
            assertThat(it.taxesAndCharges?.charges?.map { c -> c.kind })
                .containsExactly("PACKAGING", "PLATFORM_FEE", "DELIVERY_FEE").inOrder()
            assertThat(it.taxesAndCharges?.taxes?.map { t -> t.liability })
                .containsExactly("SUPPLIER", "ECO_SECTION_9_5").inOrder()
            assertThat(it.taxesAndCharges?.totalTaxesAndChargesPaise).isEqualTo(Paise(8_912))
            assertThat(it.taxesAndCharges?.taxes?.first()?.rates?.single()?.ratePercent).isEqualTo("18.00")
            assertThat(it.items.single().addons.single().lineTotalPaise).isEqualTo(Paise(6_000))
            assertThat(it.totals.taxTotal).isEqualTo(Paise(3_512))
            assertThat(it.pricingError).isNull()
        },
        "cart_get_200_supplier_liable.json" to data(FeastCartDto.serializer()) {
            assertThat(it.totalsPaise?.finalAmountPaise).isEqualTo(Paise(72_452))
            assertThat(it.taxesAndCharges?.taxes?.first()?.liableParty).isEqualTo("RESTAURANT")
            assertThat(it.items.single().taxAmountPaise).isEqualTo(Paise(10_080))
        },
        "cart_get_200_pricing_blocked.json" to data(FeastCartDto.serializer()) {
            assertThat(it.taxesAndCharges).isNull()
            assertThat(it.totalsPaise).isNull()
            assertThat(it.pricingError?.code).isEqualTo("FOOD_RESTAURANT_TAX_CATEGORY_MISSING")
            assertThat(it.pricingError?.message).isEqualTo("restaurant has no GST tax category and cannot take orders")
        },

        // ── Customer: orders ──────────────────────────────────────────────
        "order_get_200.json" to data(FeastOrderDto.serializer()) {
            assertThat(it.status).isEqualTo("CONFIRMED")
            assertThat(it.money?.totalsPaise?.finalAmountPaise).isEqualTo(Paise(64_912))
            assertThat(it.money?.taxesAndCharges?.totalTaxPaise).isEqualTo(Paise(3_512))
            assertThat(it.deliveryCode).isNull()
            assertThat(it.etaAt).isNull()
            assertThat(it.items.single().lineTotalPaise).isEqualTo(Paise(50_000))
            assertThat(it.history.single().toStatus).isEqualTo("PLACED")
        },
        "order_get_200_out_for_delivery.json" to data(FeastOrderDto.serializer()) {
            assertThat(it.status).isEqualTo("OUT_FOR_DELIVERY")
            assertThat(it.deliveryCode).isEqualTo("7390")
            assertThat(it.etaAt).isEqualTo("2026-09-13T06:52:00Z")
            assertThat(it.etaSource).isEqualTo("google")
            assertThat(it.money).isNull()
            assertThat(it.totals.finalAmount).isEqualTo(Paise(30_162))
        },
        "order_place_422_tax_category_missing.json" to
            coded("FOOD_RESTAURANT_TAX_CATEGORY_MISSING", FoodError.InvalidField::class.java),
        "order_place_503_platform_gstin_missing.json" to
            coded("FOOD_PLATFORM_GSTIN_NOT_CONFIGURED", FoodError.Unexpected::class.java),
        "invoice_get_200.json" to data(FeastInvoiceDto.serializer()) {
            assertThat(it.sections.map { s -> s.issuer }).containsExactly("RESTAURANT", "PLATFORM").inOrder()
            assertThat(it.sections.first().lines).hasSize(3)
            assertThat(it.sections.last().issuerGstin).isEqualTo("29ZZZCZ9999Z1ZF")
            assertThat(it.grandTotalPaise).isEqualTo(Paise(72_452))
            assertThat(it.buyer.city).isEqualTo("Bengaluru")
            assertThat(it.legacy).isFalse()
        },

        // ── Customer: payment ─────────────────────────────────────────────
        "order_payment_get_200_confirming.json" to data(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("confirming")
            assertThat(it.refundStatus).isNull()
            assertThat(it.amountMinor).isEqualTo(25_000L)
        },
        "order_payment_get_200_paid.json" to data(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
            assertThat(it.refundStatus).isNull()
        },
        "order_payment_get_200_failed.json" to data(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("failed")
        },
        "order_payment_get_200_paid_refund_pending.json" to data(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
            assertThat(it.refundStatus).isEqualTo("pending")
        },
        "order_payment_get_404.json" to error { assertThat(it).isEqualTo(FoodError.NotFound) },
        "order_payment_get_409_not_online.json" to coded("FOOD_PAYMENT_NOT_ONLINE", FoodError.Unexpected::class.java),
        "payment_intent_post_201_client_session.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.clientSession).containsExactly(
                "provider", "razorpay",
                "order_id", "order_ContractRzp01",
                "key_id", "rzp_test_ContractKey01",
                "merchant_display_name", "Momentum Merchant",
            )
            assertThat(it.paymentIntent?.amountMinor).isEqualTo(25_000L)
            assertThat(it.amount).isEqualTo(Paise(25_000))
            assertThat(it.orderId).isEqualTo("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030")
        },
        "payment_intent_post_201_client_session_no_merchant_name.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.clientSession).doesNotContainKey("merchant_display_name")
            assertThat(it.clientSession).containsKey("key_id")
        },
        "payment_intent_post_201_no_client_session.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.clientSession).isNull()
            assertThat(it.paymentIntent?.method).isEqualTo("card")
        },
        "payment_intent_post_422_cod.json" to coded("PAYMENT_METHOD_UNAVAILABLE", FoodError.InvalidField::class.java),
        "payment_intent_post_422_unknown_method.json" to coded("PAYMENT_METHOD_INVALID", FoodError.InvalidField::class.java),
        "payment_intent_post_422_wallet.json" to coded("PAYMENT_METHOD_UNAVAILABLE", FoodError.InvalidField::class.java),

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

    /** An error fixture whose stable code and failure kind are what a screen branches on. */
    private fun coded(code: String, kind: Class<out FoodError>): (String, String) -> Unit = error {
        assertThat(it).isInstanceOf(kind)
        assertThat(it.code).isEqualTo(code)
    }

    private fun kyc(it: DeliveryKycDto) {
        assertThat(it.status).isEqualTo("PENDING_REVIEW")
        assertThat(it.drivingDocumentsRequired).isTrue()
        assertThat(it.missing).containsExactly("selfie", "payout_account").inOrder()
        assertThat(it.checks.map { c -> c.kind }).containsExactly("AADHAAR", "DRIVING_LICENCE", "VEHICLE_RC").inOrder()
        assertThat(it.checks.first().validUntil).isNull()
        assertThat(it.documents.map { d -> d.numberMasked }).containsExactly("****0001", "****1234").inOrder()
        assertThat(it.hasPayoutAccount).isFalse()
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
