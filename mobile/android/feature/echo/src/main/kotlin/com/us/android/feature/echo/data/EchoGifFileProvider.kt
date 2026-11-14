package com.us.android.feature.echo.data

import android.content.Context
import androidx.core.content.FileProvider
import com.us.android.feature.echo.R

/**
 * Shares only cacheDir/echo_gifs (res/xml/echo_gif_paths.xml): the GIFs the
 * composer downloads from Giphy before uploading them as the note's one
 * `image/gif`. A subclass so its manifest key is unique; the authority is
 * `<applicationId>.echo.gifs`.
 */
class EchoGifFileProvider : FileProvider(R.xml.echo_gif_paths) {
    companion object {
        const val DIR = "echo_gifs"

        fun authority(context: Context): String = "${context.packageName}.echo.gifs"
    }
}
