package com.us.android.core.media.offline

import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.scheduler.Requirements
import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * How Media3's own states are read (offline copies, 2026-10-02).
 *
 * What this protects: "Save on Wi-Fi only" really asking for an unmetered
 * network (and any network otherwise), and a fetch held by that rule
 * reading as WAITING rather than as a save that is quietly going nowhere.
 * The Media3 constants are restated in the module so the rules are pure;
 * the first test pins the restatement to the library.
 */
@OptIn(UnstableApi::class)
class OfflineFetchStateTest {

    @Test
    fun `the restated constants are media3's`() {
        assertThat(REQUIREMENT_NETWORK).isEqualTo(Requirements.NETWORK)
        assertThat(REQUIREMENT_NETWORK_UNMETERED).isEqualTo(Requirements.NETWORK_UNMETERED)
        assertThat(DOWNLOAD_DOWNLOADING).isEqualTo(Download.STATE_DOWNLOADING)
        assertThat(DOWNLOAD_COMPLETED).isEqualTo(Download.STATE_COMPLETED)
        assertThat(DOWNLOAD_FAILED).isEqualTo(Download.STATE_FAILED)
        assertThat(DOWNLOAD_REMOVING).isEqualTo(Download.STATE_REMOVING)
        assertThat(DOWNLOAD_RESTARTING).isEqualTo(Download.STATE_RESTARTING)
    }

    @Test
    fun `wifi only fetches on an unmetered network and nothing else`() {
        assertThat(offlineNetworkRequirement(wifiOnly = true)).isEqualTo(Requirements.NETWORK_UNMETERED)
    }

    @Test
    fun `with the switch off any network will do`() {
        assertThat(offlineNetworkRequirement(wifiOnly = false)).isEqualTo(Requirements.NETWORK)
    }

    @Test
    fun `a queued fetch held by the network rule is waiting, not queued`() {
        assertThat(fetchStateOf(Download.STATE_QUEUED, waitingForRequirements = true))
            .isEqualTo(OfflineFetchState.WAITING)
        assertThat(fetchStateOf(Download.STATE_QUEUED, waitingForRequirements = false))
            .isEqualTo(OfflineFetchState.QUEUED)
        assertThat(fetchStateOf(Download.STATE_STOPPED, waitingForRequirements = false))
            .isEqualTo(OfflineFetchState.QUEUED)
    }

    @Test
    fun `a running, a finished and a failed fetch read as themselves`() {
        assertThat(fetchStateOf(Download.STATE_DOWNLOADING, false)).isEqualTo(OfflineFetchState.RUNNING)
        assertThat(fetchStateOf(Download.STATE_RESTARTING, false)).isEqualTo(OfflineFetchState.RUNNING)
        assertThat(fetchStateOf(Download.STATE_COMPLETED, false)).isEqualTo(OfflineFetchState.DONE)
        assertThat(fetchStateOf(Download.STATE_FAILED, false)).isEqualTo(OfflineFetchState.FAILED)
        // Finished stays finished whatever the network is doing.
        assertThat(fetchStateOf(Download.STATE_COMPLETED, true)).isEqualTo(OfflineFetchState.DONE)
    }

    @Test
    fun `a fetch being removed is no fetch`() {
        assertThat(fetchStateOf(Download.STATE_REMOVING, false)).isNull()
    }
}
