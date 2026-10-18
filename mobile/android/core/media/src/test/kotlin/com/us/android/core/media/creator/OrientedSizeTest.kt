package com.us.android.core.media.creator

import android.media.ExifInterface
import com.google.common.truth.Truth.assertThat
import org.junit.Test

class OrientedSizeTest {

    /** The camera case: landscape pixels tagged to be seen portrait. */
    @Test
    fun `a quarter turn swaps width and height`() {
        listOf(
            ExifInterface.ORIENTATION_ROTATE_90,
            ExifInterface.ORIENTATION_ROTATE_270,
            ExifInterface.ORIENTATION_TRANSPOSE,
            ExifInterface.ORIENTATION_TRANSVERSE,
        ).forEach { assertThat(orientedSize(4000, 3000, it)).isEqualTo(3000 to 4000) }
    }

    @Test
    fun `no tag, a half turn or a mirror keeps the size`() {
        listOf(
            ExifInterface.ORIENTATION_UNDEFINED,
            ExifInterface.ORIENTATION_NORMAL,
            ExifInterface.ORIENTATION_ROTATE_180,
            ExifInterface.ORIENTATION_FLIP_HORIZONTAL,
            ExifInterface.ORIENTATION_FLIP_VERTICAL,
        ).forEach { assertThat(orientedSize(4000, 3000, it)).isEqualTo(4000 to 3000) }
    }
}
