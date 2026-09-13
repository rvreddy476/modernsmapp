package com.us.android.feature.kitchen.kyc

/**
 * shared/kyc `NormalizeIFSC`: trim, upper-case, then the RBI layout — four
 * letters (bank), a zero, six alphanumerics (branch). FORMAT only; RazorpayX and
 * the penny drop are where the truth comes from.
 */
object Ifsc {
    private val PATTERN = Regex("^[A-Z]{4}0[A-Z0-9]{6}$")

    /** The normalised IFSC, or null when it is not well formed. */
    fun normalize(input: String): String? = input.trim().uppercase().takeIf { PATTERN.matches(it) }

    const val INVALID_MESSAGE = "An IFSC is four letters, a zero and six letters or digits"
}

/** shared/kyc `NormalizeBankAccountNumber`: 9 to 18 digits and nothing else, after a trim. */
object BankAccountNumber {

    fun normalize(input: String): String? {
        val n = input.trim()
        if (n.length !in MIN_DIGITS..MAX_DIGITS) return null
        return n.takeIf { value -> value.all { it in '0'..'9' } }
    }

    /** The part that may be shown in the clear (shared/kyc `BankAccountLast4`). */
    fun last4(input: String): String {
        val n = input.trim()
        return if (n.length <= LAST_DIGITS) n else n.takeLast(LAST_DIGITS)
    }

    const val INVALID_MESSAGE = "An account number is 9 to 18 digits"

    private const val MIN_DIGITS = 9
    private const val MAX_DIGITS = 18
    private const val LAST_DIGITS = 4
}

/**
 * shared/kyc `ValidateFSSAILicence`: trim, drop inner spaces, exactly fourteen
 * ASCII digits. FORMAT only — nothing confirms with FoSCoS.
 */
object FssaiLicence {

    fun normalize(input: String): String? {
        val n = normalizeAscii(input, drop = " ") ?: return null
        return n.takeIf { value -> value.length == DIGITS && value.all { it in '0'..'9' } }
    }

    const val INVALID_MESSAGE = "An FSSAI licence number is 14 digits"

    private const val DIGITS = 14
}
