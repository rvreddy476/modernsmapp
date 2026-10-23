package com.us.android.core.memberships.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Paid memberships (contract pinned 2026-10-23). monetization-service's
 * routes are registered in http/handler.go (`RegisterRoutes`) and
 * http/membership_payments_handler.go (`registerMembershipRoutes`); every one
 * answers 503 `MEMBERSHIPS_NOT_ENABLED` while the server's switch is off.
 */
// One interface per contract: every membership route the app calls.
@Suppress("TooManyFunctions")
interface MembershipsApi {

    // ── Levels (the creator's own) ──────────────────────────────────────

    /** The signed-in creator's levels, active and not. */
    @GET("v1/monetization/tiers")
    suspend fun myTiers(): ApiEnvelope<List<TierDto>>

    /** A new level. The FIRST needs eligibility: 403 `MEMBERSHIPS_NOT_ELIGIBLE` with `details.missing`. */
    @POST("v1/monetization/tiers")
    suspend fun createTier(@Body body: CreateTierRequest): ApiEnvelope<TierDto>

    /** A partial edit: name, price, perks, or `is_active` to deactivate. */
    @PATCH("v1/monetization/tiers/{tierId}")
    suspend fun updateTier(
        @Path("tierId") tierId: String,
        @Body body: UpdateTierRequest
    ): ApiEnvelope<UpdateTierResponseDto>

    /** Whether the creator may offer memberships, and what is missing. The creator only. */
    @GET("v1/monetization/creators/{creatorId}/membership-eligibility")
    suspend fun eligibility(@Path("creatorId") creatorId: String): ApiEnvelope<EligibilityDto>

    // ── Levels (anyone's, active only) ──────────────────────────────────

    @GET("v1/monetization/creators/{creatorId}/tiers")
    suspend fun creatorTiers(@Path("creatorId") creatorId: String): ApiEnvelope<List<TierDto>>

    // ── Joining and paying ──────────────────────────────────────────────

    /**
     * A join: 201 checkout, status `pending_payment`. The SAME
     * Idempotency-Key replays the same checkout (a lost response is never a
     * second join); a key already used for another creator or level is 409
     * `IDEMPOTENCY_KEY_REUSED`; an expired one 409 `CHECKOUT_EXPIRED`.
     */
    @POST("v1/monetization/subscribe/{creatorId}")
    suspend fun subscribe(
        @Path("creatorId") creatorId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: SubscribeRequest,
    ): ApiEnvelope<CheckoutDto>

    /** Pay for the next month: 201 checkout, kind `renewal`. Deterministic per period, so no key. */
    @POST("v1/monetization/subscriptions/{subscriptionId}/renew")
    suspend fun renew(@Path("subscriptionId") subscriptionId: String): ApiEnvelope<CheckoutDto>

    /** Change level: a checkout for a paid upgrade, or `upgraded` / `scheduled` with nothing to pay. */
    @POST("v1/monetization/subscriptions/{subscriptionId}/upgrade")
    suspend fun changeLevel(
        @Path("subscriptionId") subscriptionId: String,
        @Body body: UpgradeRequest,
    ): ApiEnvelope<UpgradeResultDto>

    /** Cancel at the period's end: access stays until then. */
    @POST("v1/monetization/subscriptions/{subscriptionId}/cancel")
    suspend fun cancel(
        @Path("subscriptionId") subscriptionId: String,
        @Body body: CancelRequest,
    ): ApiEnvelope<StatusDto>

    /** The payer's poll: `confirming | paid | failed`; 404 for anyone else's. */
    @GET("v1/monetization/memberships/payments/{paymentId}")
    suspend fun paymentStatus(@Path("paymentId") paymentId: String): ApiEnvelope<PaymentStatusDto>

    /** The signed-in viewer's memberships: live, pending, and ended ones still renewable. */
    @GET("v1/monetization/memberships")
    suspend fun myMemberships(): ApiEnvelope<List<MembershipViewDto>>

    /** One of them, with the level's perks and any scheduled downgrade; 404 for anyone else's. */
    @GET("v1/monetization/memberships/{subscriptionId}")
    suspend fun myMembership(@Path("subscriptionId") subscriptionId: String): ApiEnvelope<MembershipViewDto>

    // ── The creator's members ─────────────────────────────────────────────

    /**
     * The signed-in creator's members, newest first (2026-10-23);
     * `meta.next_cursor` pages on, absent on the last page. 400
     * `INVALID_CURSOR` for a cursor the server did not mint. A members-only
     * level rides the post create and PATCH themselves, so there is no gate
     * route here.
     */
    @GET("v1/monetization/members")
    suspend fun members(
        @Query("cursor") cursor: String? = null,
        @Query("limit") limit: Int? = null,
    ): ApiEnvelope<List<MemberRowDto>>
}
