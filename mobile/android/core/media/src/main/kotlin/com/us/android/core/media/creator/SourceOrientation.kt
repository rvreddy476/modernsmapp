package com.us.android.core.media.creator

import android.graphics.Bitmap
import android.graphics.Matrix
import android.media.ExifInterface
import java.io.ByteArrayInputStream
import java.io.File

/**
 * A photo's EXIF orientation, honoured wherever the creator reads pixels
 * (2026-10-19).
 *
 * A camera JPEG is usually stored landscape with an "rotate 90°" tag. The
 * studio's preview honoured the tag, but the import measured the raw pixels
 * and the renderer decoded them with `BitmapFactory`, which ignores it: the
 * crop was worked out on a sideways image and the post went up sideways. Every
 * read now goes through here so measure, preview and render agree.
 */
object SourceOrientation {

    /** The EXIF orientation of encoded image [bytes]; [ExifInterface.ORIENTATION_NORMAL] when absent or unreadable. */
    fun of(bytes: ByteArray): Int =
        runCatching { ExifInterface(ByteArrayInputStream(bytes)).orientation() }
            .getOrDefault(ExifInterface.ORIENTATION_NORMAL)

    /** The EXIF orientation of the image at [file]; normal when absent or unreadable. */
    fun of(file: File): Int =
        runCatching { ExifInterface(file.absolutePath).orientation() }
            .getOrDefault(ExifInterface.ORIENTATION_NORMAL)

    /** [bitmap] drawn the way the tag says it is seen; the same bitmap when no turn is needed. */
    fun upright(bitmap: Bitmap, orientation: Int): Bitmap {
        val matrix = matrixFor(orientation) ?: return bitmap
        val turned = Bitmap.createBitmap(bitmap, 0, 0, bitmap.width, bitmap.height, matrix, true)
        if (turned !== bitmap) bitmap.recycle()
        return turned
    }

    private fun ExifInterface.orientation(): Int =
        getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)

    private fun matrixFor(orientation: Int): Matrix? = when (orientation) {
        ExifInterface.ORIENTATION_FLIP_HORIZONTAL -> Matrix().apply { setScale(-1f, 1f) }
        ExifInterface.ORIENTATION_ROTATE_180 -> Matrix().apply { setRotate(180f) }
        ExifInterface.ORIENTATION_FLIP_VERTICAL -> Matrix().apply { setScale(1f, -1f) }
        ExifInterface.ORIENTATION_TRANSPOSE -> Matrix().apply { setRotate(90f); postScale(-1f, 1f) }
        ExifInterface.ORIENTATION_ROTATE_90 -> Matrix().apply { setRotate(90f) }
        ExifInterface.ORIENTATION_TRANSVERSE -> Matrix().apply { setRotate(-90f); postScale(-1f, 1f) }
        ExifInterface.ORIENTATION_ROTATE_270 -> Matrix().apply { setRotate(-90f) }
        else -> null
    }
}

/**
 * The size an image is SEEN at: width and height swap for the four
 * orientations that turn it a quarter. Pure, so the table is a test.
 */
fun orientedSize(widthPx: Int, heightPx: Int, orientation: Int): Pair<Int, Int> = when (orientation) {
    ExifInterface.ORIENTATION_TRANSPOSE,
    ExifInterface.ORIENTATION_ROTATE_90,
    ExifInterface.ORIENTATION_TRANSVERSE,
    ExifInterface.ORIENTATION_ROTATE_270,
    -> heightPx to widthPx
    else -> widthPx to heightPx
}
