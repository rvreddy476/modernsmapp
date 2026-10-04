package com.us.android.feature.doorsteppro.model

import java.math.BigInteger

/**
 * Money in integer paise — the only money type Doorstep Pro has. COPIED from
 * :feature:doorstep (features may not share code). A Double amount
 * is a compile error, not a review comment. Every figure on screen is one the
 * server stated (`*_paise`); the client adds them up only to preview a
 * selection before the server's quote replaces the preview.
 */
@JvmInline
value class Paise(val value: Long) : Comparable<Paise> {

    operator fun plus(other: Paise): Paise = Paise(Math.addExact(value, other.value))

    operator fun minus(other: Paise): Paise = Paise(Math.subtractExact(value, other.value))

    operator fun times(quantity: Int): Paise = Paise(Math.multiplyExact(value, quantity.toLong()))

    override fun compareTo(other: Paise): Int = value.compareTo(other.value)

    val isPositive: Boolean get() = value > 0

    companion object {
        val ZERO = Paise(0)
    }
}

fun Iterable<Paise>.sum(): Paise = fold(Paise.ZERO) { acc, p -> acc + p }

/** `₹2,248.00` with Indian digit grouping (lakh, crore). Exact: integer arithmetic only. */
fun Paise.toRupeeText(): String {
    val magnitude = BigInteger.valueOf(value).abs()
    val rupees = magnitude.divide(HUNDRED).toString()
    val paise = magnitude.mod(HUNDRED).toInt().toString().padStart(2, '0')
    val sign = if (value < 0) "-" else ""
    return "$sign₹${groupIndian(rupees)}.$paise"
}

/** `₹249` when the paise are zero, otherwise `₹249.50`. For catalogue prices. */
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
