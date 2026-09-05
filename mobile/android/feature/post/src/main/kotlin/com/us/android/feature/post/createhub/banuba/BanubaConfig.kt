package com.us.android.feature.post.createhub.banuba

/**
 * The Banuba Video Editor licence, as `:app` provides it from BuildConfig.
 *
 * Blank when the build had no `.secrets/banuba.token`: the reel flow then
 * never touches the SDK and stays on the Media3 studio. The token itself is
 * never logged — [toString] deliberately says only whether one exists.
 */
class BanubaConfig(val licenseToken: String) {
    val isLicensed: Boolean
        get() = licenseToken.isNotBlank()

    override fun toString(): String = "BanubaConfig(licensed=$isLicensed)"
}
