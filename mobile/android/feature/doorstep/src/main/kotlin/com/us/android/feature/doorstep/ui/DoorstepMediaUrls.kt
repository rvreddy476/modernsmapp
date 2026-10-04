package com.us.android.feature.doorstep.ui

/**
 * Where a Doorstep media id (a visit photo, an extra's evidence) is loaded
 * from: media-service's AUTHORIZED serve route on the gateway, fetched by the
 * app's authenticated image loader. media-service decides who may see it; a
 * refused id simply renders the placeholder.
 */
class DoorstepMediaUrls(private val baseUrl: String) {
    fun serve(mediaId: String): String = baseUrl.trimEnd('/') + "/v1/media/" + mediaId.trim() + "/serve"
}
