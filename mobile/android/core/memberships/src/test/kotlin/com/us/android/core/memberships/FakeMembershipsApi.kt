package com.us.android.core.memberships

import android.app.Activity
import com.us.android.core.memberships.data.CancelRequest
import com.us.android.core.memberships.data.CheckoutDto
import com.us.android.core.memberships.data.CreateTierRequest
import com.us.android.core.memberships.data.EligibilityDto
import com.us.android.core.memberships.data.MemberRowDto
import com.us.android.core.memberships.data.MembershipViewDto
import com.us.android.core.memberships.data.MembershipsApi
import com.us.android.core.memberships.data.PaymentStatusDto
import com.us.android.core.memberships.data.StatusDto
import com.us.android.core.memberships.data.SubscribeRequest
import com.us.android.core.memberships.data.TierDto
import com.us.android.core.memberships.data.UpdateTierRequest
import com.us.android.core.memberships.data.UpdateTierResponseDto
import com.us.android.core.memberships.data.UpgradeRequest
import com.us.android.core.memberships.data.UpgradeResultDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * A scripted [MembershipsApi]: each call takes the next answer from its
 * queue (a thrown answer is a refusal or a dropped connection, as Retrofit
 * would throw it) and records what it was sent.
 */
class FakeMembershipsApi : MembershipsApi {
    val subscribeCalls = mutableListOf<Triple<String, String, String>>()
    val subscribeAnswers = ArrayDeque<() -> ApiEnvelope<CheckoutDto>>()
    val statusAnswers = ArrayDeque<() -> ApiEnvelope<PaymentStatusDto>>()
    var lastStatus: () -> ApiEnvelope<PaymentStatusDto> = { payment("confirming") }
    var statusReads = 0
    val renewAnswers = ArrayDeque<() -> ApiEnvelope<CheckoutDto>>()
    var renewCalls = 0
    var tiers: List<TierDto> = emptyList()
    var creatorTiers: List<TierDto> = emptyList()
    var eligibility: EligibilityDto = EligibilityDto(eligible = true)
    val createAnswers = ArrayDeque<() -> ApiEnvelope<TierDto>>()
    val created = mutableListOf<CreateTierRequest>()
    val updated = mutableListOf<Pair<String, UpdateTierRequest>>()
    var memberships: List<MembershipViewDto> = emptyList()
    val memberPages = mutableListOf<Pair<String?, Int?>>()
    var members: List<MemberRowDto> = emptyList()

    override suspend fun myTiers(): ApiEnvelope<List<TierDto>> = ApiEnvelope(data = tiers)

    override suspend fun createTier(body: CreateTierRequest): ApiEnvelope<TierDto> {
        created += body
        return createAnswers.removeFirstOrNull()?.invoke()
            ?: ApiEnvelope(data = TierDto(id = "new", name = body.name, pricePaise = body.pricePaise, isActive = true))
    }

    override suspend fun updateTier(tierId: String, body: UpdateTierRequest): ApiEnvelope<UpdateTierResponseDto> {
        updated += tierId to body
        return ApiEnvelope(data = UpdateTierResponseDto(status = "updated", tier = TierDto(id = tierId)))
    }

    override suspend fun eligibility(creatorId: String): ApiEnvelope<EligibilityDto> = ApiEnvelope(data = eligibility)

    override suspend fun creatorTiers(creatorId: String): ApiEnvelope<List<TierDto>> = ApiEnvelope(data = creatorTiers)

    override suspend fun subscribe(
        creatorId: String,
        idempotencyKey: String,
        body: SubscribeRequest,
    ): ApiEnvelope<CheckoutDto> {
        subscribeCalls += Triple(creatorId, idempotencyKey, body.tierId)
        return subscribeAnswers.removeFirst().invoke()
    }

    override suspend fun renew(subscriptionId: String): ApiEnvelope<CheckoutDto> {
        renewCalls++
        return renewAnswers.removeFirst().invoke()
    }

    override suspend fun changeLevel(subscriptionId: String, body: UpgradeRequest): ApiEnvelope<UpgradeResultDto> =
        error("not scripted")

    override suspend fun cancel(subscriptionId: String, body: CancelRequest): ApiEnvelope<StatusDto> =
        ApiEnvelope(data = StatusDto("cancelled_at_period_end"))

    override suspend fun paymentStatus(paymentId: String): ApiEnvelope<PaymentStatusDto> {
        statusReads++
        return (statusAnswers.removeFirstOrNull() ?: lastStatus).invoke()
    }

    override suspend fun myMemberships(): ApiEnvelope<List<MembershipViewDto>> = ApiEnvelope(data = memberships)

    override suspend fun myMembership(subscriptionId: String): ApiEnvelope<MembershipViewDto> =
        ApiEnvelope(data = memberships.first { it.subscriptionId == subscriptionId })

    override suspend fun members(cursor: String?, limit: Int?): ApiEnvelope<List<MemberRowDto>> {
        memberPages += cursor to limit
        return ApiEnvelope(data = members)
    }
}

fun checkout(
    paymentId: String = "pay-1",
    status: String = "pending_payment",
    session: Boolean = true,
    kind: String = "join",
) = ApiEnvelope(
    data = CheckoutDto(
        subscriptionId = "sub-1",
        paymentId = paymentId,
        kind = kind,
        status = status,
        amountPaise = 9_900,
        currency = "INR",
        clientSession = if (session) {
            JsonObject(
                mapOf(
                    "provider" to JsonPrimitive("razorpay"),
                    "order_id" to JsonPrimitive("order_1"),
                    "key_id" to JsonPrimitive("rzp_test_1"),
                ),
            )
        } else {
            null
        },
    ),
)

fun payment(status: String, refund: String? = null) =
    ApiEnvelope(
        data = PaymentStatusDto(
            paymentId = "pay-1",
            subscriptionId = "sub-1",
            kind = "join",
            status = status,
            refundStatus = refund
        )
    )

/** A refusal with a contract code, as Retrofit throws it. */
fun refused(http: Int, code: String): Nothing = throw HttpException(
    Response.error<Any>(
        http,
        """{"error":{"code":"$code","message":"no"}}""".toResponseBody("application/json".toMediaType())
    ),
)

/** A dropped connection. */
fun offline(): Nothing = throw IOException("offline")

/** A coordinator whose launcher must never be reached: a flow never opens a sheet itself. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(
            activity: Activity,
            attempt: PaymentAttempt,
            session: PaymentSession,
            onOutcome: (PaymentOutcome) -> Unit
        ) =
            error("the membership flow must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)
