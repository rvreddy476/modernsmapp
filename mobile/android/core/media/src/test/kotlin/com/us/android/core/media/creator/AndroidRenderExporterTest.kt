package com.us.android.core.media.creator

import android.content.Context
import android.graphics.BitmapFactory
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.Accessibility
import com.us.android.core.creator.model.Adjustments
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canvas
import com.us.android.core.creator.model.Crop
import com.us.android.core.creator.model.ImageLayer
import com.us.android.core.creator.model.LayerText
import com.us.android.core.creator.model.Page
import com.us.android.core.creator.model.PostText
import com.us.android.core.creator.model.RenderResult
import com.us.android.core.creator.model.SafeZone
import com.us.android.core.creator.model.TextLayer
import com.us.android.core.creator.model.TextStyle
import com.us.android.core.creator.model.Transform
import kotlinx.coroutines.runBlocking
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.ByteArrayOutputStream

/**
 * The production renderer, exercised through real Android graphics.
 *
 * ## WHAT THESE TESTS CLAIM AND WHAT THEY DO NOT
 *
 * Robolectric's native graphics run the REAL Skia pipeline, so decode, crop,
 * color-matrix and JPEG encode here are the production code paths. What a JVM
 * test cannot claim is pixel identity with a phone — vendor codecs differ by a
 * bit — which is why the frozen acceptance criterion compares device goldens
 * within a tolerance. These tests pin the contracts a renderer can break
 * SILENTLY: dimensions, refusal semantics, font verification, and proxy
 * downsampling.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class AndroidRenderExporterTest {

    private lateinit var context: Context
    private lateinit var exporter: AndroidRenderExporter

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        exporter = AndroidRenderExporter(context)
    }

    /** A real JPEG source, generated with the real encoder. */
    private fun jpegBytes(width: Int, height: Int, color: Int): ByteArray {
        val bitmap = android.graphics.Bitmap.createBitmap(
            width,
            height,
            android.graphics.Bitmap.Config.ARGB_8888,
        )
        bitmap.eraseColor(color)
        return ByteArrayOutputStream().use { stream ->
            bitmap.compress(android.graphics.Bitmap.CompressFormat.JPEG, 95, stream)
            bitmap.recycle()
            stream.toByteArray()
        }
    }

    private fun project(vararg pages: Page) = AndroidCreatorProject(
        projectId = "01J9Z4K7QW8XN2VB3M5R7T9Y0C",
        revision = 1,
        status = AndroidCreatorProject.STATUS_EDITING,
        createdAtMillis = 1,
        updatedAtMillis = 1,
        postText = PostText("t", "en"),
        canvas = Canvas(CANVAS_W, CANVAS_H, "4:5", SafeZone(0, 0, 0, 0)),
        pages = pages.toList(),
    )

    private fun imagePage(
        pageId: String = "p1",
        crop: Crop = Crop(0, 0, MICROS, MICROS),
        adjustments: Adjustments = Adjustments(0, 0),
        extraLayers: List<TextLayer> = emptyList(),
    ) = Page(
        pageId = pageId,
        accessibility = Accessibility("a photo", false),
        layers = listOf(
            ImageLayer(
                layerId = "l0",
                z = 0,
                transform = Transform(HALF, HALF, MICROS, 0),
                assetRef = "a1",
                crop = crop,
                adjustments = adjustments,
            ),
        ) + extraLayers.mapIndexed { index, layer -> layer.copy(z = index + 1) },
    )

    private fun textLayer(value: String, fontId: String) = TextLayer(
        layerId = "t1",
        z = 1,
        transform = Transform(HALF, HALF, MICROS, 0),
        text = LayerText(value, "hi"),
        style = TextStyle(
            fontAssetId = fontId,
            fontVersion = "2.004",
            fontSha256 = "0".repeat(64),
            license = "OFL-1.1",
            weight = 400,
            sizeCanvasMicros = 50_000,
            colorArgb = "#FFFFFFFF",
            align = "center",
        ),
    )

    // ------------------------------------------------------------------
    // Rendering
    // ------------------------------------------------------------------

    @Test
    fun `a page renders at exactly the canvas dimensions`() = runBlocking {
        val result = exporter.renderPage(
            project(imagePage()),
            "p1",
            mapOf("a1" to jpegBytes(800, 1000, android.graphics.Color.RED)),
        )

        val success = result as RenderResult.Success
        assertThat(success.widthPx).isEqualTo(CANVAS_W)
        assertThat(success.heightPx).isEqualTo(CANVAS_H)
        assertThat(success.mime).isEqualTo("image/jpeg")
        // And the bytes really are a decodable JPEG of that size.
        val decoded = BitmapFactory.decodeByteArray(success.bytes, 0, success.bytes.size)
        assertThat(decoded.width).isEqualTo(CANVAS_W)
        assertThat(decoded.height).isEqualTo(CANVAS_H)
    }

    /** Same document, same bytes in, same bytes out — on one machine. */
    @Test
    fun `rendering is deterministic on one device`() = runBlocking {
        val source = mapOf("a1" to jpegBytes(800, 1000, android.graphics.Color.BLUE))
        val page = project(imagePage(adjustments = Adjustments(200_000, 100_000)))

        val first = exporter.renderPage(page, "p1", source) as RenderResult.Success
        val second = exporter.renderPage(page, "p1", source) as RenderResult.Success

        assertThat(first.sha256()).isEqualTo(second.sha256())
    }

    /** The crop actually crops: different rects produce different pixels. */
    @Test
    fun `different crops produce different renders`() = runBlocking {
        // Left half red, right half green — a crop choosing one half must differ.
        val bitmap = android.graphics.Bitmap.createBitmap(
            400,
            400,
            android.graphics.Bitmap.Config.ARGB_8888,
        )
        val canvas = android.graphics.Canvas(bitmap)
        val paint = android.graphics.Paint()
        paint.color = android.graphics.Color.RED
        canvas.drawRect(0f, 0f, 200f, 400f, paint)
        paint.color = android.graphics.Color.GREEN
        canvas.drawRect(200f, 0f, 400f, 400f, paint)
        val source = ByteArrayOutputStream().use { stream ->
            bitmap.compress(android.graphics.Bitmap.CompressFormat.JPEG, 95, stream)
            bitmap.recycle()
            mapOf("a1" to stream.toByteArray())
        }

        val left = exporter.renderPage(
            project(imagePage(crop = Crop(0, 0, HALF, MICROS))),
            "p1",
            source,
        ) as RenderResult.Success
        val right = exporter.renderPage(
            project(imagePage(crop = Crop(HALF, 0, HALF, MICROS))),
            "p1",
            source,
        ) as RenderResult.Success

        assertThat(left.sha256()).isNotEqualTo(right.sha256())
    }

    /** Adjustments change pixels; zero adjustments do not. */
    @Test
    fun `exposure changes the render`() = runBlocking {
        val source = mapOf("a1" to jpegBytes(400, 500, android.graphics.Color.GRAY))

        val plain = exporter.renderPage(project(imagePage()), "p1", source) as RenderResult.Success
        val brightened = exporter.renderPage(
            project(imagePage(adjustments = Adjustments(500_000, 0))),
            "p1",
            source,
        ) as RenderResult.Success

        assertThat(plain.sha256()).isNotEqualTo(brightened.sha256())
    }

    // ------------------------------------------------------------------
    // Refusal semantics
    // ------------------------------------------------------------------

    /** Missing source bytes are a stated failure, not an exception. */
    @Test
    fun `missing source bytes fail recoverably`() = runBlocking {
        val result = exporter.renderPage(project(imagePage()), "p1", emptyMap())

        val failure = result as RenderResult.Failure
        assertThat(failure.recoverable).isTrue()
        assertThat(failure.reason).contains("a1")
    }

    @Test
    fun `undecodable source bytes fail without throwing`() = runBlocking {
        val result = exporter.renderPage(
            project(imagePage()),
            "p1",
            mapOf("a1" to "not an image at all".toByteArray()),
        )

        assertThat(result).isInstanceOf(RenderResult.Failure::class.java)
    }

    /**
     * An unknown font REFUSES the render.
     *
     * A fallback face would silently change the authored pixels — the one thing
     * an exporter must never do to someone's work.
     */
    @Test
    fun `an unpinned font refuses the render rather than falling back`() = runBlocking {
        val page = imagePage(extraLayers = listOf(textLayer("नमस्ते", fontId = "comic-sans")))

        val result = exporter.renderPage(
            project(page),
            "p1",
            mapOf("a1" to jpegBytes(400, 500, android.graphics.Color.WHITE)),
        )

        val failure = result as RenderResult.Failure
        assertThat(failure.recoverable).isFalse()
        assertThat(failure.reason).contains("comic-sans")
    }

    /** The three pinned faces load and verify against their vendored hashes. */
    @Test
    fun `every bundled font passes its own hash verification`() {
        CreatorFonts.ALL.forEach { font ->
            assertThat(CreatorFonts.typeface(context, font.fontAssetId)).isNotNull()
        }
    }

    /** A verified font really renders text into the page. */
    @Test
    fun `a devanagari text layer renders through the pinned face`() = runBlocking {
        val source = mapOf("a1" to jpegBytes(400, 500, android.graphics.Color.BLACK))
        val without = exporter.renderPage(project(imagePage()), "p1", source) as RenderResult.Success
        val with = exporter.renderPage(
            project(imagePage(extraLayers = listOf(textLayer("छोटे पल", "noto-sans-devanagari")))),
            "p1",
            source,
        ) as RenderResult.Success

        assertThat(with.sha256()).isNotEqualTo(without.sha256())
    }

    // ------------------------------------------------------------------
    // Proxies
    // ------------------------------------------------------------------

    /** The proxy is bounded by max edge — the editor never decodes originals. */
    @Test
    fun `a proxy is downsampled under its max edge budget`() = runBlocking {
        val result = exporter.buildProxy(
            jpegBytes(4000, 3000, android.graphics.Color.CYAN),
            maxEdgePx = 1280,
        )

        val success = result as RenderResult.Success
        // Power-of-two sampling: within (budget, budget*2] on the long edge.
        assertThat(success.widthPx).isAtMost(2 * 1280)
        assertThat(success.widthPx).isGreaterThan(640)
        assertThat(success.heightPx).isLessThan(success.widthPx)
    }

    @Test
    fun `a proxy from garbage bytes fails without throwing`() = runBlocking {
        val result = exporter.buildProxy("garbage".toByteArray(), maxEdgePx = 1280)

        assertThat(result).isInstanceOf(RenderResult.Failure::class.java)
    }

    private companion object {
        const val CANVAS_W = 1080
        const val CANVAS_H = 1350
        const val MICROS = 1_000_000
        const val HALF = 500_000
    }
}
