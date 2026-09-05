package com.us.android.feature.post.createhub.banuba

import androidx.lifecycle.ViewModel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * The reel surface's handle on the process-wide [BanubaGate]: its state, the
 * lazy start, and the export target for the launch about to happen.
 */
@HiltViewModel
class BanubaGateViewModel @Inject constructor(
    private val gate: BanubaGate,
    private val exportTarget: BanubaExportTarget,
) : ViewModel() {

    val state: StateFlow<BanubaState> = gate.state

    /** First entry to the reel flow: start the SDK if there is a token. Idempotent. */
    fun ensure() = gate.ensure()

    /** Where the next export is written — the publish store's file for the current creation key. */
    fun exportTo(path: String) {
        exportTarget.path = path
    }
}
