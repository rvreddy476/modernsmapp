package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.mopedu.captain.data.AcceptOfferResponseDto
import com.us.android.feature.mopedu.captain.data.CaptainEarningsDto
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainErrorEnvelopeDto
import com.us.android.feature.mopedu.captain.data.CaptainOfferDto
import com.us.android.feature.mopedu.captain.data.PartnerProfileDto
import com.us.android.feature.mopedu.captain.data.ReviewState
import com.us.android.feature.mopedu.captain.data.RidePaymentDto
import com.us.android.feature.mopedu.captain.data.SubscriptionCheckoutResponseDto
import com.us.android.feature.mopedu.captain.data.SubscriptionPaymentDto
import com.us.android.feature.mopedu.captain.data.SubscriptionPaymentStatus
import com.us.android.feature.mopedu.captain.data.SubscriptionPlanDto
import com.us.android.feature.mopedu.captain.data.toDomain
import com.us.android.feature.mopedu.captain.data.toOffer
import com.us.android.feature.mopedu.captain.data.toReview
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture rider-service publishes for a route the
 * captain app reads decodes STRICTLY into its DTO. The fixtures are the
 * handler-test goldens (internal/http/testdata/contracts, written by
 * internal/http/contracts_test.go), copied byte for byte and keyed on the
 * backend's file names. Unknown keys fail here so a renamed server field is
 * caught, even though the platform Json ignores them in production.
 */
class CaptainContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    /** The published fixtures for routes the captain reads. */
    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // TestContract_PaymentStatusPaid — GET /v1/rider/rides/{id}/payment 200,
        // polled by the captain until the SERVER says paid.
        "payment_status_paid.json" to data(RidePaymentDto.serializer()) { dto ->
            assertThat(dto.method).isEqualTo("upi")
            assertThat(dto.status).isEqualTo("paid")
            assertThat(dto.amountPaise).isEqualTo(12451L)
            assertThat(dto.refundedPaise).isEqualTo(0L)
            assertThat(dto.intentId).isEqualTo("6f0e2c9a-1b3d-4e5f-8a7b-9c0d1e2f3a4b")
            assertThat(dto.updatedAt).isEqualTo("2026-09-18T04:05:30Z")
            assertThat(PaymentMethod.fromCode(dto.method).isOnline).isTrue()
            assertThat(RidePaymentStatus.fromCode(dto.status).isSettled).isTrue()
        },
    )

    /**
     * Partner routes whose fixtures rider-service has NOT published yet
     * (no TestContract_* for them in contracts_test.go as of 2026-09-18):
     *
     *  - GET  /v1/rider/partners/me 200 / 404        (partner_me.json, partner_me_not_found.json)
     *  - GET  /v1/rider/offers/incoming 200          (offers_incoming.json)
     *  - POST /v1/rider/offers/{id}/accept 200       (offer_accept.json)
     *  - GET  /v1/rider/partners/me/earnings 200     (partner_earnings.json)
     *
     * The names are what we expect; rename here when the backend publishes.
     * Each is ASSUMED away until its file appears, then the parser runs.
     */
    private val pending: Map<String, (raw: String) -> Unit> = mapOf(
        "partner_me.json" to data(PartnerProfileDto.serializer()) { assertThat(it.id).isNotEmpty() },
        "partner_me_not_found.json" to error(status = 404) { assertThat(it).isEqualTo(CaptainError.NotFound) },
        // The onboarding verdict (2026-09-18): `review {state, pending}` on GET /v1/rider/partners/me.
        "partner_me_under_review.json" to data(PartnerProfileDto.serializer()) { dto ->
            val review = checkNotNull(dto.review) { "the fixture must carry review" }
            assertThat(review.state).isEqualTo("under_review")
            assertThat(dto.toReview().state).isEqualTo(ReviewState.UNDER_REVIEW)
            assertThat(dto.toReview().pending).isEqualTo(review.pending)
        },
        "partner_me_approved.json" to data(PartnerProfileDto.serializer()) { dto ->
            assertThat(checkNotNull(dto.review).state).isEqualTo("approved")
            assertThat(dto.toReview().isApproved).isTrue()
        },
        // Subscriptions through payments-service (2026-09-18).
        "subscription_plans.json" to data(ListSerializer(SubscriptionPlanDto.serializer())) { list ->
            val trial = list.first { it.code == "trial_7d" }
            assertThat(trial.priceAmount).isEqualTo(0.0)
            assertThat(trial.toDomain().price.isZero).isTrue()
            assertThat(trial.toDomain().isTrial).isTrue()
            list.filter { it.code != "trial_7d" }.forEach { plan ->
                assertThat(plan.billingPeriodDays).isGreaterThan(0)
                assertThat(plan.toDomain().price.paise).isGreaterThan(0L)
            }
        },
        // POST /v1/rider/subscriptions/checkout for the trial: active, once ever, no client_session.
        "subscription_checkout_trial.json" to data(SubscriptionCheckoutResponseDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("active")
            assertThat(dto.clientSession.orEmpty()).isEmpty()
            assertThat(dto.subscriptionId).isNotEmpty()
        },
        // POST /v1/rider/subscriptions/checkout for a paid plan: the sheet's session, the subscription id as reference.
        "subscription_checkout_paid.json" to data(SubscriptionCheckoutResponseDto.serializer()) { dto ->
            assertThat(dto.status).isNotEqualTo("active")
            assertThat(dto.amountPaise).isGreaterThan(0L)
            val session = checkNotNull(dto.clientSession)
            assertThat(session.keys).containsAtLeast("provider", "order_id", "key_id")
            assertThat(checkNotNull(dto.intentId)).isNotEmpty()
        },
        // GET /v1/rider/subscriptions/me/payment 200: the only source of "paid" for a plan.
        "subscription_payment_paid.json" to data(SubscriptionPaymentDto.serializer()) { dto ->
            assertThat(dto.status).isEqualTo("paid")
            assertThat(SubscriptionPaymentStatus.fromCode(dto.status)).isEqualTo(SubscriptionPaymentStatus.PAID)
        },
        "subscription_payment_pending.json" to data(SubscriptionPaymentDto.serializer()) { dto ->
            assertThat(SubscriptionPaymentStatus.fromCode(dto.status)).isEqualTo(SubscriptionPaymentStatus.PENDING)
        },
        "offers_incoming.json" to data(ListSerializer(CaptainOfferDto.serializer())) { list ->
            list.forEach { assertThat(it.toOffer().estimatedEarnings.paise).isGreaterThan(0L) }
        },
        "offer_accept.json" to data(AcceptOfferResponseDto.serializer()) { assertThat(it.rideId).isNotEmpty() },
        "partner_earnings.json" to data(CaptainEarningsDto.serializer()) { assertThat(it.todayEarningsPaise).isAtLeast(0L) },
    )

    private fun fixtures(): Set<String> = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser and every parser a fixture`() {
        assertThat(fixtures() - parsers.keys - pending.keys).isEmpty()
        assertThat(parsers.keys - fixtures()).isEmpty()
        assertThat(parsers.keys.intersect(pending.keys)).isEmpty()
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

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        assertThat(envelope.error).isNull()
        check(checkNotNull(envelope.data))
    }

    /** [status] is the HTTP status the Go test asserts for the route; the fixture body carries the envelope only. */
    private fun error(status: Int, check: (CaptainError) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(CaptainErrorEnvelopeDto.serializer(), raw)
        assertThat(envelope.error).isNotNull()
        check(CaptainError.from(status, raw, strict))
    }
}
