package com.us.android.feature.post.createhub.banuba

import androidx.lifecycle.ViewModel
import com.us.android.core.ui.photoeditor.PhotoEditor
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * The create surfaces' handle on the process-wide [BanubaGate]: its state,
 * the lazy start, the export target for the reel launch about to happen, and
 * the [photoEditor] the photo surface's "take & edit" path launches.
 */
@HiltViewModel
class BanubaGateViewModel @Inject constructor(
    private val gate: BanubaGate,
    private val exportTarget: BanubaExportTarget,
    val photoEditor: PhotoEditor,
) : ViewModel() {

    val state: StateFlow<BanubaState> = gate.state

    /** First entry to a flow that wants the SDK: start it if there is a token. Idempotent. */
    fun ensure() = gate.ensure()

    /** Where the next export is written — the publish store's file for the current creation key. */
    fun exportTo(path: String) {
        exportTarget.path = path
    }
}
