package com.us.android.feature.premium.data

import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
 * Momentum Premium as the screens see it, and every word and number they show,
 * as pure functions (the web's src/features/premium/model.ts, same words).
 *
 * Prices EXCLUDE GST (founder, 13 Nov 2026): price_paise is the taxable value,
 * GST is added on top, and total_paise (= checkout amount_paise) is what the
 * buyer pays. The app shows the server's numbers only: no tax is ever
 * computed here.
 */

/** The server's GST split of one price. Read, never computed. */
data class PremiumTax(
    val taxablePaise: Long,
    val gstPaise: Long,
    val totalPaise: Long,
    /** 1800 = 18%. */
    val rateBp: Int,
    val provisional: Boolean,
)

/**
 * The breakdown, or null when the server sent none (a pass opened before 13
 * Nov 2026, a plan with no price). All four numbers must be there and none
 * negative: a half breakdown is not shown.
 */
internal fun taxOf(taxable: Long?, gst: Long?, total: Long?, rateBp: Int?, provisional: Boolean): PremiumTax? {
    val tax = PremiumTax(
        taxablePaise = taxable ?: return null,
        gstPaise = gst ?: return null,
        totalPaise = total ?: return null,
        rateBp = rateBp ?: return null,
        provisional = provisional,
    )
    val negative = minOf(tax.taxablePaise, tax.gstPaise) < 0 || tax.rateBp < 0
    return if (negative || tax.totalPaise <= 0) null else tax
}

data class PremiumPlan(
    val code: String,
    val name: String,
    val durationDays: Int,
    /** The price before GST; null while the founder has not set a price. */
    val pricePaise: Long?,
    /** The server's GST on top of it; null when it could not be worked out. */
    val tax: PremiumTax?,
    val currency: String,
    val available: Boolean,
)

internal fun PlanDto.toPlan(): PremiumPlan? {
    if (code.isBlank()) return null
    return PremiumPlan(
        code = code,
        name = name,
        durationDays = durationDays,
        pricePaise = pricePaise?.takeIf { it >= 0 },
        tax = taxOf(taxablePaise, gstPaise, totalPaise, gstRateBp, taxProvisional),
        currency = currency.ifBlank { INR },
        available = available,
    )
}

internal fun List<PlanDto>.toPlans(): List<PremiumPlan> = mapNotNull { it.toPlan() }

/**
 * A plan can be bought only with a price the server set, its GST and total
 * (never offered at the pre-tax price alone), AND its own yes.
 */
fun PremiumPlan.canBuy(): Boolean = available && pricePaise != null && pricePaise > 0 && tax != null

/** "₹149" (before GST), or "Not available" when it can't be bought. */
fun PremiumPlan.priceText(): String =
    if (canBuy() && pricePaise != null) rupees(pricePaise) else PremiumWords.NOT_AVAILABLE

/** The card's breakdown line, or "" when the plan can't be bought. */
fun PremiumPlan.breakdownText(): String = if (canBuy() && tax != null) breakdownLine(tax) else ""

/** "30 days", "365 days", "1 day". */
fun planLength(durationDays: Int): String = "$durationDays ${if (durationDays == 1) "day" else "days"}"

/** The card's heading: the server's name, else the length. */
fun PremiumPlan.title(): String = name.ifBlank { "Premium for ${planLength(durationDays)}" }

/** "₹149", "₹26.82", "₹1,768.82": integer paise to rupees, Indian grouping, no floating point. */
fun rupees(paise: Long): String {
    val sign = if (paise < 0) "-" else ""
    val abs = kotlin.math.abs(paise)
    val whole = abs / PAISE_PER_RUPEE
    val part = abs % PAISE_PER_RUPEE
    val grouped = groupIndian(whole)
    return if (part == 0L) "$sign₹$grouped" else "$sign₹$grouped.${part.toString().padStart(2, '0')}"
}

private fun groupIndian(value: Long): String {
    val digits = value.toString()
    if (digits.length <= THOUSANDS) return digits
    val last = digits.takeLast(THOUSANDS)
    val rest = digits.dropLast(THOUSANDS)
    return rest.reversed().chunked(2).joinToString(",").reversed() + "," + last
}

/** "18%" from 1800 basis points; "12.5%" from 1250. */
fun gstRate(rateBp: Int): String {
    val whole = rateBp / BP_PER_PERCENT
    val part = rateBp % BP_PER_PERCENT
    if (part == 0) return "$whole%"
    return "$whole.${part.toString().padStart(2, '0').trimEnd('0')}%"
}

/** "₹149 + ₹26.82 GST (18%) = ₹175.82", the server's numbers as they are. */
fun breakdownLine(t: PremiumTax): String =
    "${rupees(t.taxablePaise)} + ${rupees(t.gstPaise)} GST (${gstRate(t.rateBp)}) = ${rupees(t.totalPaise)}"

/* ── my status ──────────────────────────────────────────────────── */

data class PremiumStatus(
    val active: Boolean,
    val inGrace: Boolean,
    val endsAt: String?,
    val graceEndsAt: String?,
)

internal fun StatusDto.toStatus(): PremiumStatus = PremiumStatus(
    active = active,
    inGrace = inGrace,
    endsAt = endsAt?.takeIf { it.isNotBlank() },
    graceEndsAt = graceEndsAt?.takeIf { it.isNotBlank() },
)

enum class StatusTone { ACTIVE, GRACE, NONE }

data class StatusWords(val tone: StatusTone, val title: String, val detail: String)

/** What the screen says about the viewer's Premium, from GET /premium/me. */
fun PremiumStatus.words(zone: ZoneId = IST): StatusWords {
    if (active && inGrace) {
        val end = premiumDate(graceEndsAt ?: endsAt, zone)
        return StatusWords(
            StatusTone.GRACE,
            if (end.isNotEmpty()) "Premium ends on $end" else "Premium is ending",
            "Renew to keep posting on Echo.",
        )
    }
    if (active) {
        val end = premiumDate(endsAt, zone)
        return StatusWords(
            StatusTone.ACTIVE,
            if (end.isNotEmpty()) "Premium is active until $end" else "Premium is active",
            "Buying another pass adds its days on top.",
        )
    }
    return StatusWords(StatusTone.NONE, "You don't have Premium", "Buy a pass below to start posting on Echo.")
}

/** "Premium is active until 13 Dec 2026" after a paid pass. */
fun paidWords(endsAt: String?, zone: ZoneId = IST): String {
    val end = premiumDate(endsAt, zone)
    return if (end.isNotEmpty()) "Premium is active until $end" else "Premium is active"
}

/** "13 Dec 2026" from an RFC 3339 instant, read in IST (the web does the same); "" when unreadable. */
fun premiumDate(rfc3339: String?, zone: ZoneId = IST): String {
    val raw = rfc3339?.trim().orEmpty()
    if (raw.isEmpty()) return ""
    return runCatching {
        OffsetDateTime.parse(raw).atZoneSameInstant(zone).format(DAY_MONTH_YEAR)
    }.getOrDefault("")
}

/* ── checkout ───────────────────────────────────────────────────── */

data class PremiumCheckout(
    val passId: String,
    val plan: String,
    val durationDays: Int,
    val status: String,
    /** What the buyer pays: the server's GST-inclusive total, relayed to Razorpay, never sent to our server. */
    val amountPaise: Long,
    val currency: String,
    val intentId: String,
    /** The server's `client_session`: provider, order_id, key_id. Empty when it sent none. */
    val clientSession: Map<String, String>,
    /** The GST split of [amountPaise]; null on a pass opened before 13 Nov 2026. */
    val tax: PremiumTax?,
)

internal fun CheckoutDto.toCheckout(): PremiumCheckout = PremiumCheckout(
    passId = passId,
    plan = plan,
    durationDays = durationDays,
    status = status,
    amountPaise = amountPaise,
    currency = currency.ifBlank { INR },
    intentId = intentId,
    clientSession = clientSession.orEmpty(),
    tax = taxOf(taxablePaise, gstPaise, totalPaise, gstRateBp, taxProvisional),
)

/**
 * The server says the charge (amount_paise) is the GST total; if the two ever
 * disagree the sheet is not opened (the server refuses the same thing on its
 * side as PREMIUM_AMOUNT_MISMATCH).
 */
fun PremiumCheckout.amountsDisagree(): Boolean = tax != null && tax.totalPaise != amountPaise

/** One label-and-amount line of the confirm step or the receipt. */
data class MoneyLine(val label: String, val value: String)

/** The confirm step: Price, GST (18%), Total to pay — the checkout's numbers. */
fun PremiumCheckout.lines(): List<MoneyLine> {
    val t = tax ?: return listOf(MoneyLine("Total to pay", rupees(amountPaise)))
    return listOf(
        MoneyLine("Price", rupees(t.taxablePaise)),
        MoneyLine("GST (${gstRate(t.rateBp)})", rupees(t.gstPaise)),
        MoneyLine("Total to pay", rupees(amountPaise)),
    )
}

/** The words the screens show, the web's own. */
object PremiumWords {
    const val TITLE = "Momentum Premium"
    const val SUBTITLE = "Passes for a set number of days. Pay once; nothing renews by itself."
    const val NOT_AVAILABLE = "Not available"
    const val NO_AUTO_RENEW = "No auto-renew: we'll remind you 3 days before it ends."
    const val PERKS_HEADING = "What Premium gives you"
    const val PERK_ECHO = "Post on Echo: notes, threads and quotes."
    const val PERKS_FREE = "Reading, liking, reposting and replying on Echo stay free."
    const val CHOOSE = "Choose a pass"
    const val CONFIRM_HEADING = "Confirm your pass"
    const val OPENING = "Opening checkout…"
    const val IN_SHEET = "Complete the payment in the Razorpay window."
    const val CONFIRMING = "Confirming your payment…"

    const val NOT_ENABLED = "Premium is not available yet"
    const val NOT_ENABLED_DETAIL = "We'll let you know when you can get it."
    const val PRICE_NOT_SET = "This pass can't be bought yet."
    const val TAX_UNAVAILABLE =
        "This pass can't be bought right now: we couldn't work out its GST. You haven't been charged. Try again later."
    const val AMOUNT_MISMATCH =
        "The price changed while checkout was opening. You haven't been charged. Reload the page and try again."
    const val UNKNOWN_PLAN = "That pass isn't on sale any more. Pick another one."
    const val PAYMENTS_UNAVAILABLE = "Payments are unavailable right now. Try again in a few minutes."
    const val SESSION_UNAVAILABLE = "Checkout could not be opened. Try again."
    const val NOT_FOUND = "We couldn't find this payment."
    const val MAINTENANCE = "Premium is paused for maintenance. Try again later."
    const val BAD_REQUEST = "Something went wrong starting checkout. Try again."
    const val NETWORK = "No connection. Check your network and try again."
    const val GENERIC = "Something went wrong. Try again."
    const val NO_ONLINE_PAYMENT = "Online payment isn't available right now. Try again later."
    const val FAILED = "The payment didn't go through. You haven't been charged for Premium."
    const val REFUNDED = "This payment was refunded, so Premium wasn't added."
    const val STILL_CONFIRMING =
        "We're still confirming your payment. If money left your account, Premium will be added or the money " +
            "refunded. You can check again or come back later."
    const val DISMISSED = "Payment wasn't completed."

    const val INVOICES = "Invoices"
    const val DOWNLOAD_INVOICE = "Download invoice"
    const val DOWNLOAD_ROW = "Download"
    const val INVOICE_PREPARING = "Your invoice will be ready in a moment"
    const val INVOICE_LATER = "Your invoice will appear under Invoices shortly."
    const val INVOICE_NOT_FOUND = "Invoice not found"
    const val INVOICE_RETRY = "Couldn't download this invoice. Try again."
    const val INVOICE_SAVED = "Invoice saved."
    const val INVOICE_NO_FILE = "That file couldn't be opened. Pick another place."
}

/** India Standard Time: invoice dates and pass ends are read the way the web reads them. */
val IST: ZoneId = ZoneId.of("Asia/Kolkata")

private val DAY_MONTH_YEAR: DateTimeFormatter = DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH)

internal const val INR = "INR"
private const val PAISE_PER_RUPEE = 100L
private const val THOUSANDS = 3
private const val BP_PER_PERCENT = 100
