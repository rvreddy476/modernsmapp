package com.us.android.core.media

import com.us.android.core.network.ApiConfig
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Turns what the media endpoint returns into something a player can open.
 *
 * The 2026-08-17 capture settled two facts that make this a real component
 * rather than string concatenation:
 *
 *  1. `hls_url` is **gateway-relative** — `/v1/media/:id/hls/master.m3u8`. It
 *     is not a URL a player can open. It must be resolved against the API base
 *     URL, and the resulting request needs the bearer token, because the route
 *     is authorized.
 *  2. The `variants` map holds **absolute, short-lived signed URLs** pointing
 *     straight at the object store. Those must be used exactly as returned.
 *
 * Mixing the two up is the failure this class exists to prevent: prefixing a
 * signed variant with the gateway produces a 404, and opening the relative HLS
 * path directly produces a malformed URL.
 *
 * Storage keys elsewhere in the payload (`storage_key`, `hls_master_key`,
 * `thumbnail_url`) are deliberately not handled. The contract capture warns
 * explicitly against building URLs from them, and a resolver that accepted one
 * would make doing so easy.
 */
@Singleton
class MediaUrlResolver @Inject constructor(
    private val config: ApiConfig,
) {

    /**
     * Absolute URL for an HLS master playlist.
     *
     * Returns null rather than a guess when the server sent no `hls_url` — an
     * asset still processing has none, and inventing the conventional path
     * would produce a player error instead of a spinner.
     */
    fun hlsUrl(relativeOrAbsolute: String?): String? {
        val value = relativeOrAbsolute?.takeIf { it.isNotBlank() } ?: return null
        // Absolute already: a future deployment could return a CloudFront URL
        // here instead of a gateway path, and that must keep working without a
        // client release.
        if (value.startsWith("http://") || value.startsWith("https://")) return value
        return config.baseUrl.trimEnd('/') + "/" + value.trimStart('/')
    }

    /**
     * The best variant for a target width, or null when none fit.
     *
     * Variant keys are quality labels (`360p`, `720p`, `1080p`) plus
     * `original` and `thumb_150`. Only the ladder entries are candidates:
     * `original` can be arbitrarily large, and `thumb_150` is a still.
     *
     * Used for the poster frame and for progressive fallback, never as the
     * primary video source — that is HLS, so the ladder is chosen by ABR at
     * playback time rather than guessed here.
     */
    fun bestVariant(variants: Map<String, String>, maxHeight: Int): String? =
        variants.entries
            .mapNotNull { (key, url) -> key.heightOrNull()?.let { it to url } }
            .filter { (height, _) -> height <= maxHeight }
            .maxByOrNull { (height, _) -> height }
            ?.second

    /** The still frame, when one has been generated. */
    fun thumbnail(variants: Map<String, String>): String? = variants[THUMBNAIL_KEY]

    private fun String.heightOrNull(): Int? =
        takeIf { it.endsWith('p') }?.dropLast(1)?.toIntOrNull()

    private companion object {
        const val THUMBNAIL_KEY = "thumb_150"
    }
}
