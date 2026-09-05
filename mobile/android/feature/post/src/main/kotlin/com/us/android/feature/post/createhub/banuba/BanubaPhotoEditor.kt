package com.us.android.feature.post.createhub.banuba

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.result.contract.ActivityResultContract
import com.banuba.sdk.pe.PhotoCreationActivity
import com.banuba.sdk.pe.PhotoExportResultContract
import com.us.android.core.ui.photoeditor.PhotoEditResult
import com.us.android.core.ui.photoeditor.PhotoEditor
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The `:core:ui` photo editor PORT over Banuba's Photo Editor, behind the
 * same [BanubaGate] as the video editor: one token, one start, one answer.
 *
 * The Photo Editor is Koin-free (its AAR references core-sdk's licence
 * manager and the effect player, nothing of Koin), so nothing is registered
 * for it; it only needs the `EditorSdk.initialize` the gate already does.
 */
@Singleton
class BanubaPhotoEditor @Inject constructor(private val gate: BanubaGate) : PhotoEditor {
    override val ready: StateFlow<Boolean> = gate.photoEditorAvailable

    override fun ensure() = gate.ensure()

    override fun contract(): ActivityResultContract<Uri, PhotoEditResult> = BanubaPhotoEditContract()
}

/**
 * Image in, outcome out. The vendor's contract answers `Uri?` and cannot
 * tell a back-out from "the token does not include the Photo Editor" (both
 * are null); the result code can, and this contract reads it first.
 */
internal class BanubaPhotoEditContract : ActivityResultContract<Uri, PhotoEditResult>() {
    private val vendor = PhotoExportResultContract()

    override fun createIntent(context: Context, input: Uri): Intent =
        PhotoCreationActivity.startFromEditor(context, imageUri = input)

    override fun parseResult(resultCode: Int, intent: Intent?): PhotoEditResult =
        photoEditOutcomeOf(resultCode, vendor.parseResult(resultCode, intent))
}

/**
 * The mapping, on its own for the test: an OK result with a real Uri is the
 * export; OK with nothing is the licence gap the vendor sample describes;
 * anything but OK is a cancel.
 */
internal fun photoEditOutcomeOf(resultCode: Int, exported: Uri?): PhotoEditResult = when {
    resultCode != Activity.RESULT_OK -> PhotoEditResult.Cancelled
    exported == null || exported == Uri.EMPTY -> PhotoEditResult.Failed(PHOTO_EDITOR_NOT_LICENSED)
    else -> PhotoEditResult.Exported(exported)
}

internal const val PHOTO_EDITOR_NOT_LICENSED = "The licence does not include the Photo Editor"
