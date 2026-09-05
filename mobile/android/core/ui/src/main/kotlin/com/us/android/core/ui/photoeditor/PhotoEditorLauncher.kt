package com.us.android.core.ui.photoeditor

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * The editor as one function — `(image) -> Unit` — or NULL when it cannot be
 * offered, so a screen renders its Edit action only when tapping it would
 * open something. Starts the editor on first composition ([PhotoEditor.ensure]).
 *
 * An export is copied out of the editor's `content://` into this app's cache
 * as a JPEG before [onEdited] sees it: the editor's own file can vanish once
 * its activity is gone, and every consumer (the studio's vault, the profile
 * uploader) wants a plain file of its own anyway. The copy runs off the main
 * thread; a copy that fails is reported through [onFailed].
 */
@Composable
fun rememberPhotoEditor(
    editor: PhotoEditor,
    onEdited: (path: String) -> Unit,
    onFailed: (message: String) -> Unit,
    onCancelled: () -> Unit = {},
): ((Uri) -> Unit)? {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val latestEdited by rememberUpdatedState(onEdited)
    val latestFailed by rememberUpdatedState(onFailed)
    val latestCancelled by rememberUpdatedState(onCancelled)
    LaunchedEffect(editor) { editor.ensure() }
    val ready by editor.ready.collectAsStateWithLifecycle()
    val launcher = rememberLauncherForActivityResult(editor.contract()) { result ->
        when (result) {
            is PhotoEditResult.Exported -> scope.launch {
                val path = withContext(Dispatchers.IO) { copyExportToCache(context, result.image) }
                if (path != null) latestEdited(path) else latestFailed(COPY_FAILED)
            }
            is PhotoEditResult.Failed -> latestFailed(result.message)
            PhotoEditResult.Cancelled -> latestCancelled()
        }
    }
    val launch: (Uri) -> Unit = { image -> launcher.launch(image) }
    return launch.takeIf { ready }
}

private const val COPY_FAILED = "The edited photo could not be saved."
