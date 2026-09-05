package com.us.android.core.ui.photoeditor

import android.graphics.Bitmap
import android.net.Uri
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.io.File

/** Robolectric for a real ContentResolver over `file://` sources and a real Bitmap encoder. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ExportedImageCacheTest {

    private val context = RuntimeEnvironment.getApplication()

    private fun exported(name: String, format: Bitmap.CompressFormat): Uri {
        val file = File(context.cacheDir, name)
        val bitmap = Bitmap.createBitmap(4, 4, Bitmap.Config.ARGB_8888)
        file.outputStream().use { bitmap.compress(format, 90, it) }
        return Uri.fromFile(file)
    }

    @Test
    fun `a jpeg export is copied byte for byte into the edits directory`() {
        val source = exported("export.jpg", Bitmap.CompressFormat.JPEG)

        val path = copyExportToCache(context, source)

        assertThat(path).isNotNull()
        val copy = File(path!!)
        assertThat(copy.parentFile?.name).isEqualTo(PHOTO_EDITS_DIR)
        assertThat(copy.extension).isEqualTo("jpg")
        assertThat(copy.readBytes()).isEqualTo(File(source.path!!).readBytes())
    }

    @Test
    fun `a png export is re-encoded as jpeg`() {
        val source = exported("export.png", Bitmap.CompressFormat.PNG)

        val path = copyExportToCache(context, source)

        assertThat(path).isNotNull()
        val head = File(path!!).inputStream().use { input -> ByteArray(2).also { input.read(it) } }
        assertThat(head[0]).isEqualTo(0xFF.toByte())
        assertThat(head[1]).isEqualTo(0xD8.toByte())
    }

    @Test
    fun `an unreadable export is null and leaves nothing behind`() {
        val path = copyExportToCache(context, Uri.fromFile(File(context.cacheDir, "missing.jpg")))

        assertThat(path).isNull()
        val leftovers = File(context.cacheDir, PHOTO_EDITS_DIR).listFiles().orEmpty()
        assertThat(leftovers).isEmpty()
    }
}
