package com.us.android.core.food.model

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.JsonUnquotedLiteral
import java.math.BigDecimal
import java.math.RoundingMode

/**
 * An amount in Indian paise. 100 paise = ₹1.
 *
 * A value class over Long, so a Double amount is a compile error rather than a
 * review comment. Serialises as the bare integer, which is what food-service's
 * `*_paise` fields carry.
 *
 * Several food-service routes still speak FLOAT RUPEES on the wire — menu item
 * `base_price`, the order `totals`, the kitchen queue's `final_amount`, the
 * earnings summary and settlements. Those fields use [RupeesAsPaiseSerializer],
 * which converts through the JSON number's decimal TEXT, never through a
 * Double. That float-rupee wire is a backend gap reported with Feast A3.
 */
@Serializable
@JvmInline
value class Paise(val value: Long) : Comparable<Paise> {

    operator fun plus(other: Paise): Paise = Paise(Math.addExact(value, other.value))

    operator fun minus(other: Paise): Paise = Paise(Math.subtractExact(value, other.value))

    override fun compareTo(other: Paise): Int = value.compareTo(other.value)

    /** The exact rupee amount: 24950 paise is `249.50`. */
    fun toRupeesDecimal(): BigDecimal = BigDecimal.valueOf(value, 2)

    companion object {
        val ZERO = Paise(0)

        /**
         * Rupees to paise, rounding a third decimal half away from zero — the
         * rounding food-service's `roundMoney` applies — so a wire value such as
         * `0.30000000000000004` reads as 30 paise, not 30.000…4.
         *
         * @throws ArithmeticException when the amount does not fit in a Long.
         */
        fun fromRupeesDecimal(rupees: BigDecimal): Paise =
            Paise(rupees.setScale(2, RoundingMode.HALF_UP).movePointRight(2).longValueExact())
    }
}

/**
 * A float-rupee JSON number ⇄ [Paise], through decimal text only.
 *
 * Reading takes the number's literal content (`249.5`, `1e3`) into a
 * BigDecimal; writing emits the exact two-place decimal (`249.50`) as an
 * unquoted JSON number. No Double is created in either direction.
 */
object RupeesAsPaiseSerializer : KSerializer<Paise> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("com.us.android.core.food.RupeesAsPaise", PrimitiveKind.DOUBLE)

    override fun deserialize(decoder: Decoder): Paise = try {
        Paise.fromRupeesDecimal(readDecimal(decoder))
    } catch (e: ArithmeticException) {
        throw SerializationException("rupee amount out of range: ${e.message}")
    }

    override fun serialize(encoder: Encoder, value: Paise) = writeDecimal(encoder, value.toRupeesDecimal())
}

/**
 * A JSON number ⇄ BigDecimal, through its text. For non-money decimals the
 * server sends as float — `tax_percentage` — so a `5` stays `5` and is never
 * printed back as `5.000000000000001`.
 */
object WireDecimalSerializer : KSerializer<BigDecimal> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("com.us.android.core.food.WireDecimal", PrimitiveKind.DOUBLE)

    override fun deserialize(decoder: Decoder): BigDecimal = readDecimal(decoder)

    override fun serialize(encoder: Encoder, value: BigDecimal) = writeDecimal(encoder, value)
}

private fun readDecimal(decoder: Decoder): BigDecimal {
    val content = if (decoder is JsonDecoder) {
        val element = decoder.decodeJsonElement()
        if (element !is JsonPrimitive || element is JsonNull) {
            throw SerializationException("expected a decimal number, got $element")
        }
        element.content
    } else {
        decoder.decodeString()
    }
    return content.trim().toBigDecimalOrNull()
        ?: throw SerializationException("'$content' is not a decimal number")
}

@OptIn(ExperimentalSerializationApi::class)
private fun writeDecimal(encoder: Encoder, value: BigDecimal) {
    if (encoder is JsonEncoder) {
        // NOT JsonPrimitive(value): its serializer re-encodes a non-integer
        // number through Double (249.50 went out as 249.5, 199.00 as 199.0 —
        // caught by RupeeFormatTest). An unquoted literal writes the decimal
        // text exactly, which is kotlinx.serialization's documented route for
        // BigDecimal.
        encoder.encodeJsonElement(JsonUnquotedLiteral(value.toPlainString()))
    } else {
        encoder.encodeString(value.toPlainString())
    }
}
