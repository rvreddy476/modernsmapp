package com.us.android.core.commerce.model

import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder

/**
 * An amount in Indian paise. 100 paise = ₹1.
 *
 * LB-A1 / LB-19. The backend carried money as `float64` rupees and crossed
 * the payments-service boundary as `int64(round(x * 100))`, so every hop was
 * a lossy IEEE-754 round trip. That is fixed server-side; this type is what
 * stops the app reintroducing it at the other end.
 *
 * A `@JvmInline value class` over `Long` costs nothing at runtime — it
 * compiles to a bare long — while making `Double` money a compile error
 * rather than a code-review question. The CI gate in
 * `.github/workflows/integration-commerce.yml` rejects a `Double` on any
 * money-named property in these modules, but the type system catches it
 * first and catches it everywhere.
 *
 * Division is deliberately absent. Splitting money is never a plain divide —
 * it has to allocate a remainder — and that allocation lives server-side in
 * `internal/tax`, which owns the rule and is property-tested against it. The
 * app displays what it is told.
 */
@JvmInline
@Serializable(with = PaiseSerializer::class)
value class Paise(val value: Long) : Comparable<Paise> {

    operator fun plus(other: Paise) = Paise(value + other.value)
    operator fun minus(other: Paise) = Paise(value - other.value)

    /**
     * Scale by a quantity. Quantity is the only multiplier money is
     * legitimately scaled by here; a percentage would need a rounding rule,
     * and that rule is the server's.
     */
    operator fun times(quantity: Int) = Paise(value * quantity)

    override fun compareTo(other: Paise): Int = value.compareTo(other.value)

    val isZero: Boolean get() = value == 0L
    val isPositive: Boolean get() = value > 0L

    /**
     * Renders "1,234.56" with Indian digit grouping (2,2,3 from the right:
     * ₹12,34,567.89, not ₹1,234,567.89).
     *
     * Integer arithmetic throughout — formatting money by dividing into a
     * Double is how a rounding bug reaches a customer's screen.
     */
    @Suppress("MagicNumber")
    fun format(): String {
        val negative = value < 0
        val abs = if (negative) -value else value
        val rupees = abs / 100
        val paise = abs % 100

        val digits = rupees.toString()
        val grouped = if (digits.length <= 3) {
            digits
        } else {
            val last3 = digits.takeLast(3)
            val rest = digits.dropLast(3)
            val chunks = mutableListOf<String>()
            var remaining = rest
            while (remaining.length > 2) {
                chunks.add(0, remaining.takeLast(2))
                remaining = remaining.dropLast(2)
            }
            if (remaining.isNotEmpty()) chunks.add(0, remaining)
            (chunks + last3).joinToString(",")
        }
        val body = "$grouped.${paise.toString().padStart(2, '0')}"
        return if (negative) "-$body" else body
    }

    /** "₹1,234.56", for display only. */
    fun formatWithSymbol(): String = "₹${format()}"

    companion object {
        val ZERO = Paise(0)

        /** 100 paise to the rupee. The one conversion factor in this type. */
        const val PAISE_PER_RUPEE = 100L

        /** For constants and config only, never for a user-entered amount. */
        fun ofRupees(rupees: Long) = Paise(rupees * PAISE_PER_RUPEE)
    }
}

/**
 * Wire format is a bare integer, always.
 *
 * Decoding REFUSES a fractional number. A `1234.56` in a paise field means
 * the sender is still thinking in rupees, and silently truncating it is
 * precisely how the original defect propagated between services. Better a
 * loud parse failure than a customer charged the wrong amount.
 */
object PaiseSerializer : KSerializer<Paise> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("Paise", PrimitiveKind.LONG)

    override fun serialize(encoder: Encoder, value: Paise) = encoder.encodeLong(value.value)

    override fun deserialize(decoder: Decoder): Paise = Paise(decoder.decodeLong())
}
