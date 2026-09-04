package com.us.android.feature.tube.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.sign

/**
 * The BlurHash a media row carries, decoded — the placeholder a video card
 * shows while its cover loads (Tube, 2026-09-05). A few dozen DCT
 * coefficients become a soft, correctly coloured wash at the card's aspect,
 * so the grid has colour on its first frame instead of grey boxes.
 *
 * The reference algorithm (github.com/woltapp/blurhash), pure Kotlin so
 * [decode] is a unit test; the only Android in this file is the bitmap the
 * composable wraps it in. A hash that does not parse decodes to nothing and
 * the card draws its plain surface — never a crash on a bad string.
 */
object BlurHash {

    /**
     * The pixels of [hash] at [width] × [height], ARGB, row-major; null for
     * a string that is not a BlurHash. Small sizes are the point — 32 × 18
     * is plenty for a wash the renderer scales up.
     */
    fun decode(hash: String, width: Int, height: Int, punch: Float = 1f): IntArray? {
        if (hash.length < MIN_LENGTH || width <= 0 || height <= 0) return null
        val sizeFlag = decode83(hash, 0, 1) ?: return null
        val componentsY = sizeFlag / MAX_COMPONENTS + 1
        val componentsX = sizeFlag % MAX_COMPONENTS + 1
        if (hash.length != HEADER_LENGTH + 2 * componentsX * componentsY) return null
        val quantisedMax = decode83(hash, 1, 2) ?: return null
        val maxValue = (quantisedMax + 1) / QUANT_SCALE
        val colours = ArrayList<FloatArray>(componentsX * componentsY)
        colours += decodeDc(decode83(hash, 2, DC_END) ?: return null)
        for (i in 1 until componentsX * componentsY) {
            val start = DC_END + (i - 1) * 2
            colours += decodeAc(decode83(hash, start, start + 2) ?: return null, maxValue * punch)
        }
        return IntArray(width * height).also { pixels ->
            for (y in 0 until height) {
                for (x in 0 until width) {
                    pixels[y * width + x] = pixelAt(x, y, width, height, colours, componentsX)
                }
            }
        }
    }

    private fun pixelAt(x: Int, y: Int, width: Int, height: Int, colours: List<FloatArray>, componentsX: Int): Int {
        var r = 0f
        var g = 0f
        var b = 0f
        colours.forEachIndexed { index, colour ->
            val basis = cos(PI * x * (index % componentsX) / width).toFloat() *
                cos(PI * y * (index / componentsX) / height).toFloat()
            r += colour[0] * basis
            g += colour[1] * basis
            b += colour[2] * basis
        }
        return (OPAQUE shl ALPHA_SHIFT) or (linearToSrgb(r) shl RED_SHIFT) or
            (linearToSrgb(g) shl GREEN_SHIFT) or linearToSrgb(b)
    }

    private fun decode83(hash: String, from: Int, to: Int): Int? {
        var value = 0
        for (i in from until to) {
            val digit = ALPHABET.indexOf(hash[i])
            if (digit < 0) return null
            value = value * BASE + digit
        }
        return value
    }

    private fun decodeDc(value: Int): FloatArray = floatArrayOf(
        srgbToLinear((value shr RED_SHIFT) and BYTE),
        srgbToLinear((value shr GREEN_SHIFT) and BYTE),
        srgbToLinear(value and BYTE),
    )

    private fun decodeAc(value: Int, maxValue: Float): FloatArray {
        val quantR = value / (AC_STEPS * AC_STEPS)
        val quantG = (value / AC_STEPS) % AC_STEPS
        val quantB = value % AC_STEPS
        return floatArrayOf(
            signPow((quantR - AC_MID) / AC_MID.toFloat(), 2f) * maxValue,
            signPow((quantG - AC_MID) / AC_MID.toFloat(), 2f) * maxValue,
            signPow((quantB - AC_MID) / AC_MID.toFloat(), 2f) * maxValue,
        )
    }

    private fun signPow(value: Float, exp: Float): Float = sign(value) * abs(value).pow(exp)

    private fun srgbToLinear(value: Int): Float {
        val v = value / BYTE.toFloat()
        return if (v <= SRGB_CUTOFF) v / SRGB_LINEAR_DIVISOR else ((v + SRGB_OFFSET) / SRGB_SCALE).pow(GAMMA)
    }

    private fun linearToSrgb(value: Float): Int {
        val v = value.coerceIn(0f, 1f)
        val srgb = if (v <= LINEAR_CUTOFF) v * SRGB_LINEAR_DIVISOR else SRGB_SCALE * v.pow(1f / GAMMA) - SRGB_OFFSET
        return (srgb * BYTE + HALF).toInt().coerceIn(0, BYTE)
    }

    private const val ALPHABET =
        "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#\$%*+,-.:;=?@[]^_{|}~"
    private const val BASE = 83
    private const val MIN_LENGTH = 6
    private const val HEADER_LENGTH = 4
    private const val DC_END = 6
    private const val MAX_COMPONENTS = 9
    private const val QUANT_SCALE = 166f
    private const val AC_STEPS = 19
    private const val AC_MID = 9
    private const val BYTE = 255
    private const val OPAQUE = 0xFF
    private const val ALPHA_SHIFT = 24
    private const val RED_SHIFT = 16
    private const val GREEN_SHIFT = 8
    private const val HALF = 0.5f
    private const val SRGB_CUTOFF = 0.04045f
    private const val LINEAR_CUTOFF = 0.0031308f
    private const val SRGB_LINEAR_DIVISOR = 12.92f
    private const val SRGB_OFFSET = 0.055f
    private const val SRGB_SCALE = 1.055f
    private const val GAMMA = 2.4f
}

/**
 * The decoded wash as an image, or nothing when [hash] is blank or bad.
 * Decoded once per hash and kept: a 32 × 18 decode is cheap, but a card
 * list recomposes on every scroll frame and must not redo it.
 */
@Composable
fun BlurHashImage(hash: String, modifier: Modifier = Modifier) {
    val bitmap = remember(hash) { hash.takeIf { it.isNotBlank() }?.let(::blurHashBitmap) } ?: return
    Image(
        bitmap = bitmap,
        contentDescription = null,
        contentScale = ContentScale.Crop,
        modifier = modifier,
    )
}

private fun blurHashBitmap(hash: String): ImageBitmap? {
    val pixels = BlurHash.decode(hash, PLACEHOLDER_WIDTH, PLACEHOLDER_HEIGHT) ?: return null
    return Bitmap.createBitmap(pixels, PLACEHOLDER_WIDTH, PLACEHOLDER_HEIGHT, Bitmap.Config.ARGB_8888).asImageBitmap()
}

/** 16:9, small: the renderer scales it up and the blur is the point. */
private const val PLACEHOLDER_WIDTH = 32
private const val PLACEHOLDER_HEIGHT = 18
