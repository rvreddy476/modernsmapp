package com.us.android.feature.post.createhub.studio

import kotlin.math.min
import kotlin.math.roundToInt

/** How a source frame becomes 9:16 (the studio's Frame step, 2026-09-05). */
enum class FrameMode {
    /** Crop to the 9:16 window; the user pans the window along the free axis. */
    FILL,

    /** The whole frame, scaled to fit, over a blurred copy of itself. */
    FIT,
}

/**
 * The 9:16 frame math. Pure, so the crop window, the fit scale and the
 * output size are table tests; the Media3 effects built from these live in
 * [ReelEffects].
 *
 * A "frame" here is the source as it is DISPLAYED — rotation metadata
 * already applied, which is how both ExoPlayer and Transformer hand frames
 * to effects — so a phone video shot upright arrives taller than wide
 * whatever its container says.
 */
object ReelFrame {
    /** Width over height of a reel. */
    const val ASPECT = 9f / 16f

    /** The export's ceiling: 1080 × 1920. A smaller source is not upscaled. */
    const val MAX_WIDTH = 1080
    const val MAX_HEIGHT = 1920

    /**
     * The window's edges in OpenGL's normalized device coordinates, which is
     * what Media3's `Crop` takes: -1..1 on both axes, the origin in the
     * middle, y up.
     */
    data class Window(val left: Float, val right: Float, val bottom: Float, val top: Float)

    data class Size(val width: Int, val height: Int)

    /** Portrait sources default to Fill (founder); a landscape one would lose its sides, so it fits. */
    fun defaultMode(width: Int, height: Int): FrameMode = if (height >= width) FrameMode.FILL else FrameMode.FIT

    /** Whether the source is wider than 9:16 — the window then spans the full height and pans sideways. */
    fun isWiderThanReel(width: Int, height: Int): Boolean = width.toFloat() / height > ASPECT

    /**
     * How much of the source the 9:16 window covers along the axis it does
     * NOT fill: the fraction of the width for a wide source, of the height
     * for a tall one. 1 when the source is already 9:16.
     */
    fun windowFraction(width: Int, height: Int): Float {
        require(width > 0 && height > 0) { "A frame needs a size" }
        return if (isWiderThanReel(width, height)) {
            (height * ASPECT) / width
        } else {
            (width / ASPECT) / height
        }.coerceIn(0f, 1f)
    }

    /**
     * The crop window for Fill: the largest 9:16 rectangle inside the
     * source, slid along the free axis by [pan] — -1 is the left (or
     * bottom) edge, 0 the middle, 1 the right (or top) edge.
     */
    fun cropWindow(width: Int, height: Int, pan: Float): Window {
        val fraction = windowFraction(width, height)
        val slack = 1f - fraction
        val centre = pan.coerceIn(-1f, 1f) * slack
        return if (isWiderThanReel(width, height)) {
            Window(left = centre - fraction, right = centre + fraction, bottom = -1f, top = 1f)
        } else {
            Window(left = -1f, right = 1f, bottom = centre - fraction, top = centre + fraction)
        }
    }

    /**
     * The sharp frame's scale for Fit: the whole source, shrunk until it
     * sits inside the 9:16 window that a centred crop would take. That is
     * the same number as [windowFraction] — the window as a fraction of
     * the source is exactly the source as a fraction of the window.
     */
    fun fitScale(width: Int, height: Int): Float = windowFraction(width, height)

    /**
     * The export's size: the 9:16 window's own pixels, capped at
     * 1080 × 1920 and rounded down to even — H.264 encoders refuse odd
     * dimensions. Never larger than the source's window, so a 720p phone
     * clip is not inflated to 1080p.
     */
    fun outputSize(width: Int, height: Int): Size {
        val windowWidth: Float
        val windowHeight: Float
        if (isWiderThanReel(width, height)) {
            windowHeight = height.toFloat()
            windowWidth = height * ASPECT
        } else {
            windowWidth = width.toFloat()
            windowHeight = width / ASPECT
        }
        val scale = min(1f, min(MAX_WIDTH / windowWidth, MAX_HEIGHT / windowHeight))
        val outHeight = even((windowHeight * scale).roundToInt()).coerceAtLeast(2)
        val outWidth = even((outHeight * ASPECT).roundToInt()).coerceAtLeast(2)
        return Size(outWidth, outHeight)
    }

    /**
     * How far a drag of [dragPx] across a preview [previewPx] wide (or tall)
     * moves the pan: the whole slack of the source spans the preview, so a
     * finger travelling one preview-width swings the window edge to edge.
     * Dragging the picture right shows more of the left — the sign flips.
     */
    fun panAfterDrag(pan: Float, dragPx: Float, previewPx: Float, width: Int, height: Int): Float {
        if (previewPx <= 0f) return pan
        val slack = 1f - windowFraction(width, height)
        if (slack <= 0f) return 0f
        // One preview length of drag moves the window across its whole slack:
        // the window's centre travels 2·slack in NDC, i.e. pan by 2.
        val delta = dragPx / previewPx * 2f
        return (pan - delta).coerceIn(-1f, 1f)
    }

    private fun even(value: Int): Int = value - (value % 2)
}
