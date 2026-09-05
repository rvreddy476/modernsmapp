package com.us.android.feature.post.createhub.banuba

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import com.banuba.sdk.export.data.ExportError
import com.banuba.sdk.export.data.ExportResult
import com.banuba.sdk.ve.flow.VideoCreationActivity
import com.banuba.sdk.ve.flow.VideoExportResultContract

/** What came back from the Banuba editor, in the reel flow's own terms. */
internal sealed interface BanubaExportOutcome {
    /** A rendered MP4 on disk — what [com.us.android.feature.post.createhub.ReelPublishViewModel.onReelExported] takes. */
    data class Exported(val path: String) : BanubaExportOutcome

    /** The SDK reported an error; the message is for the person, not a log. */
    data class Failed(val message: String) : BanubaExportOutcome

    /** Backed out before exporting. Nothing to do. */
    data object Cancelled : BanubaExportOutcome
}

/** The vendor's result contract, mapped. Null is what the contract yields for a plain back-out. */
internal fun exportOutcomeOf(result: ExportResult?): BanubaExportOutcome = when (result) {
    is ExportResult.Success -> result.videoList.firstOrNull()?.sourceUri?.toFilePath()
        ?.let(BanubaExportOutcome::Exported)
        ?: BanubaExportOutcome.Failed(NO_FILE)
    is ExportResult.Error -> BanubaExportOutcome.Failed(messageFor(result.type))
    else -> BanubaExportOutcome.Cancelled
}

/** Exports land in our cache directory as plain files; anything else is not something the pipeline can upload. */
private fun Uri.toFilePath(): String? =
    path?.takeIf { (scheme == null || scheme == FILE_SCHEME) && it.isNotBlank() }

private fun messageFor(type: ExportError): String = when (type) {
    ExportError.INVALID_LICENSE -> "The advanced editor licence is not valid."
    ExportError.CODEC_ERROR -> "This device could not encode the video."
    else -> "Export failed. Try again."
}

private const val FILE_SCHEME = "file"
private const val NO_FILE = "The editor returned no video file."

/** The two ways into the Banuba flow: its camera, or its trimmer over a video already picked. */
internal class BanubaEditor(
    val record: () -> Unit,
    val edit: (uri: String) -> Unit,
)

/**
 * A launcher for the Banuba flow. [prepare] runs just before each launch —
 * the surface uses it to point the export at the current creation key —
 * and [onOutcome] receives the mapped result.
 */
@Composable
internal fun rememberBanubaEditor(
    prepare: () -> Unit,
    onOutcome: (BanubaExportOutcome) -> Unit,
): BanubaEditor {
    val context = LocalContext.current
    val latestOutcome = rememberUpdatedState(onOutcome)
    val latestPrepare = rememberUpdatedState(prepare)
    val launcher = rememberLauncherForActivityResult(VideoExportResultContract()) { result ->
        latestOutcome.value(exportOutcomeOf(result))
    }
    return remember(launcher, context) {
        BanubaEditor(
            record = {
                latestPrepare.value()
                launcher.launch(VideoCreationActivity.startFromCamera(context))
            },
            edit = { uri ->
                latestPrepare.value()
                launcher.launch(VideoCreationActivity.startFromTrimmer(context, arrayOf(Uri.parse(uri))))
            },
        )
    }
}
