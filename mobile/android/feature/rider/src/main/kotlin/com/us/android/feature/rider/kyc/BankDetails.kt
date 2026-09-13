package com.us.android.feature.rider.kyc

/*
 * DUPLICATED from :feature:kitchen's kyc/BankDetails.kt (IFSC and account
 * number only). Lift both into :core:kyc-ui together.
 */

/** shared/kyc `NormalizeIFSC`: four letters, a zero, six alphanumerics. FORMAT only. */
object Ifsc {
    private val PATTERN = Regex("^[A-Z]{4}0[A-Z0-9]{6}$")

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

    const val INVALID_MESSAGE = "An account number is 9 to 18 digits"

    private const val MIN_DIGITS = 9
    private const val MAX_DIGITS = 18
}
