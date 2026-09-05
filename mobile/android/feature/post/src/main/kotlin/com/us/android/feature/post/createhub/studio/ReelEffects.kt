package com.us.android.feature.post.createhub.studio

import android.graphics.Bitmap
import androidx.media3.common.C
import androidx.media3.common.Effect
import androidx.media3.common.audio.SpeedProvider
import androidx.media3.common.util.UnstableApi
import androidx.media3.effect.BitmapOverlay
import androidx.media3.effect.Crop
import androidx.media3.effect.GaussianBlurWithFrameOverlaid
import androidx.media3.effect.OverlayEffect
import androidx.media3.effect.Presentation
import androidx.media3.effect.RgbMatrix
import androidx.media3.effect.StaticOverlaySettings

/**
 * The Media3 effects a [ReelEdit] means (2026-09-05). ONE list, built the
 * same way for the preview player and for the export, so the studio shows
 * what it will write:
 *
 *  1. the frame — Fill is a [Crop] to [ReelFrame.cropWindow]; Fit is
 *     [GaussianBlurWithFrameOverlaid] (the frame, scaled by
 *     [ReelFrame.fitScale], sharp over a blurred copy of itself) and a
 *     centre crop to 9:16 — then a [Presentation] to the export size;
 *  2. the look, as an [RgbMatrix] over [ReelLook.glMatrix];
 *  3. the text pill, as a [BitmapOverlay] anchored where the user dragged it.
 *
 * Speed is not an effect here: the player takes it as its playback speed
 * and the Transformer as the item's [SpeedProvider] ([speedProvider]),
 * which slows or hurries the audio with the video.
 */
@UnstableApi
object ReelEffects {

    /** Every effect for [edit], in order. [pill] draws the text pill when there is one. */
    fun build(edit: ReelEdit, pill: (TextPill) -> Bitmap?): List<Effect> = buildList {
        addAll(frame(edit))
        if (edit.look != ReelLook.NONE) add(LookEffect(edit.look))
        edit.text?.let { text -> pill(text)?.let { bitmap -> add(overlay(bitmap, text)) } }
    }

    private fun frame(edit: ReelEdit): List<Effect> {
        val size = ReelFrame.outputSize(edit.width, edit.height)
        return when (edit.mode) {
            FrameMode.FILL -> {
                val window = ReelFrame.cropWindow(edit.width, edit.height, edit.pan)
                listOf(
                    Crop(window.left, window.right, window.bottom, window.top),
                    Presentation.createForWidthAndHeight(size.width, size.height, Presentation.LAYOUT_SCALE_TO_FIT),
                )
            }
            FrameMode.FIT -> {
                val scale = ReelFrame.fitScale(edit.width, edit.height)
                listOf(
                    GaussianBlurWithFrameOverlaid(BLUR_SIGMA, scale, scale),
                    Presentation.createForAspectRatio(ReelFrame.ASPECT, Presentation.LAYOUT_SCALE_TO_FIT_WITH_CROP),
                    Presentation.createForWidthAndHeight(size.width, size.height, Presentation.LAYOUT_SCALE_TO_FIT),
                )
            }
        }
    }

    /**
     * The pill over the frame. Media3 anchors overlays in NDC: the
     * background anchor is the point on the FRAME the overlay's own anchor
     * lands on, so the pill's centre goes to the user's (x, y) — mapped
     * from top-left fractions to -1..1 with y up.
     */
    private fun overlay(bitmap: Bitmap, text: TextPill): Effect {
        val settings = StaticOverlaySettings.Builder()
            .setBackgroundFrameAnchor(text.x * 2f - 1f, 1f - text.y * 2f)
            .setOverlayFrameAnchor(0f, 0f)
            .build()
        return OverlayEffect(listOf(BitmapOverlay.createStaticBitmapOverlay(bitmap, settings)))
    }

    /** A constant speed for the whole item — what the Speed chips choose. */
    fun speedProvider(speed: ReelSpeed): SpeedProvider = ConstantSpeed(speed.factor)

    private class ConstantSpeed(private val factor: Float) : SpeedProvider {
        override fun getSpeed(timeUs: Long): Float = factor
        override fun getNextSpeedChangeTimeUs(timeUs: Long): Long = C.TIME_UNSET
    }

    /** The look's matrix as Media3 wants it. `isNoOp` keeps None free. */
    private class LookEffect(private val look: ReelLook) : RgbMatrix {
        private val matrix = look.glMatrix()
        override fun getMatrix(presentationTimeUs: Long, useHdr: Boolean): FloatArray = matrix
        override fun isNoOp(inputWidth: Int, inputHeight: Int): Boolean = look == ReelLook.NONE
    }

    /** The background's softness, in source pixels. */
    private const val BLUR_SIGMA = 40f
}
