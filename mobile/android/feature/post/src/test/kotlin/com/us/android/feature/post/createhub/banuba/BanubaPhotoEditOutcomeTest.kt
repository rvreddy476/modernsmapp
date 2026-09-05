package com.us.android.feature.post.createhub.banuba

import android.app.Activity
import android.net.Uri
import com.us.android.core.ui.photoeditor.PhotoEditResult
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Robolectric for a real [Uri]. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class BanubaPhotoEditOutcomeTest {

    @Test
    fun `an ok result with an exported image is the export`() {
        val image = Uri.parse("content://com.us.android.dev.fileprovider/photo_edits/edit.jpg")

        assertEquals(PhotoEditResult.Exported(image), photoEditOutcomeOf(Activity.RESULT_OK, image))
    }

    @Test
    fun `an ok result with no image is the licence gap, in the person's words`() {
        val expected = PhotoEditResult.Failed("The licence does not include the Photo Editor")

        assertEquals(expected, photoEditOutcomeOf(Activity.RESULT_OK, null))
        assertEquals(expected, photoEditOutcomeOf(Activity.RESULT_OK, Uri.EMPTY))
    }

    @Test
    fun `a cancelled result is a cancel even if the intent carried a uri`() {
        val image = Uri.parse("file:///data/cache/photo_edits/edit.jpg")

        assertEquals(PhotoEditResult.Cancelled, photoEditOutcomeOf(Activity.RESULT_CANCELED, image))
        assertEquals(PhotoEditResult.Cancelled, photoEditOutcomeOf(Activity.RESULT_CANCELED, null))
    }
}
