package com.us.android.core.media.creator

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.ColorMatrix
import android.graphics.ColorMatrixColorFilter
import android.graphics.Paint
import android.graphics.Rect
import android.graphics.RectF
import android.text.Layout
import android.text.StaticLayout
import android.text.TextPaint
import com.us.android.core.creator.model.AdjustmentsMath
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.creator.model.RenderExporter
import com.us.android.core.creator.model.RenderResult
import com.us.android.core.creator.model.TextLayer
import dagger.hilt.android.qualifiers.ApplicationContext
import java.io.ByteArrayOutputStream
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The production [RenderExporter] — Canvas/Bitmap rendering of a project page.
 *
 * ## WHERE THIS SITS IN THE DAG
 *
 * This class implements the port DECLARED in `:core:creator-model`. It depends
 * on the model only; `:core:creator-engine` calls the port and never sees this
 * type — app DI binds the two (guards G-4/G-5). That is what lets the engine
 * stay pure enough to unit-test while the pixels are drawn here, next to the
 * rest of the media machinery.
 *
 * ## DETERMINISM WITHIN A DEVICE, HONESTY ACROSS THEM
 *
 * Everything the renderer consumes is integers from the frozen document —
 * micros-based crops, transforms and adjustments — and the fonts are pinned by
 * hash. On ONE device the same document renders the same bytes. Across devices,
 * bitmap decoding and JPEG encoding are vendor code and may differ by a bit;
 * that is why the acceptance criterion compares goldens within a declared pixel
 * tolerance on physical devices, not by hash equality across hardware.
 *
 * ## FAILURE IS A VALUE
 *
 * Every failure path returns [RenderResult.Failure] with a stated reason. An
 * export that throws halfway through a ten-page carousel is how partial state
 * gets committed; an export that returns a failure lets the publisher stop
 * cleanly with the project intact.
 */
@Singleton
class AndroidRenderExporter @Inject constructor(
    @ApplicationContext private val context: Context,
) : RenderExporter {

    override suspend fun renderPage(
        project: AndroidCreatorProject,
        pageId: String,
        sourceBytes: Map<String, ByteArray>,
    ): RenderResult {
        val canvasSpec = project.canvas
            ?: return RenderResult.Failure("project has no canvas", recoverable = false)
        val page = project.pages.firstOrNull { it.pageId == pageId }
            ?: return RenderResult.Failure("page $pageId does not exist", recoverable = false)

        val output = Bitmap.createBitmap(
            canvasSpec.widthPx,
            canvasSpec.heightPx,
            Bitmap.Config.ARGB_8888,
        )
        val canvas = Canvas(output)
        canvas.drawColor(Color.BLACK)

        // Layers paint bottom-up in document order — V-5 makes z == index, so
        // the array IS the paint order.
        for (layer in page.layers) {
            val failure = when (layer) {
                is ImageLayer -> drawImage(canvas, canvasSpec.widthPx, canvasSpec.heightPx, layer, sourceBytes)
                is TextLayer -> drawText(canvas, canvasSpec.widthPx, canvasSpec.heightPx, layer)
            }
            if (failure != null) {
                output.recycle()
                return failure
            }
        }

        val encoded = ByteArrayOutputStream().use { stream ->
            val ok = output.compress(Bitmap.CompressFormat.JPEG, EXPORT_JPEG_QUALITY, stream)
            output.recycle()
            if (!ok) return RenderResult.Failure("JPEG encoding failed", recoverable = true)
            stream.toByteArray()
        }

        return RenderResult.Success(
            bytes = encoded,
            widthPx = canvasSpec.widthPx,
            heightPx = canvasSpec.heightPx,
            mime = "image/jpeg",
        )
    }

    override suspend fun buildProxy(sourceBytes: ByteArray, maxEdgePx: Int): RenderResult {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(sourceBytes, 0, sourceBytes.size, bounds)
        if (bounds.outWidth <= 0 || bounds.outHeight <= 0) {
            return RenderResult.Failure("source is not a decodable image", recoverable = false)
        }

        // Power-of-two sampling keeps peak memory proportional to the PROXY,
        // not the original — the difference between editing a 48 MP photo on a
        // 4 GB phone and OOMing on it.
        var sample = 1
        while (bounds.outWidth / (sample * 2) >= maxEdgePx ||
            bounds.outHeight / (sample * 2) >= maxEdgePx
        ) {
            sample *= 2
        }

        val options = BitmapFactory.Options().apply { inSampleSize = sample }
        val decoded = BitmapFactory.decodeByteArray(sourceBytes, 0, sourceBytes.size, options)
            ?: return RenderResult.Failure("source failed to decode", recoverable = false)

        val encoded = ByteArrayOutputStream().use { stream ->
            val ok = decoded.compress(Bitmap.CompressFormat.JPEG, PROXY_JPEG_QUALITY, stream)
            val width = decoded.width
            val height = decoded.height
            decoded.recycle()
            if (!ok) return RenderResult.Failure("proxy encoding failed", recoverable = true)
            Triple(stream.toByteArray(), width, height)
        }

        return RenderResult.Success(
            bytes = encoded.first,
            widthPx = encoded.second,
            heightPx = encoded.third,
            mime = "image/jpeg",
        )
    }

    // ------------------------------------------------------------------

    /** Null on success; a typed failure otherwise. */
    private fun drawImage(
        canvas: Canvas,
        canvasWidth: Int,
        canvasHeight: Int,
        layer: ImageLayer,
        sourceBytes: Map<String, ByteArray>,
    ): RenderResult.Failure? {
        val bytes = sourceBytes[layer.assetRef]
            ?: return RenderResult.Failure(
                "no bytes supplied for asset ${layer.assetRef}",
                recoverable = true,
            )
        val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
            ?: return RenderResult.Failure(
                "asset ${layer.assetRef} failed to decode",
                recoverable = false,
            )

        // The crop is normalized micros over the SOURCE image.
        val crop = layer.crop
        val src = Rect(
            (bitmap.width.toLong() * crop.xMicros / MICROS).toInt(),
            (bitmap.height.toLong() * crop.yMicros / MICROS).toInt(),
            (bitmap.width.toLong() * (crop.xMicros + crop.wMicros) / MICROS).toInt(),
            (bitmap.height.toLong() * (crop.yMicros + crop.hMicros) / MICROS).toInt(),
        )

        val paint = Paint(Paint.FILTER_BITMAP_FLAG or Paint.ANTI_ALIAS_FLAG).apply {
            colorFilter = adjustmentsFilter(layer)
        }

        // P0-A scope: the base image fills the canvas (centre-crop by the
        // author's crop rect) with optional quarter-turn rotation. Pan/zoom
        // beyond the crop is P0-B.
        val rotation = layer.transform.rotationDegMicros / MICROS_F
        canvas.save()
        if (rotation != 0f) {
            canvas.rotate(rotation, canvasWidth / 2f, canvasHeight / 2f)
        }
        canvas.drawBitmap(
            bitmap,
            src,
            RectF(0f, 0f, canvasWidth.toFloat(), canvasHeight.toFloat()),
            paint,
        )
        canvas.restore()
        bitmap.recycle()
        return null
    }

    /**
     * All four look channels as one color matrix.
     *
     * The numbers come from [AdjustmentsMath] — the ONE definition of the
     * adjustment semantics, shared with the editor preview — which is what
     * keeps "what you saw" and "what you posted" the same picture.
     */
    private fun adjustmentsFilter(layer: ImageLayer): ColorMatrixColorFilter =
        ColorMatrixColorFilter(ColorMatrix(AdjustmentsMath.matrix(layer.adjustments)))

    private fun drawText(
        canvas: Canvas,
        canvasWidth: Int,
        canvasHeight: Int,
        layer: TextLayer,
    ): RenderResult.Failure? {
        // A missing or corrupt pinned font REFUSES the render. Falling back to
        // a system face would silently change the authored pixels, which is the
        // one thing an exporter must never do.
        val typeface = CreatorFonts.typeface(context, layer.style.fontAssetId)
            ?: return RenderResult.Failure(
                "font ${layer.style.fontAssetId} is unavailable or failed verification",
                recoverable = false,
            )

        val paint = TextPaint(Paint.ANTI_ALIAS_FLAG).apply {
            this.typeface = typeface
            textSize = canvasHeight.toFloat() * layer.style.sizeCanvasMicros / MICROS_F
            color = Color.parseColor(layer.style.colorArgb)
        }

        val alignment = when (layer.style.align) {
            "left" -> Layout.Alignment.ALIGN_NORMAL
            "right" -> Layout.Alignment.ALIGN_OPPOSITE
            else -> Layout.Alignment.ALIGN_CENTER
        }
        val layout = StaticLayout.Builder
            .obtain(layer.text.value, 0, layer.text.value.length, paint, canvasWidth)
            .setAlignment(alignment)
            .build()

        // The transform anchor is the layer CENTRE, in canvas micros.
        val centerX = canvasWidth.toFloat() * layer.transform.xMicros / MICROS_F
        val centerY = canvasHeight.toFloat() * layer.transform.yMicros / MICROS_F

        canvas.save()
        canvas.rotate(
            layer.transform.rotationDegMicros / MICROS_F,
            centerX,
            centerY,
        )
        canvas.translate(centerX - canvasWidth / 2f, centerY - layout.height / 2f)
        layout.draw(canvas)
        canvas.restore()
        return null
    }

    private companion object {
        const val MICROS = 1_000_000L
        const val MICROS_F = 1_000_000f
        const val EXPORT_JPEG_QUALITY = 92
        const val PROXY_JPEG_QUALITY = 85
    }
}
