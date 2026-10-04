package com.us.android.feature.doorstep

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.AddressListDto
import com.us.android.feature.doorstep.data.BookingCreatedDto
import com.us.android.feature.doorstep.data.BookingDto
import com.us.android.feature.doorstep.data.BookingPageDto
import com.us.android.feature.doorstep.data.BookingPaymentsDto
import com.us.android.feature.doorstep.data.CancelPreviewDto
import com.us.android.feature.doorstep.data.CatalogueDto
import com.us.android.feature.doorstep.data.CategoryPageDto
import com.us.android.feature.doorstep.data.CheckoutSessionDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepErrorEnvelopeDto
import com.us.android.feature.doorstep.data.ExtraDto
import com.us.android.feature.doorstep.data.ExtraListDto
import com.us.android.feature.doorstep.data.ExtrasBillDto
import com.us.android.feature.doorstep.data.IncidentDto
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.PaymentIntentDto
import com.us.android.feature.doorstep.data.QuoteDto
import com.us.android.feature.doorstep.data.RatingDto
import com.us.android.feature.doorstep.data.RealtimeTokenDto
import com.us.android.feature.doorstep.data.ReworkListDto
import com.us.android.feature.doorstep.data.ReworkRequestDto
import com.us.android.feature.doorstep.data.ServicePageDto
import com.us.android.feature.doorstep.data.ServiceabilityDto
import com.us.android.feature.doorstep.data.ShareTokenDto
import com.us.android.feature.doorstep.data.SlotDaysDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.domain.SelectionRules
import com.us.android.feature.doorstep.domain.StepState
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.sum
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.payment.CheckoutRoute
import com.us.android.feature.doorstep.payment.DoorstepPaymentConfig
import com.us.android.feature.doorstep.payment.DoorstepReference
import com.us.android.feature.doorstep.payment.checkoutRoute
import com.us.android.feature.doorstep.payment.readingFor
import com.us.android.core.payments.PaymentStatusReading
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture doorstep-service publishes for the customer
 * routes decodes into its DTO and maps into what the screens show.
 *
 * The fixtures are doorstep-service's handler-test goldens
 * (internal/http/testdata/contracts), copied BYTE FOR BYTE into
 * src/test/resources/contracts and keyed here on the backend's file names.
 * Decoding is STRICT on two counts: unknown keys fail (a field the server
 * added that the DTO does not declare), and every key the OpenAPI schema
 * requires has no default in the DTO (a key the server dropped fails too) —
 * the contract emits explicit nulls, never omits.
 *
 * [parsers] must name every fixture in the directory and every parser must
 * have its fixture; [pending] names the routes the app calls whose fixtures
 * the backend has not published yet. The admin fixtures are the console's,
 * not the app's, and are not copied.
 */
class DoorstepContractFixtureTest {

    private companion object {
        // The dev seed's deterministic ids (devseed.ID) the A3 fixtures carry.
        const val FIXTURE_ADDRESS_ID = "e4329848-a88b-5702-87c4-4baa7bb8e509"
        const val FIXTURE_BOOKING_ID = "b5dbe726-3083-5696-8af3-e63f4620137e"
    }

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // GET /v1/doorstep/catalogue?city=HYD 200
        "catalogue_get_200.json" to data(CatalogueDto.serializer()) { dto ->
            assertThat(dto.city.code).isEqualTo("HYD")
            assertThat(dto.categories.map { it.slug }).containsExactly("home-cleaning", "salon-women", "salon-men").inOrder()
            val cleaning = dto.categories.first()
            assertThat(cleaning.imageUrl).isNull()
            assertThat(Paise(cleaning.startingPricePaise).toRupeeText()).isEqualTo("₹249.00")
            assertThat(dto.categories.map { it.genderRule })
                .containsExactly(GenderRules.ANY, GenderRules.FEMALE_ONLY, GenderRules.MALE_ONLY).inOrder()
        },
        // GET /v1/doorstep/catalogue?city=XXX 404
        "catalogue_get_404_city.json" to error(status = 404) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.CITY_NOT_FOUND)
        },
        // GET /v1/doorstep/categories/salon-women?city=HYD 200
        "category_get_200.json" to data(CategoryPageDto.serializer()) { dto ->
            assertThat(dto.category.slug).isEqualTo("salon-women")
            assertThat(dto.services).hasSize(4)
            assertThat(dto.services.first { it.slug == "waxing" }.startingMrpPaise).isEqualTo(99900L)
            assertThat(dto.services.first { it.slug == "facial" }.startingMrpPaise).isNull()
        },
        // GET /v1/doorstep/services/{facial}?city=HYD 200 — a required pick-one mask group plus optional add-ons
        "service_get_200.json" to data(ServicePageDto.serializer()) { dto ->
            val service = dto.service
            assertThat(service.category.extrasPolicy).isEqualTo("catalogue_addons_only")
            assertThat(service.category.genderRule).isEqualTo(GenderRules.FEMALE_ONLY)
            assertThat(GenderRules.womanPreferenceOffered(service.category.genderRule)).isFalse()
            val initial = SelectionRules.initial(service)
            assertThat(initial.optionId).isEqualTo("3418c19d-9e25-5e7d-89a9-57d66209a97f") // the is_default Gold facial
            // The required mask group is unfinished until a mask is chosen.
            assertThat(SelectionRules.isComplete(service, initial)).isFalse()
            assertThat(service.addonGroups.map { SelectionRules.required(it) }).containsExactly(1, 0).inOrder()
        },
        // POST /v1/doorstep/serviceability 200 (inside)
        "serviceability_in_200.json" to data(ServiceabilityDto.serializer()) { dto ->
            assertThat(dto.serviceable).isTrue()
            assertThat(dto.zone?.name).isEqualTo("Gachibowli - HITEC City")
            assertThat(dto.reason).isNull()
        },
        // POST /v1/doorstep/serviceability 200 (outside)
        "serviceability_out_200.json" to data(ServiceabilityDto.serializer()) { dto ->
            assertThat(dto.serviceable).isFalse()
            assertThat(dto.city).isNull()
            assertThat(dto.reason).isEqualTo("OUTSIDE_SERVICE_AREA")
        },
        // POST /v1/doorstep/quotes 201 — kitchen deep cleaning, HOME_CLEANING_VIA_ECO at 18%
        "quote_post_201.json" to data(QuoteDto.serializer()) { dto -> assertQuoteAddsUp(dto, total = 224800, rateBps = 1800) },
        // POST /v1/doorstep/quotes 201 — salon, BEAUTY_SALON_REGISTERED at 5%
        "quote_post_201_salon.json" to data(QuoteDto.serializer()) { dto -> assertQuoteAddsUp(dto, total = 179700, rateBps = 500) },
        "quote_post_422_addon_max.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.ADDON_INVALID)
            val details = (err as DoorstepError.Refused).details!!
            assertThat(details["max_select"]!!.jsonPrimitive.int).isEqualTo(1)
            assertThat(details["selected"]!!.jsonPrimitive.int).isEqualTo(2)
        },
        "quote_post_422_addon_min.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.ADDON_INVALID)
            assertThat((err as DoorstepError.Refused).details!!["selected"]!!.jsonPrimitive.int).isEqualTo(0)
        },
        "quote_post_422_option_invalid.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo("DOORSTEP_OPTION_INVALID")
        },
        "quote_post_422_outside_area.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.OUTSIDE_SERVICE_AREA)
        },
        "quote_post_422_quantity.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo("DOORSTEP_QUANTITY_INVALID")
        },

        // ── Addresses (A3) ──
        // POST /v1/doorstep/addresses 201
        "address_post_201.json" to data(AddressDto.serializer()) { dto ->
            assertThat(dto.id).isEqualTo(FIXTURE_ADDRESS_ID)
            assertThat(dto.cityCode).isEqualTo("HYD")
            assertThat(dto.locality).isEqualTo("HITEC City")
            assertThat(dto.isDefault).isTrue()
        },
        "address_post_422_outside_area.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.OUTSIDE_SERVICE_AREA)
        },
        // GET /v1/doorstep/addresses 200
        "addresses_get_200.json" to data(AddressListDto.serializer()) { dto ->
            assertThat(dto.items.map { it.id }).containsExactly(FIXTURE_ADDRESS_ID)
        },

        // ── Slots (A3) ──
        // GET /v1/doorstep/slots?quote_id=&address_id= 200 — Sunday (today) has no hours in the fixture world
        "slots_get_200.json" to data(SlotDaysDto.serializer()) { dto ->
            assertThat(dto.timezone).isEqualTo("Asia/Kolkata")
            assertThat(dto.days).hasSize(7)
            assertThat(dto.days.first().date).isEqualTo("2026-10-04")
            assertThat(dto.days.first().slots.none { it.available }).isTrue()
            assertThat(dto.days[1].slots.count { it.available }).isEqualTo(14)
        },
        "slots_get_409_outstanding.json" to error(status = 409) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.OUTSTANDING_DUE)
            assertThat((err as DoorstepError.Refused).details!!["outstanding_paise"]!!.jsonPrimitive.long).isEqualTo(45000L)
        },

        // ── Bookings (A3) ──
        // POST /v1/doorstep/bookings 201 (Idempotency-Key) — the booking plus its Razorpay-shaped checkout
        "booking_post_201.json" to data(BookingCreatedDto.serializer()) { dto ->
            assertThat(dto.booking.status).isEqualTo("pending_payment")
            assertThat(dto.booking.holdExpiresAt).isEqualTo("2026-10-04T06:40:00Z")
            assertThat(dto.paymentIntent.referenceType).isEqualTo(DoorstepReference.BOOKING)
            assertThat(dto.paymentIntent.referenceId).isEqualTo(dto.booking.id)
            assertThat(dto.paymentIntent.amountPaise).isEqualTo(dto.booking.totalPaise)
            assertRazorpayCheckout(dto.paymentIntent)
            // The intent is pending: nothing here says paid.
            assertThat(payments(dto.paymentIntent).readingFor(DoorstepReference.BOOKING, dto.booking.id))
                .isEqualTo(PaymentStatusReading.Confirming)
        },
        "booking_post_400_idempotency_key.json" to error(status = 400) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.INVALID_REQUEST)
        },
        "booking_post_409_slot_taken.json" to error(status = 409) { err -> assertThat(err.code).isEqualTo(DoorstepCodes.SLOT_TAKEN) },
        "booking_post_409_outstanding.json" to error(status = 409) { err -> assertThat(err.code).isEqualTo(DoorstepCodes.OUTSTANDING_DUE) },
        "booking_post_410_quote_expired.json" to error(status = 410) { err -> assertThat(err.code).isEqualTo(DoorstepCodes.QUOTE_EXPIRED) },
        "booking_post_422_slot_unavailable.json" to error(status = 422) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.SLOT_UNAVAILABLE)
        },
        // GET /v1/doorstep/bookings/{id} 200 — awaiting payment: no OTPs, no photos, one history step
        "booking_get_200_pending_payment.json" to data(BookingDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("pending_payment")
            assertThat(dto.endOtp).isNull()
            assertThat(dto.photos).isEmpty()
            assertThat(dto.statusHistory.map { it.toStatus }).containsExactly("pending_payment")
            assertThat(dto.statusHistory.single().fromStatus).isNull()
            val steps = BookingRules.timeline(dto)
            assertThat(steps.all { it.state == StepState.UPCOMING }).isTrue()
            assertThat(steps.all { it.at == null }).isTrue()
        },
        // GET /v1/doorstep/bookings/{id} 200 — confirmed: "Booked" is done, with the time from status_history
        "booking_get_200.json" to data(BookingDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("confirmed")
            assertThat(dto.paidPaise).isEqualTo(dto.totalPaise)
            assertThat(BookingRules.visibleStartOtp(dto)).isNull()
            assertThat(BookingRules.visibleEndOtp(dto)).isNull()
            assertThat(BookingRules.visitPhotos(dto)).isEmpty()
            val steps = BookingRules.timeline(dto)
            assertThat(steps.first().label).isEqualTo("Booked")
            assertThat(steps.first().state).isEqualTo(StepState.CURRENT)
            assertThat(steps.first().at).isEqualTo("2026-10-04T06:30:00Z")
            assertThat(steps.drop(1).all { it.state == StepState.UPCOMING && it.at == null }).isTrue()
        },
        "booking_get_404.json" to error(status = 404) { err -> assertThat(err.code).isEqualTo(DoorstepCodes.BOOKING_NOT_FOUND) },
        // GET /v1/doorstep/bookings 200
        "bookings_get_200.json" to data(BookingPageDto.serializer()) { dto ->
            assertThat(dto.items.single().status).isEqualTo("confirmed")
            assertThat(dto.nextCursor).isNull()
        },
        // GET /v1/doorstep/bookings/{id}/cancel-preview 200
        "cancel_preview_get_200.json" to data(CancelPreviewDto.serializer()) { dto ->
            assertThat(dto.allowed).isTrue()
            assertThat(dto.feePaise + dto.refundPaise).isEqualTo(224800L)
            assertThat(dto.rule).isEqualTo("free_before_assignment")
        },
        // POST /v1/doorstep/bookings/{id}/cancel 200 — the timeline becomes the history, ending cancelled
        "booking_cancel_post_200.json" to data(BookingDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("cancelled")
            assertThat(dto.canCancel).isFalse()
            val steps = BookingRules.timeline(dto)
            assertThat(steps.map { it.label }).containsExactly("Awaiting payment", "Confirmed", "Cancelled").inOrder()
            assertThat(steps.all { it.state == StepState.DONE }).isTrue()
        },
        // POST /v1/doorstep/bookings/{id}/reschedule 200 — moved once, so no second move
        "booking_reschedule_post_200.json" to data(BookingDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("confirmed")
            assertThat(dto.slotStart).isEqualTo("2026-10-06T04:30:00Z")
            assertThat(dto.canReschedule).isFalse()
        },
        "booking_reschedule_post_409.json" to error(status = 409) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.RESCHEDULE_NOT_ALLOWED)
        },

        // ── Payments (A3) ──
        // POST /v1/doorstep/bookings/{id}/payment/intent 200
        "booking_payment_intent_post_200.json" to data(PaymentIntentDto.serializer()) { dto ->
            assertThat(dto.referenceType).isEqualTo(DoorstepReference.BOOKING)
            assertRazorpayCheckout(dto)
        },
        "booking_payment_intent_410_hold_expired.json" to error(status = 410) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.HOLD_EXPIRED)
        },
        // GET /v1/doorstep/bookings/{id}/payment 200 — the ONLY source of "paid"
        "booking_payment_get_200_pending.json" to data(BookingPaymentsDto.serializer()) { dto ->
            assertThat(dto.readingFor(DoorstepReference.BOOKING, FIXTURE_BOOKING_ID)).isEqualTo(PaymentStatusReading.Confirming)
        },
        "booking_payment_get_200.json" to data(BookingPaymentsDto.serializer()) { dto ->
            assertThat(dto.readingFor(DoorstepReference.BOOKING, FIXTURE_BOOKING_ID)).isEqualTo(PaymentStatusReading.Paid)
        },
        // A refund in flight outranks the succeeded capture.
        "booking_payment_get_200_refund.json" to data(BookingPaymentsDto.serializer()) { dto ->
            assertThat(dto.refunds.single().cause).isEqualTo("customer_cancel")
            assertThat(dto.readingFor(DoorstepReference.BOOKING, FIXTURE_BOOKING_ID)).isEqualTo(PaymentStatusReading.RefundPending)
        },
        // POST /v1/doorstep/bookings/{id}/payment/stub-confirm — refused outside a development stack
        "booking_payment_stub_confirm_404.json" to error(status = 404) { err ->
            assertThat(err.code).isEqualTo(DoorstepCodes.NOT_FOUND)
        },
    )

    /**
     * Routes the app calls whose fixtures doorstep-service has NOT published
     * yet (lanes A4–A5, and GET /quotes/{id}; contract only as of 2026-10-04 —
     * A3's addresses, slots, bookings and payments were published in
     * 9def49e5 and moved to [parsers]). The names are what we expect; rename
     * here when the backend publishes. Each is ASSUMED away until its file
     * appears, then it must decode strictly.
     *
     *  - GET    /v1/doorstep/quotes/{id} 200                   quote_get_200.json
     *  - GET    /v1/doorstep/bookings/{id}/extras 200          extras_list_200.json
     *  - POST   …/extras/{extraId}/approve|decline 200         extra_approve_200.json, extra_decline_200.json
     *  - GET    /v1/doorstep/bookings/{id}/extras-bill 200     extras_bill_get_200.json
     *  - POST   /v1/doorstep/extras-bills/{id}/payment/intent  extras_payment_intent_200.json
     *  - GET    /v1/doorstep/me/outstanding 200                outstanding_get_200.json
     *  - POST   /v1/doorstep/bookings/{id}/rating 201          rating_post_201.json
     *  - POST|GET /v1/doorstep/bookings/{id}/rework            rework_post_201.json, rework_list_200.json
     *  - POST   /v1/doorstep/bookings/{id}/sos 201             sos_post_201.json
     *  - POST   /v1/doorstep/bookings/{id}/share 201           share_post_201.json
     *  - POST   /v1/doorstep/realtime/token 200                realtime_token_200.json
     */
    private val pending: Map<String, (raw: String) -> Unit> = mapOf(
        "quote_get_200.json" to data(QuoteDto.serializer()) {},
        "extras_list_200.json" to data(ExtraListDto.serializer()) {},
        "extra_approve_200.json" to data(ExtraDto.serializer()) {},
        "extra_decline_200.json" to data(ExtraDto.serializer()) {},
        "extras_bill_get_200.json" to data(ExtrasBillDto.serializer()) {},
        "extras_payment_intent_200.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.referenceType).isEqualTo("doorstep_extras")
        },
        "outstanding_get_200.json" to data(OutstandingDto.serializer()) {},
        "rating_post_201.json" to data(RatingDto.serializer()) {},
        "rework_post_201.json" to data(ReworkRequestDto.serializer()) {},
        "rework_list_200.json" to data(ReworkListDto.serializer()) {},
        "sos_post_201.json" to data(IncidentDto.serializer()) {},
        "share_post_201.json" to data(ShareTokenDto.serializer()) {},
        "realtime_token_200.json" to data(RealtimeTokenDto.serializer()) {
            assertThat(it.topics.single()).startsWith("doorstep.booking.")
        },
    )

    /** The A3 fixtures' checkout is payments-service's Razorpay session, typed, and it opens the sheet. */
    private fun assertRazorpayCheckout(intent: PaymentIntentDto) {
        assertThat(intent.checkout).isEqualTo(
            CheckoutSessionDto(
                provider = "razorpay",
                orderId = "order_FixtureDoorstep01",
                keyId = "rzp_test_fixture",
                merchantDisplayName = "Doorstep",
            ),
        )
        val route = intent.checkoutRoute("Doorstep booking", DoorstepPaymentConfig.forEnvironment("dev")) as CheckoutRoute.Sheet
        assertThat(route.session.providerOrderId).isEqualTo("order_FixtureDoorstep01")
        assertThat(route.session.merchantDisplayName).isEqualTo("Doorstep")
        assertThat(route.session.amountMinor).isEqualTo(intent.amountPaise)
    }

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
            val raw = File(contractsDir, name).readText()
            try {
                pending.getValue(name)(raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    /** The strictness itself: a dropped required key and an unknown key both fail. */
    @Test
    fun `a dropped required key or an unknown key fails the strict decode`() {
        val raw = File(contractsDir, "serviceability_in_200.json").readText()
        val dropped = raw.replace(""","reason":null""", "")
        assertThat(dropped).isNotEqualTo(raw)
        assertThat(runCatching { data(ServiceabilityDto.serializer()) {}(dropped) }.isFailure).isTrue()
        val added = raw.replace(""""serviceable":true""", """"serviceable":true,"surprise":1""")
        assertThat(runCatching { data(ServiceabilityDto.serializer()) {}(added) }.isFailure).isTrue()

        // The A3 gap-closers are required on Booking: dropping any one fails.
        val booking = File(contractsDir, "booking_get_200.json").readText()
        listOf(""""end_otp":null,""", """"photos":[],""").forEach { key ->
            val without = booking.replace(key, "")
            assertThat(without).isNotEqualTo(booking)
            assertThat(runCatching { data(BookingDto.serializer()) {}(without) }.isFailure).isTrue()
        }
        val noHistory = booking.replace(Regex(""""status_history":\[[^\]]*],"""), "")
        assertThat(noHistory).isNotEqualTo(booking)
        assertThat(runCatching { data(BookingDto.serializer()) {}(noHistory) }.isFailure).isTrue()
    }

    /** Lines add up to the totals, each line's split adds up to its total, and the rate is the category's. */
    private fun assertQuoteAddsUp(dto: QuoteDto, total: Long, rateBps: Int) {
        assertThat(dto.totalPaise).isEqualTo(total)
        assertThat(dto.pricesIncludeTax).isTrue()
        assertThat(dto.lines.map { Paise(it.lineTotalPaise) }.sum()).isEqualTo(Paise(dto.totalPaise))
        assertThat(Paise(dto.taxablePaise) + Paise(dto.taxPaise)).isEqualTo(Paise(dto.totalPaise))
        dto.lines.forEach { line ->
            assertThat(line.taxablePaise + line.taxPaise).isEqualTo(line.lineTotalPaise)
            assertThat(Paise(line.unitPricePaise) * line.quantity).isEqualTo(Paise(line.lineTotalPaise))
            assertThat(line.taxRateBps).isEqualTo(rateBps)
        }
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        check(checkNotNull(envelope.data))
    }

    /** [status] is the HTTP status the Go test asserts for the route; the fixture body carries the envelope only. */
    private fun error(status: Int, check: (DoorstepError) -> Unit): (String) -> Unit = { raw ->
        strict.decodeFromString(DoorstepErrorEnvelopeDto.serializer(), raw)
        check(DoorstepError.from(status, raw, strict))
    }
}
