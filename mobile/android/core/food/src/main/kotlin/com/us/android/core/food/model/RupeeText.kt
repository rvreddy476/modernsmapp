package com.us.android.core.food.model

import java.math.BigInteger

/**
 * `₹1,23,456.78` from [Paise] — Indian digit grouping (the last three digits,
 * then pairs) and always two decimal places. Integer arithmetic only: no
 * Double is created.
 *
 * The customer app (Feast in Momentum) formats every amount through this. The
 * Kitchen app's RupeeFormat predates it and formats identically.
 */
fun Paise.toRupeeText(): String {
    val magnitude = BigInteger.valueOf(value).abs()
    val rupees = magnitude.divide(HUNDRED).toString()
    val paise = magnitude.mod(HUNDRED).toInt().toString().padStart(2, '0')
    val sign = if (value < 0) "-" else ""
    return "$sign₹${groupIndian(rupees)}.$paise"
}

/** `₹249` when the paise are zero, otherwise `₹249.50`. For menu prices. */
fun Paise.toShortRupeeText(): String {
    val full = toRupeeText()
    return if (value % PAISE_PER_RUPEE == 0L) full.removeSuffix(".00") else full
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
private const val PAISE_PER_RUPEE = 100L
private const val LAST_GROUP = 3
private const val PAIR = 2
