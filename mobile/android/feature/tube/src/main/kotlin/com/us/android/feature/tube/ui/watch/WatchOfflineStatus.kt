package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.offline.OfflineEntry
import com.us.android.core.feed.offline.OfflinePhase
import com.us.android.core.feed.offline.WAITING_FOR_NETWORK
import com.us.android.core.media.ui.OfflineCopyBadge
import com.us.android.core.media.ui.OfflineSaveRing

/** What the watch screen says about this video's offline copy, under the player. */
sealed interface WatchOfflineStatus {
    /** The frame is coming off the device. */
    data object Playing : WatchOfflineStatus

    /** A copy is being saved: how far, or what it is held for. */
    data class Saving(val progress: Float?, val waiting: String?) : WatchOfflineStatus
}

/**
 * The line under the player (offline copies, 2026-10-02), or null for none:
 * "Offline copy" while the stored copy is what plays, the save's ring while
 * one is being made. A copy that is stored but was saved after this video
 * started says nothing: what is on screen is still the network's stream.
 * Pure, so the three cases are a table test.
 */
fun watchOfflineStatus(offlineCopy: Boolean, entry: OfflineEntry?): WatchOfflineStatus? = when {
    offlineCopy -> WatchOfflineStatus.Playing
    entry == null || entry.phase == OfflinePhase.STORED -> null
    entry.phase == OfflinePhase.WAITING -> WatchOfflineStatus.Saving(entry.progress, WAITING_FOR_NETWORK)
    else -> WatchOfflineStatus.Saving(entry.progress, waiting = null)
}

/** The status as the first row of the details, when there is one. */
internal fun LazyListScope.watchOfflineStatusItem(offlineCopy: Boolean, entry: OfflineEntry?) {
    val status = watchOfflineStatus(offlineCopy, entry) ?: return
    item(key = "offline_status") { WatchOfflineLine(status) }
}

@Composable
private fun WatchOfflineLine(status: WatchOfflineStatus) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = UsTheme.spacing.l)
            .testTag("watch_offline_status"),
    ) {
        when (status) {
            WatchOfflineStatus.Playing -> OfflineCopyBadge()
            is WatchOfflineStatus.Saving -> OfflineSaveRing(progress = status.progress, waiting = status.waiting)
        }
    }
}

@Preview
@Composable
private fun WatchOfflineLinePreview() {
    UsTheme {
        Box(Modifier.background(UsTheme.extended.bgCanvas)) {
            WatchOfflineLine(WatchOfflineStatus.Saving(progress = 0.42f, waiting = null))
        }
    }
}
