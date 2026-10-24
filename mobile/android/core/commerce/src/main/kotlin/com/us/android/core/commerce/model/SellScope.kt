package com.us.android.core.commerce.model

import java.util.Locale

/**
 * "What will you sell?" — the sell scope a shop chooses at onboarding
 * (commerce-service migration 045, internal/service/sellscope.go; founder,
 * 24 Oct 2026).
 *
 *     ALL_CATEGORIES   needs a GSTIN.
 *     BOOKS_ONLY       needs a PAN, the business name, a business address and
 *                      one proof of address, and no GSTIN. A books-only shop
 *                      lists only in book categories, on GST 0%: printed books
 *                      are GST-exempt, and a shop with no GSTIN cannot charge GST.
 *
 * The server is the authority on every rule here; these mirror it so the
 * seller is told before the round trip. Pure, so each rule is a table test.
 */
enum class SellScope(val wire: String, val label: String, val help: String) {
    ALL_CATEGORIES("all_categories", "All categories", "Sell in any category. A GSTIN is required."),
    BOOKS_ONLY("books_only", "Only books", "Sell printed books only. A PAN is required; no GSTIN needed."),
    ;

    companion object {
        /** Null for a value this build does not know: never read as either scope. */
        fun fromWire(wire: String?): SellScope? = entries.firstOrNull { it.wire == wire }
    }
}

// ─── PAN ─────────────────────────────────────────────────────────────

private val PAN_PATTERN = Regex("^[A-Z]{5}[0-9]{4}[A-Z]$")

/** shared/kyc PANHolderType: the PAN's fourth character. */
private val PAN_HOLDER_BY_LETTER = mapOf(
    'P' to "Individual",
    'C' to "Company",
    'H' to "Hindu undivided family",
    'F' to "Firm",
    'A' to "Association of persons",
    'T' to "Trust",
    'B' to "Body of individuals",
    'L' to "Local authority",
    'J' to "Artificial juridical person",
    'G' to "Government",
)

/** The server's `pan_holder_type` (INDIVIDUAL, COMPANY, …), in the words a seller reads. */
private val PAN_HOLDER_BY_WIRE = mapOf(
    "INDIVIDUAL" to "Individual",
    "COMPANY" to "Company",
    "HUF" to "Hindu undivided family",
    "FIRM" to "Firm",
    "AOP" to "Association of persons",
    "TRUST" to "Trust",
    "BOI" to "Body of individuals",
    "LOCAL_AUTHORITY" to "Local authority",
    "ARTIFICIAL_JURIDICAL_PERSON" to "Artificial juridical person",
    "GOVERNMENT" to "Government",
)

/** Spaces out, upper case: how a PAN or a GSTIN is compared and sent. */
fun normaliseTaxId(raw: String): String = raw.filterNot(Char::isWhitespace).uppercase(Locale.ROOT)

/** What is wrong with a PAN, or null. Layout and holder type, as shared/kyc ValidatePAN. */
fun panProblem(raw: String): String? {
    val pan = normaliseTaxId(raw)
    return when {
        pan.isEmpty() -> "Enter your PAN."
        !PAN_PATTERN.matches(pan) -> "A PAN is five letters, four digits and a letter (like ABCPE1234F)."
        pan[PAN_HOLDER_INDEX] !in PAN_HOLDER_BY_LETTER -> "The fourth letter of a PAN says who holds it; this one is not a holder type."
        else -> null
    }
}

/**
 * The holder type the server answered (`pan_holder_type`), as a seller reads
 * it. A value this build does not know is shown humanised rather than hidden.
 */
fun panHolderLabel(wire: String): String =
    PAN_HOLDER_BY_WIRE[wire] ?: wire.lowercase(Locale.ROOT).replace('_', ' ').replaceFirstChar { it.uppercase() }

private const val PAN_HOLDER_INDEX = 3

// ─── GSTIN ───────────────────────────────────────────────────────────

private val GSTIN_PATTERN = Regex("^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$")
private const val GSTIN_ALPHABET = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
private const val GSTIN_BODY = 14
private const val GSTIN_RADIX = 36
private const val GSTIN_PAN_START = 2
private const val GSTIN_PAN_END = 12
private const val GSTIN_HOLDER_INDEX = GSTIN_PAN_START + PAN_HOLDER_INDEX

/** shared/kyc GSTINCheckDigit: mod 36 over the first fourteen, weights 1 and 2 alternating. */
fun gstinCheckCharacter(first14: String): Char? {
    if (first14.length != GSTIN_BODY) return null
    var sum = 0
    for (i in 0 until GSTIN_BODY) {
        val value = GSTIN_ALPHABET.indexOf(first14[i])
        if (value < 0) return null
        val product = value * (if (i % 2 == 0) 1 else 2)
        sum += product / GSTIN_RADIX + product % GSTIN_RADIX
    }
    return GSTIN_ALPHABET[(GSTIN_RADIX - sum % GSTIN_RADIX) % GSTIN_RADIX]
}

/** What is wrong with a GSTIN, or null. Layout and check character; the server also checks the state code. */
fun gstinProblem(raw: String): String? {
    val gstin = normaliseTaxId(raw)
    return when {
        gstin.isEmpty() -> "Enter your GSTIN."
        !GSTIN_PATTERN.matches(gstin) ->
            "A GSTIN is 15 characters: state code, PAN, entity number, Z and a check character."
        gstin[GSTIN_HOLDER_INDEX] !in PAN_HOLDER_BY_LETTER -> "The PAN inside this GSTIN has no recognised holder type."
        gstinCheckCharacter(gstin.take(GSTIN_BODY)) != gstin[GSTIN_BODY] ->
            "The last character does not match; check the GSTIN for a typo."
        else -> null
    }
}

/** The PAN a GSTIN is registered to (characters 3 to 12). */
fun gstinPan(raw: String): String = normaliseTaxId(raw).let {
    if (it.length >= GSTIN_PAN_END) it.substring(GSTIN_PAN_START, GSTIN_PAN_END) else ""
}

// ─── The step ────────────────────────────────────────────────────────

/** The "What will you sell?" form, as typed. */
data class SellScopeDraft(
    val scope: SellScope = SellScope.ALL_CATEGORIES,
    val gstin: String = "",
    val pan: String = "",
    val businessName: String = "",
)

enum class SellScopeField { GSTIN, PAN, BUSINESS_NAME }

private const val BUSINESS_NAME_MIN = 2
private const val BUSINESS_NAME_MAX = 200

/**
 * The step's own checks (service.validateSellScope): the GSTIN on the
 * all-categories path, the PAN and the business name on the books path. An
 * optional business name on the all-categories path is still held to its
 * length. Empty when the step may go.
 */
fun sellScopeProblems(draft: SellScopeDraft): Map<SellScopeField, String> {
    val problems = mutableMapOf<SellScopeField, String>()
    val name = draft.businessName.trim()
    val nameOk = name.length in BUSINESS_NAME_MIN..BUSINESS_NAME_MAX
    when (draft.scope) {
        SellScope.ALL_CATEGORIES -> {
            gstinProblem(draft.gstin)?.let { problems[SellScopeField.GSTIN] = it }
            if (name.isNotEmpty() && !nameOk) {
                problems[SellScopeField.BUSINESS_NAME] = "Keep the business name between 2 and 200 characters."
            }
        }

        SellScope.BOOKS_ONLY -> {
            panProblem(draft.pan)?.let { problems[SellScopeField.PAN] = it }
            if (!nameOk) problems[SellScopeField.BUSINESS_NAME] = "Enter the business name (2 to 200 characters)."
        }
    }
    return problems
}

/**
 * What the step sends: only the fields the chosen path takes. The server
 * refuses a GSTIN on the books path and a PAN on the other
 * (SELL_SCOPE_FIELD_NOT_TAKEN), so a value typed before switching never
 * rides along.
 */
data class SellScopeChoice(
    val scope: SellScope,
    val gstin: String? = null,
    val pan: String? = null,
    val businessName: String? = null,
)

fun sellScopeChoice(draft: SellScopeDraft): SellScopeChoice {
    val name = draft.businessName.trim().takeIf { it.isNotEmpty() }
    return when (draft.scope) {
        SellScope.ALL_CATEGORIES -> SellScopeChoice(draft.scope, gstin = normaliseTaxId(draft.gstin), businessName = name)
        SellScope.BOOKS_ONLY -> SellScopeChoice(draft.scope, pan = normaliseTaxId(draft.pan), businessName = name)
    }
}

/**
 * What the step stored, as the seller may see it again. The PAN comes back
 * masked with its holder type, never in full.
 */
data class SellScopeSaved(
    val scope: SellScope?,
    val gstin: String?,
    val businessName: String?,
    val panMasked: String?,
    val panHolderType: String?,
)

/** `GET /seller/sell-scope` and the upgrade's answer. */
data class SellScopeView(
    val scope: SellScope?,
    val hasGstin: Boolean,
    /** Every category a books-only shop may list in (the flagged ones and their children). */
    val bookCategoryIds: List<String>,
    /** A books-only shop may add a GSTIN and sell in every category. */
    val canUpgrade: Boolean,
) {
    val booksOnly: Boolean get() = scope == SellScope.BOOKS_ONLY
}

// ─── Listing as a books-only shop ────────────────────────────────────

/** The categories a books-only shop may pick: the listable choices that are book categories. */
fun bookCategoryChoices(choices: List<CategoryChoice>, bookCategoryIds: Collection<String>): List<CategoryChoice> {
    val allowed = bookCategoryIds.toSet()
    return choices.filter { it.id in allowed }
}

/** The tax classes a books-only shop may choose: the zero-rated ones. */
fun zeroRatedTaxClasses(classes: List<TaxClass>): List<TaxClass> = classes.filter { it.ratePercent == 0.0 }

// ─── Proof of address ────────────────────────────────────────────────

/** A two-sided document: the address is on the back, so both sides are sent. */
val SellerDocumentType.twoSided: Boolean
    get() = this == SellerDocumentType.VOTER_ID || this == SellerDocumentType.PASSPORT

/** The documents a books-only seller may prove their address with (Flipkart's "Only Books" list). */
val SellerDocumentType.provesAddress: Boolean
    get() = this in ADDRESS_PROOF_TYPES

/**
 * Stored as the upload alone: the "number" on a bill or a bank statement is
 * an account number, and the server refuses one (DOCUMENT_NUMBER_NOT_ACCEPTED).
 */
val SellerDocumentType.imageOnly: Boolean
    get() = this == SellerDocumentType.ELECTRICITY_BILL ||
        this == SellerDocumentType.TELEPHONE_BILL ||
        this == SellerDocumentType.BANK_STATEMENT

val ADDRESS_PROOF_TYPES: List<SellerDocumentType> = listOf(
    SellerDocumentType.VOTER_ID,
    SellerDocumentType.PASSPORT,
    SellerDocumentType.ELECTRICITY_BILL,
    SellerDocumentType.TELEPHONE_BILL,
    SellerDocumentType.BANK_STATEMENT,
)
