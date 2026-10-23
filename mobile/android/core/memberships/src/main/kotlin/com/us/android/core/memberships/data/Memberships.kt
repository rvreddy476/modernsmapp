package com.us.android.core.memberships.data

/** A membership level as the app shows it. Money is integer paise, never a Double. */
data class MembershipLevel(
    val id: String,
    val creatorId: String,
    val name: String,
    val pricePaise: Long,
    val perks: List<String>,
    val memberCount: Int,
    val isActive: Boolean,
) {
    /** "₹99 a month" — GST is included in the price (founder, 23 Oct). */
    val priceLabel: String get() = "${rupees(pricePaise)} a month"
}

internal fun TierDto.toLevel() = MembershipLevel(
    id = id,
    creatorId = creatorId,
    name = name.trim(),
    pricePaise = pricePaise,
    perks = perkList,
    memberCount = subscriberCount.coerceAtLeast(0),
    isActive = isActive,
)

/** Whether a creator may offer memberships, and what is missing, in plain words. */
data class MembershipEligibility(
    val eligible: Boolean,
    val missing: List<EligibilityItem>,
)

/**
 * The four things a creator needs (founder, 23 Oct 2026): 18 or older, PAN
 * verified, no active strikes, at least 500 followers. [Unknown] keeps an
 * item this build has no words for — it is still missing.
 */
sealed interface EligibilityItem {
    val sentence: String

    data object Age : EligibilityItem {
        override val sentence = "You need to be 18 or older."
    }

    data object Pan : EligibilityItem {
        override val sentence = "Add and verify your PAN in tax details."
    }

    data object Followers : EligibilityItem {
        override val sentence = "You need at least 500 followers."
    }

    data object Strikes : EligibilityItem {
        override val sentence = "Your account must have no active strikes."
    }

    data class Unknown(val raw: String) : EligibilityItem {
        override val sentence = "One more requirement isn't met yet."
    }

    companion object {
        fun fromWire(raw: String): EligibilityItem = when (raw.trim().lowercase()) {
            "age" -> Age
            "pan" -> Pan
            "followers" -> Followers
            "strikes" -> Strikes
            else -> Unknown(raw)
        }
    }
}

internal fun EligibilityDto.toEligibility() = MembershipEligibility(
    // Fail closed: "eligible" with something missing is not eligible.
    eligible = eligible && missing.isEmpty(),
    missing = missing.filter { it.isNotBlank() }.distinct().map(EligibilityItem::fromWire),
)

/** One of the viewer's memberships (`GET /v1/monetization/memberships` and `/memberships/:id`). */
data class MyMembership(
    val subscriptionId: String,
    val creatorId: String,
    /** The creator's display name, else username; blank when the server's profile lookup failed. */
    val creatorName: String = "",
    /** The level's perks: on the detail only. */
    val perks: List<String> = emptyList(),
    /** A move down scheduled for the next period: on the detail only. */
    val pendingDowngrade: PendingDowngrade? = null,
    val levelId: String,
    val levelName: String,
    val pricePaise: Long,
    val status: MembershipStatus,
    /** RFC 3339; when the paid month ends. */
    val currentPeriodEnd: String,
    /** RFC 3339; when access ends, or null when this status grants none. */
    val accessUntil: String?,
    val renewAvailable: Boolean,
) {
    /** Still has access: there is an [accessUntil] and the status is one that grants it. */
    val hasAccess: Boolean get() = accessUntil != null && status.grantsAccess

    /** Cancel is offered where the server takes it: active, payment due, or in grace (service/subscription.go CancelAtPeriodEnd). */
    val canCancel: Boolean
        get() = status in CANCELLABLE

    /** A level change is offered on an active membership only (service/subscription.go UpgradeSubscription). */
    val canChangeLevel: Boolean get() = status == MembershipStatus.Active
}

internal fun MembershipViewDto.toMembership() = MyMembership(
    subscriptionId = subscriptionId,
    creatorId = creator.id,
    creatorName = creator.displayName.trim().ifBlank { creator.username.trim() },
    perks = perksOf(tier.perks),
    pendingDowngrade = pendingDowngrade?.takeIf { it.tierId.isNotBlank() }?.let {
        PendingDowngrade(levelName = it.tierName.trim(), pricePaise = it.pricePaise, effectiveAt = it.effectiveAt)
    },
    levelId = tier.id,
    levelName = tier.name.trim(),
    pricePaise = tier.pricePaise,
    status = MembershipStatus.fromWire(status),
    currentPeriodEnd = currentPeriodEnd,
    accessUntil = accessUntil?.takeIf { it.isNotBlank() },
    renewAvailable = renewAvailable,
)

/** A downgrade waiting for the next period: the level, its price, and when (RFC 3339). */
data class PendingDowngrade(
    val levelName: String,
    val pricePaise: Long,
    val effectiveAt: String,
)

/** A subscription's status (store/postgres/store.go `Subscription.Status`). */
enum class MembershipStatus(val wire: String, val label: String, val grantsAccess: Boolean) {
    PendingPayment("pending_payment", "Waiting for payment", grantsAccess = false),
    Active("active", "Active", grantsAccess = true),
    CancelledAtPeriodEnd(
        "cancelled_at_period_end",
        "Cancelled — access until the end of the month",
        grantsAccess = true
    ),
    Paused("paused", "Paused", grantsAccess = true),
    PastDue("past_due", "Payment due", grantsAccess = true),
    Grace("grace", "Renew to keep access", grantsAccess = true),
    Cancelled("cancelled", "Cancelled", grantsAccess = false),
    Expired("expired", "Ended", grantsAccess = false),

    /** A status newer than this app: shown, never treated as access. */
    Unknown("", "Unknown", grantsAccess = false),
    ;

    companion object {
        fun fromWire(raw: String): MembershipStatus =
            entries.firstOrNull { it != Unknown && it.wire == raw.trim().lowercase() } ?: Unknown
    }
}

/**
 * What a join, renewal or paid upgrade asks to be paid: the server's own
 * figures and session. The client never chooses an amount.
 */
data class MembershipCheckout(
    val subscriptionId: String,
    val paymentId: String,
    val kind: String,
    val status: String,
    val amountPaise: Long,
    val currency: String,
    val clientSession: Map<String, String>,
) {
    /** The payment is open and there is a session to open the sheet with. */
    val payable: Boolean get() = status == STATUS_PENDING_PAYMENT && clientSession.isNotEmpty()

    companion object {
        const val STATUS_PENDING_PAYMENT = "pending_payment"
    }
}

internal fun CheckoutDto.toCheckout() = MembershipCheckout(
    subscriptionId = subscriptionId,
    paymentId = paymentId,
    kind = kind,
    status = status.trim().lowercase(),
    amountPaise = amountPaise,
    currency = currency.ifBlank { INR },
    clientSession = clientSessionMap,
)

/** What a level change did. */
sealed interface LevelChange {
    /** Pay the difference; the level changes when the payment lands. */
    data class Pay(val checkout: MembershipCheckout) : LevelChange

    /** Moved up at once (nothing left to pay this month). */
    data object Upgraded : LevelChange

    /** Moved down from the next month; nothing charged. [effectiveAt] is RFC 3339. */
    data class Scheduled(val effectiveAt: String) : LevelChange

    /** Already on that level. */
    data object Unchanged : LevelChange
}

internal fun UpgradeResultDto.toLevelChange(): LevelChange {
    checkout()?.let { return LevelChange.Pay(it.toCheckout()) }
    return when (status.trim().lowercase()) {
        "scheduled" -> LevelChange.Scheduled(effectiveAt)
        "upgraded" -> LevelChange.Upgraded
        else -> LevelChange.Unchanged
    }
}

/** "₹99" or "₹99.50": integer paise to rupees, no floating point. */
fun rupees(paise: Long): String {
    val whole = paise / PAISE_PER_RUPEE
    val part = paise % PAISE_PER_RUPEE
    val grouped = groupIndian(whole)
    return if (part == 0L) "₹$grouped" else "₹$grouped.${part.toString().padStart(2, '0')}"
}

/** 2999 → "2,999"; 100000 → "1,00,000" (the Indian grouping). */
private fun groupIndian(value: Long): String {
    val digits = value.toString()
    if (digits.length <= THOUSANDS) return digits
    val last = digits.takeLast(THOUSANDS)
    val rest = digits.dropLast(THOUSANDS)
    return rest.reversed().chunked(2).joinToString(",").reversed() + "," + last
}

/** The statuses the server cancels from (service/subscription.go CancelAtPeriodEnd). */
private val CANCELLABLE = setOf(MembershipStatus.Active, MembershipStatus.PastDue, MembershipStatus.Grace)

private const val PAISE_PER_RUPEE = 100L
private const val THOUSANDS = 3
