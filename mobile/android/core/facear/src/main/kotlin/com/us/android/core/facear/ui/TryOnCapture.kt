package com.us.android.core.facear.ui

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Typeface
import androidx.core.content.FileProvider
import java.io.File

/**
 * What happens to a captured try-on photo.
 *
 * ## WHAT THE CAPTURE CONTROL IS FOR — the decision, and why
 *
 * It produces **a shareable picture of the shopper wearing this shade, with
 * the product and the shade written on it**, and hands it to the system share
 * sheet. That is the whole purpose, the control is labelled and iconed for it
 * ("Share this look", the share glyph), and nothing else is offered.
 *
 * Two things were considered and rejected:
 *
 *  * **A bare "Capture" button.** The founder asked what it was for, which is
 *    the answer: a shutter that produces a file the shopper cannot find is a
 *    control with no purpose on screen. Either it shares or it should not be
 *    there.
 *  * **Saving to the gallery.** Below Android 10 there is no scoped-storage
 *    insert, so it needs `WRITE_EXTERNAL_STORAGE` — a permission this app does
 *    not declare and which is not worth adding to the whole product for one
 *    button. A control that works on some of the device matrix and silently
 *    does not on the rest is worse than one control that works everywhere, and
 *    **every gallery app accepts a share**. So the gallery path is gone rather
 *    than conditionally present.
 *
 * The caption matters as much as the picture: a screenshot of a face is not a
 * product recommendation, and a friend who is sent one has to be able to see
 * what it is. [captureCaption] builds that line and is pure, so the one part
 * of this file that has a decision in it is tested.
 *
 * The still comes back from the SDK as a `Bitmap` in memory and the share
 * sheet cannot take a bitmap, so it is stamped, written once into the app's
 * cache, and shared from that one file.
 */

/** Where captures land. Served by the app's FileProvider — see `create_capture_paths.xml`. */
const val TRY_ON_CAPTURE_DIR = "try_on"

/**
 * The line written across the bottom of a shared capture.
 *
 * The product always; the shade when there is one. Joined with an en dash and
 * not a comma because a product title may itself contain commas, and the
 * result is read at thumbnail size in a chat.
 *
 * Blank inputs are dropped rather than rendered as empty segments: a server
 * that sends a shade with no label must not produce "Lipstick — ".
 */
fun captureCaption(productTitle: String, shadeLabel: String?): String =
    listOfNotNull(
        productTitle.trim().takeIf { it.isNotEmpty() },
        shadeLabel?.trim()?.takeIf { it.isNotEmpty() },
    ).joinToString(" — ")

/**
 * [bitmap] with [caption] burned into the bottom, as a new bitmap.
 *
 * A new bitmap and not a mutation: the one the SDK handed over may be
 * immutable and may still be referenced by the player. A blank caption returns
 * the original untouched — stamping an empty plate would just darken the
 * picture.
 *
 * Sizes are proportional to the image, so a 1080-wide phone and a 2160-wide
 * one produce the same-looking caption rather than one with unreadable text.
 */
fun stampCapture(bitmap: Bitmap, caption: String): Bitmap {
    if (caption.isBlank()) return bitmap
    val stamped = bitmap.copy(Bitmap.Config.ARGB_8888, true) ?: return bitmap
    val canvas = Canvas(stamped)
    val textSize = stamped.width * CAPTION_SIZE_RATIO
    val margin = stamped.width * CAPTION_MARGIN_RATIO

    val plate = Paint().apply {
        color = Color.argb(PLATE_ALPHA, PLATE_R, PLATE_G, PLATE_B)
        isAntiAlias = true
    }
    val text = Paint().apply {
        color = Color.WHITE
        isAntiAlias = true
        this.textSize = textSize
        typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
    }

    val plateTop = stamped.height - (textSize + margin * 2)
    canvas.drawRect(0f, plateTop, stamped.width.toFloat(), stamped.height.toFloat(), plate)
    canvas.drawText(caption, margin, stamped.height - margin, text)
    return stamped
}

/**
 * Writes [bitmap] into `cacheDir/try_on/` as a JPEG and returns the file, or
 * null when it could not be written.
 *
 * Cache and not files: a try-on photo is a scratch file the moment the share
 * sheet has copied what it needs, and the OS should be free to reclaim it.
 *
 * Call off the main thread — this encodes a full-resolution bitmap.
 */
fun writeCapture(context: Context, bitmap: Bitmap): File? {
    val dir = File(context.cacheDir, TRY_ON_CAPTURE_DIR).apply { mkdirs() }
    val target = File(dir, "tryon_${System.currentTimeMillis()}.jpg")
    return runCatching {
        target.outputStream().use { out ->
            bitmap.compress(Bitmap.CompressFormat.JPEG, JPEG_QUALITY, out)
        }
        target.takeIf { it.length() > 0 }
    }.getOrElse {
        target.delete()
        null
    }
}

/**
 * Hands [file] to the system share sheet as an image.
 *
 * `createChooser`, not the bare intent, for the reason `:core:ui`'s
 * `rememberPostSharer` gives: without it Android can silently reuse a default
 * chosen once, months ago, and "share" stops being a choice.
 *
 * The URI is a FileProvider one with a read grant. A `file://` URI would throw
 * `FileUriExposedException` on every supported version of Android.
 */
fun shareCapture(context: Context, file: File, subject: String? = null) {
    val uri = FileProvider.getUriForFile(context, "${context.packageName}.fileprovider", file)
    val send = Intent(Intent.ACTION_SEND).apply {
        type = JPEG_MIME
        putExtra(Intent.EXTRA_STREAM, uri)
        // The caption again, as text: a chat app that shows it beside the
        // image gets the product name without the recipient having to read it
        // off a thumbnail.
        subject?.takeIf { it.isNotBlank() }?.let {
            putExtra(Intent.EXTRA_SUBJECT, it)
            putExtra(Intent.EXTRA_TEXT, it)
        }
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    context.startActivity(Intent.createChooser(send, null))
}

private const val JPEG_MIME = "image/jpeg"
private const val JPEG_QUALITY = 95
private const val CAPTION_SIZE_RATIO = 0.045f
private const val CAPTION_MARGIN_RATIO = 0.035f
private const val PLATE_ALPHA = 190
private const val PLATE_R = 4
private const val PLATE_G = 17
private const val PLATE_B = 34
