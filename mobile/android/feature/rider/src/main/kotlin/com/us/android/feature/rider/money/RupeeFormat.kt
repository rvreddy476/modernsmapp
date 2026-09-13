package com.us.android.feature.rider.money

import com.us.android.core.food.model.Paise
import java.math.BigInteger

/**
 * Rupees for display, from [Paise] only — no Double anywhere.
 *
 * DUPLICATED from :feature:kitchen's money/RupeeFormat.kt (display half only).
 * Lift into a shared module with the kyc-ui lift.
 */
object RupeeFormat {

    /** `₹1,23,456.78` — Indian digit grouping and always two decimal places. */
    fun format(amount: Paise): String {
        val magnitude = BigInteger.valueOf(amount.value).abs()
        val rupees = magnitude.divide(HUNDRED).toString()
        val paise = magnitude.mod(HUNDRED).toInt().toString().padStart(2, '0')
        val sign = if (amount.value < 0) "-" else ""
        return "$sign₹${groupIndian(rupees)}.$paise"
    }

    internal fun groupIndian(digits: String): String {
        if (digits.length <= LAST_GROUP) return digits
        val tail = digits.takeLast(LAST_GROUP)
        var head = digits.dropLast(LAST_GROUP)
        val groups = ArrayDeque<String>()
        while (head.length > PAIR) {
            groups.addFirst(head.takeLast(PAIR))
            head = head.dropLast(PAIR)
        }
        if (head.isNotEmpty()) groups.addFirst(head)
        return groups.joinToString(",") + "," + tail
    }

    private val HUNDRED: BigInteger = BigInteger.valueOf(100)
    private const val LAST_GROUP = 3
    private const val PAIR = 2
}
