package com.us.android.core.creator.model

/**
 * The render/export PORT.
 *
 * ## WHY THIS INTERFACE LIVES IN :core:creator-model
 *
 * Plan v2 said two contradictory things: that `creator-engine` owns the
 * render/export interfaces, and that `core:media` consumes `creator-model`
 * interfaces. Following both would make engine and media depend on each other.
 *
 * Putting the port here removes the choice. `:core:media` implements it and
 * depends only on this module; `:core:creator-engine` calls it and also depends
 * only on this module. Neither can reach the other, and `app`'s DI is the one
 * place that knows both sides. Guards G-4 and G-5 assert exactly that.
 *
 * The port deals only in model types and byte arrays — no Bitmap, no Context,
 * no Uri — because the moment an Android type appears in this signature,
 * `:core:creator-model` stops being pure and the whole arrangement collapses.
 */
interface RenderExporter {

    /**
     * Render one page to encoded image bytes.
     *
     * @param project the document being exported, for canvas and font resolution.
     * @param pageId which page.
     * @param sourceBytes the vault-backed originals, keyed by `assetId`. The
     *   caller reads the vault; the renderer never touches the filesystem, which
     *   is what keeps it testable off-device.
     */
    suspend fun renderPage(
        project: AndroidCreatorProject,
        pageId: String,
        sourceBytes: Map<String, ByteArray>,
    ): RenderResult

    /** Build a downscaled proxy for editing, so the editor never decodes a full-size image. */
    suspend fun buildProxy(sourceBytes: ByteArray, maxEdgePx: Int): RenderResult
}

/**
 * A render outcome.
 *
 * Failure is a value, not an exception: an export that fails mid-carousel must
 * leave the project intact and resumable, and a thrown exception halfway through
 * a background job is how partial state gets committed.
 */
sealed interface RenderResult {
    data class Success(
        val bytes: ByteArray,
        val widthPx: Int,
        val heightPx: Int,
        val mime: String,
    ) : RenderResult {
        override fun equals(other: Any?): Boolean =
            other is Success && bytes.contentEquals(other.bytes) &&
                widthPx == other.widthPx && heightPx == other.heightPx && mime == other.mime

        override fun hashCode(): Int =
            (bytes.contentHashCode() * 31 + widthPx) * 31 + heightPx

        fun sha256(): String = Canonical.sha256Hex(bytes)
    }

    data class Failure(val reason: String, val recoverable: Boolean) : RenderResult
}
