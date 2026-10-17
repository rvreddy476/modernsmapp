package com.us.android.feature.post.createhub.studio

import androidx.media3.common.util.UnstableApi
import androidx.media3.transformer.ExportException
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.io.FileNotFoundException

/**
 * A failed reel export (2026-10-17): the viewer still reads the same
 * friendly line per Media3 error code, and the log now says what Media3
 * said — the code's name and the cause — without a file path in it.
 */
@OptIn(UnstableApi::class)
class ReelExportFailureTest {

    @Test
    fun `each error code keeps its friendly message`() {
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED))
            .isEqualTo("This video's format can't be edited on this phone.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_DECODER_INIT_FAILED))
            .isEqualTo("This video's format can't be edited on this phone.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_ENCODER_INIT_FAILED))
            .isEqualTo("This phone can't encode the reel. Try a shorter clip.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_ENCODING_FORMAT_UNSUPPORTED))
            .isEqualTo("This phone can't encode the reel. Try a shorter clip.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_IO_FILE_NOT_FOUND))
            .isEqualTo("That video can't be read. Pick it again.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_IO_NO_PERMISSION))
            .isEqualTo("That video can't be read. Pick it again.")
        assertThat(exportFailureMessage(ExportException.ERROR_CODE_UNSPECIFIED))
            .isEqualTo("Couldn't prepare the reel. Try again.")
    }

    @Test
    fun `the log line names the code and the cause, with no path`() {
        val cause = FileNotFoundException(
            "/storage/emulated/0/DCIM/Camera/VID_20261017.mp4: open failed: ENOENT (No such file or directory)",
        )

        val line = exportFailureLog("ERROR_CODE_IO_FILE_NOT_FOUND", cause)

        assertThat(line).contains("ERROR_CODE_IO_FILE_NOT_FOUND")
        assertThat(line).contains("java.io.FileNotFoundException")
        assertThat(line).contains("open failed: ENOENT")
        assertThat(line).doesNotContain("/storage")
        assertThat(line).doesNotContain("VID_20261017")
    }

    @Test
    fun `uris are redacted too, and a failure without a cause still logs its code`() {
        assertThat(redactPaths("could not open content://media/external/video/media/42 for reading"))
            .isEqualTo("could not open <path> for reading")
        assertThat(exportFailureLog("ERROR_CODE_ENCODER_INIT_FAILED", null))
            .isEqualTo("export failed: ERROR_CODE_ENCODER_INIT_FAILED (no cause)")
    }
}
