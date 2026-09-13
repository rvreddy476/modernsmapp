package com.us.android.feature.kitchen.money

import com.us.android.core.food.model.Paise
import java.math.BigInteger

/**
 * Rupees for display and for price entry, from and to [Paise] only.
 *
 * No Double anywhere: display splits an integer, entry parses decimal text.
 */
object RupeeFormat {

    /**
     * `₹1,23,456.78` — Indian digit grouping (the last three digits, then pairs)
     * and always two decimal places, so a column of prices lines up.
     */
    fun format(amount: Paise): String {
        val magnitude = BigInteger.valueOf(amount.value).abs()
        val rupees = magnitude.divide(HUNDRED).toString()
        val paise = magnitude.mod(HUNDRED).toInt().toString().padStart(2, '0')
        val sign = if (amount.value < 0) "-" else ""
        return "$sign₹${groupIndian(rupees)}.$paise"
    }

    /**
     * A typed price — `249`, `249.5`, `₹1,249.50` — as paise, or null for
     * anything else: letters, a negative, more than two decimal places, or more
     * than nine rupee digits. Refusing a third decimal (rather than rounding it)
     * keeps what the partner typed and what customers pay identical.
     */
    fun parse(input: String): Paise? {
        val cleaned = input.trim().removePrefix("₹").trim().replace(",", "")
        if (!ENTRY.matches(cleaned)) return null
        return Paise.fromRupeesDecimal(cleaned.toBigDecimal())
    }

    /** "249.50" for an edit field — no symbol, no grouping, exact. */
    fun toEntryText(amount: Paise): String = amount.toRupeesDecimal().toPlainString()

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
    private val ENTRY = Regex("^[0-9]{1,9}(\\.[0-9]{1,2})?$")
    private const val LAST_GROUP = 3
    private const val PAIR = 2
}
