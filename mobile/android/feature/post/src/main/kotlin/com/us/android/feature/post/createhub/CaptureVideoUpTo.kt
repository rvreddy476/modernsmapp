package com.us.android.feature.post.createhub

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import androidx.activity.result.contract.ActivityResultContracts
import com.us.android.core.media.publish.REEL_MAX_DURATION_MS

/**
 * The system camera, asked to stop at the camera cap (founder, 2026-10-21:
 * at most ten minutes per clip).
 *
 * Without a Banuba licence Record opens the system camera, which had no
 * limit at all (reels-36, was A-4). `EXTRA_DURATION_LIMIT` is in SECONDS and
 * is a request, not a guarantee: some camera apps ignore it. A clip that
 * comes back longer anyway is routed exactly as a picked file of that length
 * — a reel goes through the studio, whose export is refused over the cap
 * (`ReelEdit.exceedsReelCap`, read from the file's real duration) until it is
 * trimmed; a long video has no cap.
 */
class CaptureVideoUpTo(
    private val limitSeconds: Int = CAMERA_CLIP_MAX_SECONDS,
) : ActivityResultContracts.CaptureVideo() {

    override fun createIntent(context: Context, input: Uri): Intent =
        withDurationLimit(super.createIntent(context, input), limitSeconds)

    companion object {
        /** Ten minutes, in the seconds the intent extra takes. */
        const val CAMERA_CLIP_MAX_SECONDS: Int = (REEL_MAX_DURATION_MS / MILLIS_PER_SECOND).toInt()

        /** Puts the camera cap on a video-capture intent. */
        fun withDurationLimit(intent: Intent, limitSeconds: Int = CAMERA_CLIP_MAX_SECONDS): Intent =
            intent.putExtra(MediaStore.EXTRA_DURATION_LIMIT, limitSeconds)
    }
}

private const val MILLIS_PER_SECOND = 1_000L
