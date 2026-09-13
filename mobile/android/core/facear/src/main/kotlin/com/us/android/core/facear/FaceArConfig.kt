package com.us.android.core.facear

/**
 * The Banuba licence, as `:app` provides it from BuildConfig.
 *
 * THE SAME TOKEN, THE SAME SECRET PATH.
 *
 * Face AR is a different Banuba product line from the Video Editor the reel
 * studio uses, but it is the same licence and the same file: repo-root
 * `.secrets/banuba.token` → `BuildConfig.BANUBA_LICENSE_TOKEN` → here. There
 * is deliberately no second secret path, no second Gradle property and no
 * second file to keep in step; a token that could disagree with itself is a
 * licence bug that only shows up on one of the two surfaces.
 *
 * Blank when the build had no token file: [isLicensed] is then false, the gate
 * never touches the SDK, and every try-on entry point stays hidden rather than
 * opening a camera that will not track a face. The token is never logged —
 * [toString] says only whether one exists.
 */
class FaceArConfig(val licenseToken: String) {
    val isLicensed: Boolean
        get() = licenseToken.isNotBlank()

    override fun toString(): String = "FaceArConfig(licensed=$isLicensed)"
}
