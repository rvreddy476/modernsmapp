package com.us.android.feature.doorsteppro.camera

import android.content.Context
import android.net.Uri
import androidx.core.content.FileProvider
import com.us.android.feature.doorsteppro.R
import java.io.File

/** A captured photo: the file (for its EXIF orientation and preview) and its content:// URI (for the upload). */
data class SelfieShot(val path: String, val uri: String)

/**
 * Shares only cacheDir/selfies and cacheDir/job_photos
 * (res/xml/doorstep_pro_photo_paths.xml). A subclass so its manifest key is
 * unique; the authority is `<applicationId>.doorsteppro.photos`.
 */
class ProPhotoFileProvider : FileProvider(R.xml.doorstep_pro_photo_paths) {
    companion object {
        fun authority(context: Context): String = "${context.packageName}.doorsteppro.photos"
    }
}

/**
 * A fresh file for the system camera to write a job photo into, and the
 * content:// URI to hand it (ActivityResultContracts.TakePicture). Photos of
 * the work area and the kit only — the screen says so before the camera opens.
 * Older captures are cleared: each one is uploaded straight away.
 */
object JobPhotoFiles {
    private const val DIR = "job_photos"
    private const val KEEP = 8

    fun newTarget(context: Context): Pair<File, Uri> {
        val directory = File(context.cacheDir, DIR).apply { mkdirs() }
        directory.listFiles()?.sortedByDescending { it.lastModified() }?.drop(KEEP)?.forEach { it.delete() }
        val file = File(directory, "job-${System.currentTimeMillis()}.jpg")
        return file to FileProvider.getUriForFile(context, ProPhotoFileProvider.authority(context), file)
    }
}
