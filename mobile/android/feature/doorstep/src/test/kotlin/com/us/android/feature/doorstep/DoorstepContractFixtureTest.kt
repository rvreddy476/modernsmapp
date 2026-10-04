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
import com.us.android.feature.doorstep.domain.GenderRules
import com.us.android.feature.doorstep.domain.SelectionRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.sum
import com.us.android.feature.doorstep.model.toRupeeText
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
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
    )

    /**
     * Routes the app calls whose fixtures doorstep-service has NOT published
     * yet (lanes A3–A5, contract only as of 2026-10-04). The names are what we
     * expect; rename here when the backend publishes. Each is ASSUMED away
     * until its file appears, then it must decode strictly.
     *
     *  - GET    /v1/doorstep/quotes/{id} 200                   quote_get_200.json
     *  - GET    /v1/doorstep/addresses 200                     addresses_list_200.json
     *  - POST   /v1/doorstep/addresses 201                     address_post_201.json
     *  - GET    /v1/doorstep/slots 200                         slots_get_200.json
     *  - POST   /v1/doorstep/bookings 201                      booking_post_201.json
     *  - GET    /v1/doorstep/bookings 200                      bookings_list_200.json
     *  - GET    /v1/doorstep/bookings/{id} 200                 booking_get_200.json
     *  - GET    /v1/doorstep/bookings/{id}/cancel-preview 200  cancel_preview_200.json
     *  - POST   /v1/doorstep/bookings/{id}/cancel 200          booking_cancel_200.json
     *  - POST   /v1/doorstep/bookings/{id}/reschedule 200      booking_reschedule_200.json
     *  - POST   /v1/doorstep/bookings/{id}/payment/intent 200  booking_payment_intent_200.json
     *  - GET    /v1/doorstep/bookings/{id}/payment 200         booking_payment_get_200.json
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
     *  - error  DOORSTEP_OUTSTANDING_DUE / SLOT_TAKEN / HOLD_EXPIRED  booking_post_409_outstanding.json,
     *           booking_post_409_slot_taken.json, booking_payment_intent_410_hold_expired.json
     */
    private val pending: Map<String, (raw: String) -> Unit> = mapOf(
        "quote_get_200.json" to data(QuoteDto.serializer()) {},
        "addresses_list_200.json" to data(AddressListDto.serializer()) {},
        "address_post_201.json" to data(AddressDto.serializer()) {},
        "slots_get_200.json" to data(SlotDaysDto.serializer()) { assertThat(it.timezone).isEqualTo("Asia/Kolkata") },
        "booking_post_201.json" to data(BookingCreatedDto.serializer()) { dto ->
            assertThat(dto.booking.status).isEqualTo("pending_payment")
            assertThat(dto.paymentIntent.referenceType).isEqualTo("doorstep_booking")
            assertThat(dto.paymentIntent.referenceId).isEqualTo(dto.booking.id)
        },
        "bookings_list_200.json" to data(BookingPageDto.serializer()) {},
        "booking_get_200.json" to data(BookingDto.serializer()) {},
        "cancel_preview_200.json" to data(CancelPreviewDto.serializer()) {},
        "booking_cancel_200.json" to data(BookingDto.serializer()) {},
        "booking_reschedule_200.json" to data(BookingDto.serializer()) {},
        "booking_payment_intent_200.json" to data(PaymentIntentDto.serializer()) {},
        "booking_payment_get_200.json" to data(BookingPaymentsDto.serializer()) {},
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
        "booking_post_409_outstanding.json" to error(status = 409) { assertThat(it.code).isEqualTo(DoorstepCodes.OUTSTANDING_DUE) },
        "booking_post_409_slot_taken.json" to error(status = 409) { assertThat(it.code).isEqualTo(DoorstepCodes.SLOT_TAKEN) },
        "booking_payment_intent_410_hold_expired.json" to error(status = 410) { assertThat(it.code).isEqualTo(DoorstepCodes.HOLD_EXPIRED) },
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
