package com.us.android.feature.premium.data

import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.util.Locale

/* ── the pass ───────────────────────────────────────────────────── */

enum class PassStatus { CONFIRMING, PAID, FAILED, REFUNDED }

/** What a paid pass cost: the total charged and, when sent, its GST split. */
data class PremiumReceipt(val amountPaise: Long, val tax: PremiumTax?)

data class PremiumPass(
    val passId: String,
    val status: PassStatus,
    val endsAt: String?,
    val receipt: PremiumReceipt?,
    /** The pass's tax invoice once the server has issued it (on the signed event). */
    val invoiceId: String?,
)

/** An unknown status keeps confirming: the server's vocabulary can grow ahead of a released app. */
internal fun PassDto.toPass(): PremiumPass {
    val s = when (status.trim().lowercase()) {
        "paid" -> PassStatus.PAID
        "failed" -> PassStatus.FAILED
        "refunded" -> PassStatus.REFUNDED
        else -> PassStatus.CONFIRMING
    }
    val tax = taxOf(taxablePaise, gstPaise, totalPaise, gstRateBp, taxProvisional)
    val receipt = when {
        amountPaise > 0 -> PremiumReceipt(amountPaise, tax)
        tax != null -> PremiumReceipt(tax.totalPaise, tax)
        else -> null
    }
    return PremiumPass(
        passId = passId,
        status = s,
        endsAt = endsAt?.takeIf { it.isNotBlank() },
        receipt = receipt,
        invoiceId = invoiceId?.takeIf { it.isNotBlank() },
    )
}

/** The success view's lines: Price, GST (18%), Total paid. */
fun PremiumReceipt.lines(): List<MoneyLine> {
    val t = tax ?: return listOf(MoneyLine("Total paid", rupees(amountPaise)))
    return listOf(
        MoneyLine("Price", rupees(t.taxablePaise)),
        MoneyLine("GST (${gstRate(t.rateBp)})", rupees(t.gstPaise)),
        MoneyLine("Total paid", rupees(amountPaise)),
    )
}

/* ── tax invoices and credit notes ──────────────────────────────── */

enum class InvoiceKind { INVOICE, CREDIT_NOTE }

/** One row of GET /premium/invoices. [totalPaise] is signed: a credit note's is negative. */
data class PremiumInvoice(
    val id: String,
    val number: String,
    val kind: InvoiceKind,
    /** The IST calendar date: "2026-11-13". */
    val date: String,
    val totalPaise: Long,
    val currency: String,
    val passId: String,
)

internal fun InvoiceRowDto.toInvoice(): PremiumInvoice? {
    if (id.isBlank() || number.isBlank()) return null
    return PremiumInvoice(
        id = id,
        number = number,
        kind = if (kind == "credit_note") InvoiceKind.CREDIT_NOTE else InvoiceKind.INVOICE,
        date = date,
        totalPaise = totalPaise,
        currency = currency.ifBlank { INR },
        passId = passId,
    )
}

/**
 * The buyer's documents, newest first. The server already orders them; this
 * is a stable backstop on the date (same-day documents keep the server's order).
 */
internal fun List<InvoiceRowDto>.toInvoices(): List<PremiumInvoice> =
    mapNotNull { it.toInvoice() }.sortedByDescending { it.date }

/** One document's detail, as far as the app needs it. */
data class PremiumInvoiceDetail(
    val invoice: PremiumInvoice,
    val buyerName: String?,
    val amountInWords: String,
    val originalNumber: String?,
)

internal fun InvoiceDto.toDetail(): PremiumInvoiceDetail? {
    val row = InvoiceRowDto(id, number, kind, date, totalPaise, currency, passId).toInvoice() ?: return null
    return PremiumInvoiceDetail(
        invoice = row,
        buyerName = buyerName?.takeIf { it.isNotBlank() },
        amountInWords = amountInWords,
        originalNumber = originalInvoice?.number?.takeIf { it.isNotBlank() },
    )
}

/** "13 Nov 2026" from "2026-11-13": the server's date is already the IST day, so it is reformatted, never moved. */
fun invoiceDate(date: String): String =
    runCatching { LocalDate.parse(date.trim()).format(PADDED_DAY_MONTH_YEAR) }.getOrDefault("")

fun InvoiceKind.label(): String = if (this == InvoiceKind.CREDIT_NOTE) "Credit note" else "Tax invoice"

/** "₹175.82"; a credit note is a refund: "−₹175.82" (U+2212). */
fun PremiumInvoice.amountText(): String {
    val abs = rupees(kotlin.math.abs(totalPaise))
    return if (kind == InvoiceKind.CREDIT_NOTE || totalPaise < 0) "−$abs" else abs
}

/**
 * The invoice a credit note reverses: a pass has one invoice and at most one
 * credit note against it (the server's rule), so it is the invoice row with
 * the same pass. Null when that row is not in the list.
 */
fun PremiumInvoice.reversed(all: List<PremiumInvoice>): PremiumInvoice? {
    if (kind != InvoiceKind.CREDIT_NOTE || passId.isBlank()) return null
    return all.firstOrNull { it.kind == InvoiceKind.INVOICE && it.passId == passId }
}

/** "MP/26-27/000001" → "MP-26-27-000001.pdf". */
fun invoiceFileName(number: String): String {
    val safe = number.trim().replace('/', '-').replace(Regex("[^A-Za-z0-9._-]"), "_")
    return "${safe.ifEmpty { "invoice" }}.pdf"
}

/** The web's invoice date keeps the server's two-digit day: "03 Nov 2026". */
private val PADDED_DAY_MONTH_YEAR: DateTimeFormatter = DateTimeFormatter.ofPattern("dd MMM yyyy", Locale.ENGLISH)
