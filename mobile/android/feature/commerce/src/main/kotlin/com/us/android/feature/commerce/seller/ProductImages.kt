package com.us.android.feature.commerce.seller

/**
 * A product's gallery while the seller is building it.
 *
 * The rules below are pure functions rather than logic inside a ViewModel,
 * because the cap and the ordering are the things a seller notices when they
 * are wrong: nine images accepted and one silently dropped, or a reorder that
 * moves the wrong picture. Pure means they are a table test.
 */

/**
 * How many images one product may carry.
 *
 * Eight, the founder's number. It is enforced at the point of PICKING rather
 * than at upload, so a seller who selects twelve is told immediately instead
 * of watching four uploads finish and then vanish.
 */
const val MAX_PRODUCT_IMAGES = 8

/**
 * One image in the draft.
 *
 * [mediaId] is null until the upload has been confirmed. [uri] is the local
 * content URI it came from — kept even after upload, so the card can draw the
 * picture from the device rather than waiting for a delivery URL.
 *
 * [remoteUrl] is set for images the product ALREADY has, which have no local
 * URI at all; that is why the identity is [key] and not either field alone.
 */
data class ProductImageDraft(
    val uri: String = "",
    val mediaId: String? = null,
    val remoteUrl: String? = null,
    val uploaded: Long = 0,
    val total: Long = 0,
    val error: String? = null,
) {
    /** Stable identity for a list key: the media id once there is one, else the URI. */
    val key: String get() = mediaId ?: uri

    /** Whether this image can be sent to the server. */
    val ready: Boolean get() = mediaId != null

    /** 0..1 while bytes are moving, null when there is nothing to show. */
    val progress: Float? get() = if (total > 0 && !ready) uploaded.toFloat() / total else null
}

/**
 * Adds picked URIs, keeping the seller's order and stopping at the cap.
 *
 * Duplicates are dropped rather than added twice: picking the same photo
 * again is a mis-tap, not an instruction to upload it a second time.
 *
 * Returns the new list. Whether anything was REFUSED is [wouldExceedCap]'s
 * question, so the caller can say so rather than the list quietly being
 * shorter than the picker's selection.
 */
fun addImages(current: List<ProductImageDraft>, uris: List<String>): List<ProductImageDraft> {
    val known = current.map { it.uri }.toSet()
    val room = MAX_PRODUCT_IMAGES - current.size
    if (room <= 0) return current
    val fresh = uris.filter { it.isNotBlank() && it !in known }.distinct().take(room)
    return current + fresh.map { ProductImageDraft(uri = it) }
}

/** Whether a pick would have to be trimmed to fit [MAX_PRODUCT_IMAGES]. */
fun wouldExceedCap(current: List<ProductImageDraft>, picked: Int): Boolean =
    current.size + picked > MAX_PRODUCT_IMAGES

/**
 * Moves one image by [offset] positions.
 *
 * Out-of-range moves are no-ops rather than errors: the buttons at the ends of
 * the row are disabled, and a race that gets past them must not reorder
 * something else or crash.
 */
fun moveImage(current: List<ProductImageDraft>, key: String, offset: Int): List<ProductImageDraft> {
    val from = current.indexOfFirst { it.key == key }
    if (from < 0) return current
    val to = from + offset
    if (to !in current.indices) return current
    val mutable = current.toMutableList()
    mutable.add(to, mutable.removeAt(from))
    return mutable
}

fun removeImage(current: List<ProductImageDraft>, key: String): List<ProductImageDraft> =
    current.filterNot { it.key == key }

/**
 * The cover: the first image, always.
 *
 * There is no separate "is cover" flag, because two sources of truth for the
 * cover is how a gallery ends up showing one picture in the grid and another
 * on the product page. "Make cover" moves the image to the front.
 */
fun coverKey(current: List<ProductImageDraft>): String? = current.firstOrNull()?.key

/** The media ids to send, in gallery order. Anything still uploading is left out. */
fun readyMediaIds(current: List<ProductImageDraft>): List<String> =
    current.mapNotNull { it.mediaId }
