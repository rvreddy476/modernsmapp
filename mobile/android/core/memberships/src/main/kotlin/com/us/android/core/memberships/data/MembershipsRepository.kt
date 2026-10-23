package com.us.android.core.memberships.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Every membership read and write, as [AppResult]. The screens and the
 * payment flow go through this; nothing else calls [MembershipsApi].
 */
@Singleton
class MembershipsRepository @Inject constructor(
    private val api: MembershipsApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun myLevels(): AppResult<List<MembershipLevel>> =
        listApiCall(errorMapper) { api.myTiers() }.map { rows -> rows.map { it.toLevel() } }

    /**
     * The signed-in creator's ACTIVE levels, cheapest first — what a composer's
     * "Members only" offers. Empty (never a failure) when there are none or the
     * read failed: the picker is then simply not drawn.
     */
    suspend fun ownActiveLevels(): List<MembershipLevel> =
        (myLevels() as? AppResult.Success)?.data.orEmpty().filter { it.isActive }.sortedBy { it.pricePaise }

    /** Anyone's ACTIVE levels, cheapest first (the order the Join sheet lists them in). */
    suspend fun levelsOf(creatorId: String): AppResult<List<MembershipLevel>> =
        listApiCall(errorMapper) { api.creatorTiers(creatorId) }
            .map { rows -> rows.map { it.toLevel() }.filter { it.isActive }.sortedBy { it.pricePaise } }

    suspend fun eligibility(creatorId: String): AppResult<MembershipEligibility> =
        apiCall(errorMapper, { it.toEligibility() }) { api.eligibility(creatorId) }

    suspend fun createLevel(name: String, pricePaise: Long, perks: List<String>): AppResult<MembershipLevel> =
        apiCall(errorMapper, { it.toLevel() }) {
            api.createTier(CreateTierRequest(name.trim(), pricePaise, INR, LevelRules.normalisePerks(perks)))
        }

    /** A partial edit; a null argument is left as it is on the server. */
    suspend fun updateLevel(
        levelId: String,
        name: String? = null,
        pricePaise: Long? = null,
        perks: List<String>? = null,
        isActive: Boolean? = null,
    ): AppResult<MembershipLevel?> =
        apiCall(errorMapper, { it.tier?.toLevel() }) {
            api.updateTier(
                levelId,
                UpdateTierRequest(
                    name = name?.trim(),
                    pricePaise = pricePaise,
                    perks = perks?.let(LevelRules::normalisePerks),
                    isActive = isActive,
                ),
            )
        }

    suspend fun join(creatorId: String, levelId: String, idempotencyKey: String): AppResult<MembershipCheckout> =
        apiCall(errorMapper, { it.toCheckout() }) {
            api.subscribe(creatorId, idempotencyKey, SubscribeRequest(levelId))
        }

    suspend fun renew(subscriptionId: String): AppResult<MembershipCheckout> =
        apiCall(errorMapper, { it.toCheckout() }) { api.renew(subscriptionId) }

    suspend fun changeLevel(subscriptionId: String, levelId: String): AppResult<LevelChange> =
        apiCall(errorMapper, { it.toLevelChange() }) { api.changeLevel(subscriptionId, UpgradeRequest(levelId)) }

    suspend fun cancel(subscriptionId: String): AppResult<String> =
        apiCall(
            errorMapper,
            { it.status }
        ) { api.cancel(subscriptionId, CancelRequest(reason = "", immediate = false)) }

    suspend fun paymentStatus(paymentId: String): AppResult<PaymentStatusDto> =
        apiCall(errorMapper) { api.paymentStatus(paymentId) }

    suspend fun myMemberships(): AppResult<List<MyMembership>> =
        listApiCall(errorMapper) { api.myMemberships() }.map { rows -> rows.map { it.toMembership() } }

    /** One membership with its perks and any scheduled downgrade; `AppError.NotFound` when it is not the viewer's. */
    suspend fun myMembership(subscriptionId: String): AppResult<MyMembership> =
        apiCall(errorMapper, { it.toMembership() }) { api.myMembership(subscriptionId) }

    /** Gates [postId] on [levelId], or opens it to everyone when [levelId] is null. Author only. */
    suspend fun setPostGate(postId: String, levelId: String?): AppResult<String?> =
        apiCall(errorMapper, { it.tierRequiredId }) { api.setPostGate(postId, gateBody(levelId)) }

    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    companion object {
        /** `{"tier_required_id": "<uuid>"}`, or `{"tier_required_id": null}` — an explicit null, never `{}` by accident. */
        fun gateBody(levelId: String?) = buildJsonObject {
            put(TIER_REQUIRED_ID, levelId?.trim()?.takeIf { it.isNotEmpty() }?.let(::JsonPrimitive) ?: JsonNull)
        }

        private const val TIER_REQUIRED_ID = "tier_required_id"
    }
}

/** The server's contract code on a refusal, wherever the mapper put it. */
fun AppError.membershipCode(): String? = when (this) {
    is AppError.Forbidden -> code
    is AppError.Unknown -> code
    is AppError.Server -> code
    is AppError.AuthFailed -> code
    is AppError.InvalidRequest -> "INVALID_REQUEST"
    else -> null
}?.takeIf { it.isNotBlank() }

/** A failure to reach the server, as opposed to the server saying no. */
val AppError.isTransport: Boolean
    get() = this is AppError.NoNetwork || this is AppError.Timeout

/** The items a 403 `MEMBERSHIPS_NOT_ELIGIBLE` names (`details.missing`, a JSON array the mapper kept as text). */
fun AppError.notEligibleItems(json: Json = Json): List<EligibilityItem>? {
    if (this !is AppError.Forbidden || code != NOT_ELIGIBLE) return null
    val raw = details["missing"] ?: return emptyList()
    val items = runCatching { json.parseToJsonElement(raw) as? JsonArray }.getOrNull()
        ?.mapNotNull { runCatching { it.jsonPrimitive.content }.getOrNull() }
        ?: listOf(raw)
    return items.filter { it.isNotBlank() }.map(EligibilityItem::fromWire)
}

/**
 * A membership refusal in plain words (http/membership_payments_handler.go
 * `writeMembershipError`, http/handler.go `writeTierError`). A code this
 * build does not know reads as [fallback]; a transport failure as a network
 * sentence.
 */
@Suppress("CyclomaticComplexMethod") // One branch per server code: a lookup table, not logic.
fun membershipRefusal(error: AppError, fallback: String = "Something went wrong. Please try again."): String {
    if (error.isTransport) return "You're offline. Check your connection and try again."
    if (error is AppError.RateLimited) return "Too many tries. Wait a moment and try again."
    return when (error.membershipCode()) {
        "MEMBERSHIPS_NOT_ENABLED" -> "Memberships aren't available yet."
        "MEMBERSHIP_PAYMENTS_UNAVAILABLE" -> "Payments are unavailable right now. Try again in a little while."
        "PAYMENT_SESSION_UNAVAILABLE" -> "Checkout couldn't be opened. Please try again."
        "CHECKOUT_EXPIRED" -> "That checkout expired. Start again."
        "ALREADY_SUBSCRIBED" -> "You're already a member."
        "IDEMPOTENCY_KEY_REUSED" -> "That checkout belongs to another level. Start again."
        "SUBSCRIPTION_NOT_RENEWABLE" -> "This membership can't be renewed any more. Join again instead."
        "RENEWAL_ALREADY_PAID" -> "This month is already paid."
        "TIER_INACTIVE" -> "That level isn't offered any more."
        "TIER_NOT_FOUND" -> "That level no longer exists."
        "TIER_CREATOR_MISMATCH" -> "That level belongs to another creator."
        "CANNOT_SUBSCRIBE_TO_SELF" -> "You can't join your own membership."
        "SUBSCRIPTION_STATE_CHANGED" ->
            "Your membership changed while this was running. Nothing was charged; try again."
        NOT_ELIGIBLE -> "You can't offer memberships yet."
        "TIER_NAME_INVALID" -> "Give the level a name of up to ${LevelRules.MAX_NAME_LENGTH} characters."
        "TIER_PRICE_OUT_OF_RANGE" ->
            "Price must be between ${rupees(
                LevelRules.MIN_PRICE_PAISE
            )} and ${rupees(LevelRules.MAX_PRICE_PAISE)} a month."
        "TIER_PERKS_INVALID" ->
            "Up to ${LevelRules.MAX_PERKS} perks, each up to ${LevelRules.MAX_PERK_LENGTH} characters."
        "TIER_CURRENCY_INVALID" -> "Levels are priced in rupees."
        "TIER_LIMIT" -> "You can have up to ${LevelRules.MAX_ACTIVE_LEVELS} active levels."
        "MEMBERS_ONLY" -> "This is for members only."
        INVALID_TIER, TIERS_UNAVAILABLE -> membersOnlyRefusal(error.membershipCode()).orEmpty()
        "FORBIDDEN" -> "You can't do that."
        else -> if (error is AppError.NotFound) "That isn't there any more." else fallback
    }
}

const val NOT_ELIGIBLE = "MEMBERSHIPS_NOT_ELIGIBLE"

/**
 * A post's create or edit refused over its members-only level, in words
 * (post-service handler.go `writeTierError`, 2026-10-23): the level is not
 * one the creator offers (400 `INVALID_TIER`), or the levels could not be
 * checked (503 `MEMBERSHIP_TIERS_UNAVAILABLE`: nothing was created or
 * changed, try again). Null for any other code.
 */
fun membersOnlyRefusal(code: String?): String? = when (code) {
    INVALID_TIER -> "That membership level isn't offered any more. Pick another, or choose Everyone."
    TIERS_UNAVAILABLE -> "Members-only can't be checked right now. Nothing was saved — try again in a moment."
    else -> null
}

const val INVALID_TIER = "INVALID_TIER"
const val TIERS_UNAVAILABLE = "MEMBERSHIP_TIERS_UNAVAILABLE"
