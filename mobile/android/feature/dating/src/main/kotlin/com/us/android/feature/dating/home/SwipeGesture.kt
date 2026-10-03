package com.us.android.feature.dating.home

import kotlin.math.abs

/** Where a deck card can be thrown. [UP] is Super Spark and may be switched off. */
enum class SwipeDirection { LEFT, RIGHT, UP }

/** What the card says while it is being dragged: which way it leans and how close it is to going. */
data class SwipeHint(val direction: SwipeDirection, val progress: Float)

/**
 * The deck gesture's arithmetic, kept free of Compose so it can be tested.
 *
 * A card goes when it is released far enough ([distanceFraction] of the card's
 * width, [upFraction] of its height), or when it is flicked ([flingVelocity],
 * in px/s) in the direction it already leans and has moved at least
 * [flingFraction]. A flick AGAINST the lean is a change of mind and never
 * commits. Whichever axis moved further decides which of the two it is.
 *
 * With [upEnabled] false an upward drag is nothing: no hint, no commit, and
 * the card springs back.
 */
class SwipeDecider(
    private val width: Float,
    private val height: Float,
    private val flingVelocity: Float,
    private val upEnabled: Boolean = false,
    private val distanceFraction: Float = DISTANCE_FRACTION,
    private val upFraction: Float = UP_FRACTION,
    private val flingFraction: Float = FLING_FRACTION,
) {

    /** The direction the card would take from this offset, or null when it leans nowhere. */
    private fun lean(offsetX: Float, offsetY: Float): SwipeDirection? = when {
        upEnabled && offsetY < 0f && -offsetY > abs(offsetX) -> SwipeDirection.UP
        offsetX > 0f -> SwipeDirection.RIGHT
        offsetX < 0f -> SwipeDirection.LEFT
        else -> null
    }

    /** The label to show mid-drag, with [SwipeHint.progress] reaching 1 at the release threshold. */
    fun hint(offsetX: Float, offsetY: Float): SwipeHint? {
        if (width <= 0f || height <= 0f) return null
        val direction = lean(offsetX, offsetY) ?: return null
        val progress = when (direction) {
            SwipeDirection.UP -> -offsetY / (height * upFraction)
            else -> abs(offsetX) / (width * distanceFraction)
        }
        return SwipeHint(direction, progress.coerceIn(0f, 1f))
    }

    /** The action a release commits to, or null when the card should return. */
    fun decide(offsetX: Float, offsetY: Float, velocityX: Float, velocityY: Float): SwipeDirection? {
        if (width <= 0f || height <= 0f) return null
        val direction = lean(offsetX, offsetY) ?: return null
        val committed = when (direction) {
            SwipeDirection.UP ->
                -offsetY >= height * upFraction ||
                    (velocityY <= -flingVelocity && -offsetY >= height * flingFraction)
            SwipeDirection.RIGHT ->
                (offsetX >= width * distanceFraction && velocityX > -flingVelocity) ||
                    (velocityX >= flingVelocity && offsetX >= width * flingFraction)
            SwipeDirection.LEFT ->
                (-offsetX >= width * distanceFraction && velocityX < flingVelocity) ||
                    (velocityX <= -flingVelocity && -offsetX >= width * flingFraction)
        }
        return direction.takeIf { committed }
    }

    /** The card's tilt in degrees: it leans with the drag, up to [MAX_ROTATION]. */
    fun rotation(offsetX: Float): Float =
        if (width <= 0f) 0f else (offsetX / width).coerceIn(-1f, 1f) * MAX_ROTATION

    companion object {
        const val DISTANCE_FRACTION = 0.3f
        const val UP_FRACTION = 0.22f
        const val FLING_FRACTION = 0.08f
        const val MAX_ROTATION = 9f
    }
}
