package com.us.android.feature.tube.ui.offline

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.feed.offline.OfflineEntry
import com.us.android.core.feed.offline.OfflineKind
import com.us.android.core.feed.offline.OfflineLibrary
import com.us.android.core.feed.offline.OfflinePhase
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.offline.WAITING_FOR_NETWORK
import com.us.android.core.feed.offline.toFeedItem
import com.us.android.core.media.ReelsEntry
import com.us.android.feature.tube.data.TubeQueue
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The Offline page (2026-10-02): what this device keeps, in two sections,
 * with how much room it takes, when each copy expires, and the ways to let
 * one go.
 *
 * It reads [OfflineLibrary] and nothing else, so it opens with NO network:
 * the list is the index on the device, the stills are stored files, and a
 * tap plays from the device. On open it deletes what has expired and, when
 * a network is there, asks the server about the rest; no answer changes
 * nothing.
 */
@HiltViewModel
class OfflineViewModel @Inject constructor(
    private val offline: OfflineLibrary,
    private val reelsEntry: ReelsEntry,
    private val queue: TubeQueue,
) : ViewModel() {

    val state: StateFlow<OfflineState> = offline.state

    /** "Save on Wi-Fi only". True until the stored choice is read, which is also the default. */
    val wifiOnly: StateFlow<Boolean> = offline.wifiOnly
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), true)

    private val _notice = MutableStateFlow<UsMessage?>(null)

    /** Copies that went on their own while the page was open. */
    val notice: StateFlow<UsMessage?> = _notice.asStateFlow()

    init {
        viewModelScope.launch {
            offline.notices.collect { _notice.value = UsMessage(text = it, type = UsMessageType.Info) }
        }
        viewModelScope.launch {
            offline.ensureLoaded()
            offline.refresh()
        }
    }

    fun dismissNotice() {
        _notice.value = null
    }

    fun setWifiOnly(enabled: Boolean) {
        viewModelScope.launch { offline.setWifiOnly(enabled) }
    }

    fun remove(postId: String) {
        viewModelScope.launch { offline.remove(postId) }
    }

    fun removeAll() {
        viewModelScope.launch { offline.removeAll() }
    }

    /**
     * A stored copy was tapped. A long video takes the other stored videos
     * with it as the watch screen's queue, so "Up next" works with no
     * network too; a reel leaves its id where Reels reads it. Answers which
     * of the two the caller should open, or null for a copy that cannot be
     * played yet (still saving).
     */
    fun onOpen(postId: String): OfflineKind? {
        val copy = offline.playable(postId) ?: return null
        when (copy.kind) {
            OfflineKind.VIDEO -> queue.set(
                offlineSections(state.value).videos.mapNotNull { row -> offline.playable(row.postId)?.toFeedItem() },
            )
            OfflineKind.REEL -> reelsEntry.open(postId)
        }
        return copy.kind
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}

/** One row of the page. */
data class OfflineRow(
    val postId: String,
    val kind: OfflineKind,
    val title: String,
    val channelName: String,
    /** The stored still, for the app's image loader; null while it has none. */
    val posterFile: String?,
    val stored: Boolean,
    /** 0..1 while saving; null once stored, or while the length is not known. */
    val progress: Float?,
    /** Under the title: the channel, then the size and the expiry, or how the save is going. */
    val meta: String,
)

/** The page's two lists, newest copy first. */
data class OfflineSections(val videos: List<OfflineRow>, val reels: List<OfflineRow>) {
    val isEmpty: Boolean get() = videos.isEmpty() && reels.isEmpty()
    val count: Int get() = videos.size + reels.size
}

/**
 * The copies as the page lists them: long videos, then reels, each newest
 * first. A save whose grant is still on the wire has nothing to show yet
 * and is left out. Pure, so the sections are a table test.
 */
fun offlineSections(state: OfflineState, nowMs: Long = System.currentTimeMillis()): OfflineSections {
    val rows = state.copies.values
        .filter { it.copy != null }
        .sortedByDescending { it.copy?.grantedAtMs ?: 0L }
        .mapNotNull { it.toRow(nowMs) }
    return OfflineSections(
        videos = rows.filter { it.kind == OfflineKind.VIDEO },
        reels = rows.filter { it.kind == OfflineKind.REEL },
    )
}

private fun OfflineEntry.toRow(nowMs: Long): OfflineRow? {
    val copy = copy ?: return null
    val stored = phase == OfflinePhase.STORED
    val status = when (phase) {
        OfflinePhase.STORED -> "${offlineSizeLabel(copy.sizeBytes)} · ${offlineExpiryLabel(copy.expiresAtMs, nowMs)}"
        OfflinePhase.WAITING -> WAITING_FOR_NETWORK
        OfflinePhase.REQUESTING, OfflinePhase.SAVING -> offlineSavingLabel(progress)
    }
    return OfflineRow(
        postId = copy.postId,
        kind = copy.kind,
        title = copy.title.ifBlank { if (copy.kind == OfflineKind.REEL) "Reel" else "Video" },
        channelName = copy.channelName,
        posterFile = copy.posterFile,
        stored = stored,
        progress = progress.takeIf { !stored },
        meta = listOf(copy.channelName, status).filter { it.isNotBlank() }.joinToString(" · "),
    )
}

/** "Saving 42%", or "Saving" while the length is not known. */
fun offlineSavingLabel(progress: Float?): String =
    if (progress == null) "Saving" else "Saving ${(progress.coerceIn(0f, 1f) * PERCENT).toInt()}%"

/** "512 KB", "184 MB", "1.2 GB": what a copy takes on the device, in the unit a person would say. */
fun offlineSizeLabel(bytes: Long): String {
    val size = bytes.coerceAtLeast(0L).toDouble()
    return when {
        size >= GIGABYTE -> "${oneDecimal(size / GIGABYTE)} GB"
        size >= MEGABYTE -> "${(size / MEGABYTE).toLong()} MB"
        size >= KILOBYTE -> "${(size / KILOBYTE).toLong()} KB"
        else -> "${size.toLong()} B"
    }
}

/**
 * "Expires in 12 days", "Expires tomorrow", "Expires today". Whole days
 * left, rounded down: a copy with 36 hours left expires "tomorrow", never
 * "in 2 days", so the label never promises more than the server granted.
 */
fun offlineExpiryLabel(expiresAtMs: Long, nowMs: Long): String {
    val days = ((expiresAtMs - nowMs).coerceAtLeast(0L) / DAY_MILLIS).toInt()
    return when (days) {
        0 -> "Expires today"
        1 -> "Expires tomorrow"
        else -> "Expires in $days days"
    }
}

/** "3 copies · 512 MB on this device". */
fun offlineStorageLabel(count: Int, usedBytes: Long): String =
    "$count ${if (count == 1) "copy" else "copies"} · ${offlineSizeLabel(usedBytes)} on this device"

private fun oneDecimal(value: Double): String {
    val tenths = (value * TENTHS).toLong()
    return "${tenths / TENTHS}.${tenths % TENTHS}"
}

private const val PERCENT = 100
private const val TENTHS = 10
private const val KILOBYTE = 1024.0
private const val MEGABYTE = 1024.0 * 1024
private const val GIGABYTE = 1024.0 * 1024 * 1024
private const val DAY_MILLIS = 24L * 60 * 60 * 1000
