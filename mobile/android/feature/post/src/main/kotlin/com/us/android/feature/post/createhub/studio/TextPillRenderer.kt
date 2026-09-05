package com.us.android.feature.post.createhub.studio

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.RectF
import android.graphics.Typeface
import androidx.core.content.res.ResourcesCompat
import com.us.android.core.designsystem.R
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.math.roundToInt

/**
 * The text pill as pixels (2026-09-05): one line in Outfit, on a white or
 * navy capsule, sized for a frame [frameWidth] wide so it reads the same
 * on the preview and in the export. Drawn with Android's Canvas — no GL —
 * and handed to Media3 as a bitmap overlay.
 */
@Singleton
class TextPillRenderer @Inject constructor(
    @ApplicationContext private val context: Context,
) {
    private val outfit: Typeface? by lazy {
        runCatching { ResourcesCompat.getFont(context, R.font.outfit_variable) }.getOrNull()
    }

    /** Null for blank text — there is nothing to draw. */
    fun render(pill: TextPill, frameWidth: Int): Bitmap? {
        val text = pill.text.trim().ifBlank { return null }
        val textSize = frameWidth * TEXT_FRACTION
        val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            typeface = Typeface.create(outfit ?: Typeface.SANS_SERIF, Typeface.BOLD)
            this.textSize = textSize
            color = if (pill.style == TextPillStyle.WHITE) NAVY else Color.WHITE
        }
        val padX = textSize * PAD_X
        val padY = textSize * PAD_Y
        val maxTextWidth = frameWidth * MAX_WIDTH_FRACTION - padX * 2
        val fitted = fit(text, paint, maxTextWidth)
        val metrics = paint.fontMetrics
        val width = (paint.measureText(fitted) + padX * 2).roundToInt().coerceAtLeast(1)
        val height = (metrics.descent - metrics.ascent + padY * 2).roundToInt().coerceAtLeast(1)
        val bitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = if (pill.style == TextPillStyle.WHITE) Color.WHITE else NAVY
        }
        canvas.drawRoundRect(RectF(0f, 0f, width.toFloat(), height.toFloat()), height / 2f, height / 2f, fill)
        canvas.drawText(fitted, padX, padY - metrics.ascent, paint)
        return bitmap
    }

    /** The line, ellipsised to the width the frame allows. */
    private fun fit(text: String, paint: Paint, maxWidth: Float): String {
        if (paint.measureText(text) <= maxWidth) return text
        var end = text.length
        while (end > 1 && paint.measureText(text.substring(0, end) + ELLIPSIS) > maxWidth) end--
        return text.substring(0, end) + ELLIPSIS
    }

    private companion object {
        /**
         * Momentum's brand navy — `UsColorTokens.BrandNavy`, which is internal
         * to the design system; the preview pill reads it as
         * `UsTheme.extended.brandNavy`, and the export must paint the same ink.
         */
        const val NAVY = 0xFF0F3460.toInt()
        const val TEXT_FRACTION = 0.052f
        const val PAD_X = 0.9f
        const val PAD_Y = 0.42f
        const val MAX_WIDTH_FRACTION = 0.86f
        const val ELLIPSIS = "…"
    }
}
