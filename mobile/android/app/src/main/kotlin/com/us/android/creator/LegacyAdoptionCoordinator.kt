package com.us.android.creator

import com.us.android.core.creator.engine.AdoptionRunner
import com.us.android.core.telemetry.Telemetry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Fires the legacy-draft adoption pass once per process start.
 *
 * ## WHY THIS EXISTS
 *
 * `MIGRATION_2_3` stages the legacy composer draft; [AdoptionRunner] is the
 * stage-two/three orchestration that turns a staged row into a project or a
 * typed recovery. Without a caller, an upgrading user's draft sat safely in
 * staging — preserved, but never adopted. This is that caller.
 *
 * ## WHY OFF THE COLD-START PATH
 *
 * The runner's fast path is one indexed read of an empty table, but its slow
 * path does filesystem copies. It launches on IO after `onCreate` returns, and
 * the runner's own idempotency (`adoptionState` is re-read on entry) makes a
 * process death mid-run an ordinary retry on next launch — so there is nothing
 * a startup race could lose.
 *
 * Failure is recorded, never rethrown: an adoption error must not take down
 * app start, because the staged row is still there and the next launch tries
 * again.
 */
@Singleton
class LegacyAdoptionCoordinator @Inject constructor(
    private val runner: AdoptionRunner,
    private val telemetry: Telemetry,
) {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    fun start() {
        scope.launch {
            runCatching { runner.runIfNeeded(now = System.currentTimeMillis()) }
                .onFailure { error ->
                    telemetry.recordError(
                        event = "creator.adoption.failed",
                        cause = error,
                        attributes = emptyMap(),
                    )
                }
        }
    }
}
