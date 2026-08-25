package com.us.android.core.media.creator

import android.content.Context
import android.graphics.Typeface
import java.io.File
import java.security.MessageDigest

/**
 * The pinned launch font set — the only faces authored pixels may use.
 *
 * ## WHY BUNDLED, PINNED AND HASH-VERIFIED
 *
 * A text layer's pixels are part of the user's published work. Rendering it
 * with whatever the system falls back to means the exported image differs
 * between devices and OS versions — Devanagari shaped by one vendor's fallback,
 * Tamil by another's, or by nothing at all. So the faces ship in the APK, are
 * referenced by id + version + hash in the project document, and are verified
 * against these hashes before first use. A hash mismatch is a corrupted or
 * substituted asset and the renderer refuses it rather than drawing the wrong
 * glyphs into someone's post.
 *
 * All three faces are licensed under the SIL Open Font License 1.1:
 *  - Noto Sans Devanagari 2.004 — (c) The Noto Project Authors
 *  - Noto Sans Tamil 2.004 — (c) The Noto Project Authors
 *  - Inter 4.0 (variable) — (c) The Inter Project Authors
 *
 * The SHA-256 values below are of the exact vendored binaries, computed at
 * vendoring time. They are NOT the fixture hashes — the contract fixtures use
 * deterministic test hashes and say so.
 */
object CreatorFonts {

    data class BundledFont(
        val fontAssetId: String,
        val assetPath: String,
        val version: String,
        val sha256: String,
        val license: String,
    )

    val ALL = listOf(
        BundledFont(
            fontAssetId = "noto-sans-devanagari",
            assetPath = "creator/fonts/noto-sans-devanagari.ttf",
            version = "2.004",
            sha256 = "306b53ecfb182a504dd8a7446093c316387d2fd8dc350d0792ed1753fe0996cd",
            license = "OFL-1.1",
        ),
        BundledFont(
            fontAssetId = "noto-sans-tamil",
            assetPath = "creator/fonts/noto-sans-tamil.ttf",
            version = "2.004",
            sha256 = "3c0a186feb3c63c7f6d63e1511dcdc144e745ae09b98e217c83f3e317974f6f9",
            license = "OFL-1.1",
        ),
        BundledFont(
            fontAssetId = "inter",
            assetPath = "creator/fonts/inter.ttf",
            version = "4.000",
            sha256 = "29160a80ff49ddcab2c97711247e08b1fab27a484a329ce8b813d820dc559031",
            license = "OFL-1.1",
        ),
    )

    private val byId = ALL.associateBy { it.fontAssetId }
    private val cache = mutableMapOf<String, Typeface>()

    /**
     * Load a face by its contract id, verifying its bytes first.
     *
     * Null means "refuse to render": unknown id, or bytes that do not match the
     * pinned hash. The caller surfaces that as a render failure — never a
     * fallback face, because a fallback silently changes the artwork.
     */
    @Synchronized
    fun typeface(context: Context, fontAssetId: String): Typeface? {
        cache[fontAssetId]?.let { return it }
        val font = byId[fontAssetId] ?: return null

        val bytes = runCatching {
            context.assets.open(font.assetPath).use { it.readBytes() }
        }.getOrNull() ?: return null

        val actual = MessageDigest.getInstance("SHA-256").digest(bytes)
            .joinToString("") { "%02x".format(it) }
        if (actual != font.sha256) return null

        // Typeface has no from-bytes API below newer SDKs; a verified temp copy
        // in the app's cache dir bridges that without trusting the extracted
        // file — the hash was checked on the exact bytes written.
        val loaded = runCatching {
            val file = File(context.cacheDir, "font-${font.fontAssetId}.ttf")
            file.writeBytes(bytes)
            Typeface.createFromFile(file)
        }.getOrNull()

        return loaded?.also { cache[fontAssetId] = it }
    }
}
