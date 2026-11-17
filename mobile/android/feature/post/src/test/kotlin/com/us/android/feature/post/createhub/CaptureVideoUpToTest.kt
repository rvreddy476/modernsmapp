package com.us.android.feature.post.createhub

import android.net.Uri
import android.provider.MediaStore
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * reels-36 (was A-4): the system camera — Record without a Banuba licence —
 * is asked to stop at ten minutes, the camera cap (founder, 2026-10-21).
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CaptureVideoUpToTest {

    @Test
    fun `the capture intent carries a 600 second duration limit`() {
        val target = Uri.parse("content://com.us.android.fileprovider/create_capture/clip.mp4")

        val intent = CaptureVideoUpTo().createIntent(ApplicationProvider.getApplicationContext(), target)

        assertThat(intent.action).isEqualTo(MediaStore.ACTION_VIDEO_CAPTURE)
        assertThat(intent.getIntExtra(MediaStore.EXTRA_DURATION_LIMIT, -1)).isEqualTo(600)
        // The output target the plain contract sets is kept.
        assertThat(intent.getParcelableExtra<Uri>(MediaStore.EXTRA_OUTPUT)).isEqualTo(target)
    }

    @Test
    fun `the cap in seconds is the reel cap`() {
        assertThat(CaptureVideoUpTo.CAMERA_CLIP_MAX_SECONDS).isEqualTo(600)
    }
}
