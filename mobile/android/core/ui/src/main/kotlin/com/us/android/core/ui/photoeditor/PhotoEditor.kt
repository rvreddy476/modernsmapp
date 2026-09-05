package com.us.android.core.ui.photoeditor

import android.net.Uri
import androidx.activity.result.contract.ActivityResultContract
import kotlinx.coroutines.flow.StateFlow

/**
 * The PORT for an advanced (licensed) photo editor: a screen that takes one
 * image and hands back an edited one.
 *
 * Two features want the same editor step — the photo post flow and the
 * profile's avatar and cover pickers — and features never depend on each
 * other, so the contract lives here and the implementation (Banuba's Photo
 * Editor, behind the reel flow's licence gate) is bound in `:feature:post`
 * and reaches both through app DI. This module knows no SDK: it only knows
 * that an editor is [ready] or not, how to [ensure] it has been started, and
 * how to launch it for a result.
 */
interface PhotoEditor {
    /**
     * True only while the editor can be offered. Anything else — no licence,
     * an expired one, a failed start, an answer still pending — is false, and
     * a screen shows NO edit action rather than a disabled one.
     */
    val ready: StateFlow<Boolean>

    /** Starts the editor on first call, if the build carries a licence. Idempotent. Main thread. */
    fun ensure()

    /** The activity contract: the image to edit in, the outcome out. */
    fun contract(): ActivityResultContract<Uri, PhotoEditResult>
}

/** What came back from the editor, in the caller's terms. */
sealed interface PhotoEditResult {
    /** The editor exported an image; usually a `content://` the app must copy before it goes away. */
    data class Exported(val image: Uri) : PhotoEditResult

    /** The editor reported nothing usable; the message is for the person. */
    data class Failed(val message: String) : PhotoEditResult

    /** Backed out before exporting. Nothing to do. */
    data object Cancelled : PhotoEditResult
}
