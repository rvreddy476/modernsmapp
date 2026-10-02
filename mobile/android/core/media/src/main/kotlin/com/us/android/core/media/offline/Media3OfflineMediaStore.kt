package com.us.android.core.media.offline

import android.content.Context
import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.database.DatabaseProvider
import androidx.media3.datasource.cache.Cache
import androidx.media3.datasource.cache.ContentMetadata
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.offline.DownloadManager
import androidx.media3.exoplayer.offline.DownloadRequest
import androidx.media3.exoplayer.scheduler.Requirements
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.media.di.OfflineCache
import com.us.android.core.network.di.AuthenticatedClient
import dagger.Lazy
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import java.io.File
import java.util.concurrent.Executor
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Offline copies' bytes, on Media3's own offline stack (2026-10-02).
 *
 * ## WHY MEDIA3 AND NOT A FILE
 *
 * `DownloadManager` and the cache are already in `media3-exoplayer` and
 * `media3-datasource`, which every player here is built on, so nothing was
 * added to the build. They give what a hand-written copier would have to
 * re-make: a fetch that survives the process and resumes from the byte it
 * stopped at, retries, "wait for Wi-Fi" as a requirement the platform
 * watches, and a cache the SAME players read with no network at all.
 *
 * ## WHERE THE BYTES ARE
 *
 * A [Cache] over [OfflineStorage.mediaDir], inside the app's no-backup
 * directory, with no evictor: a copy is removed because it expired or was
 * revoked, never because space ran short. The bytes are stored in the
 * cache's own span files under opaque names, not as a playable `.mp4`.
 * This is NOT the system `DownloadManager` service and never touches
 * shared storage, MediaStore or the Downloads collection.
 *
 * ## THE FETCH
 *
 * Through the app's AUTHENTICATED OkHttp client: the `serve` route is
 * authorized and answers with a redirect to a signed storage link, and the
 * bearer does not follow the redirect off the API host (`AuthOriginTest`).
 * Each stream is stored under its own cache key, so the redirect target
 * changing between a fetch and its resume changes nothing.
 *
 * ## NO FOREGROUND SERVICE
 *
 * The manager is driven directly, not through `DownloadService`: the app
 * has no notification channel a fetch belongs on, and a foreground service
 * needs one. A fetch therefore runs while the process lives and resumes on
 * the next launch ([fetch] is asked again for every unfinished copy).
 *
 * Every call into the manager is made on the main thread: it posts its
 * listener there, and its counters are not guarded for two callers.
 */
@Singleton
@OptIn(UnstableApi::class)
// One collaborator per thing the stack needs; a holder would only rename them.
@Suppress("LongParameterList")
class Media3OfflineMediaStore @Inject constructor(
    @ApplicationContext private val context: Context,
    private val storage: OfflineStorage,
    @OfflineCache private val cache: Lazy<Cache>,
    @OfflineCache private val database: Lazy<DatabaseProvider>,
    @AuthenticatedClient private val client: Lazy<OkHttpClient>,
    @Dispatcher(UsDispatcher.Main) private val main: CoroutineDispatcher,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
    @ApplicationScope private val scope: CoroutineScope,
) : OfflineMediaStore {

    private val _fetches = MutableStateFlow<Map<String, OfflineFetch>>(emptyMap())
    override val fetches: StateFlow<Map<String, OfflineFetch>> = _fetches.asStateFlow()

    private val files = OfflineFiles(root = { storage.filesDir }, client = { client.get() })

    /** Held until the manager exists; applied to it when it is made. */
    @Volatile
    private var wifiOnly = true

    /** The manager exists. Read on the main thread only, like the manager itself. */
    private var managerMade = false

    private var poll: Job? = null

    private val listener = object : DownloadManager.Listener {
        override fun onInitialized(downloadManager: DownloadManager) = publishAll(downloadManager)

        override fun onDownloadChanged(
            downloadManager: DownloadManager,
            download: Download,
            finalException: Exception?,
        ) {
            publish(downloadManager, download)
            pollWhileRunning(downloadManager)
        }

        override fun onDownloadRemoved(downloadManager: DownloadManager, download: Download) {
            _fetches.update { it - download.request.id }
        }

        // A fetch held for Wi-Fi reads as WAITING the moment the network changes, and RUNNING when it is back.
        override fun onWaitingForRequirementsChanged(
            downloadManager: DownloadManager,
            waitingForRequirements: Boolean,
        ) = publishAll(downloadManager)
    }

    /** Made on first use, on the main thread, and never before a copy exists. */
    private val manager: DownloadManager by lazy {
        DownloadManager(
            context,
            database.get(),
            cache.get(),
            OkHttpDataSource.Factory(client.get()),
            // Each fetch on its own thread, as Media3 documents for `Runnable::run`.
            Executor(Runnable::run),
        ).apply {
            maxParallelDownloads = PARALLEL_FETCHES
            requirements = Requirements(offlineNetworkRequirement(wifiOnly))
            addListener(listener)
            // A manager starts paused; `DownloadService` would resume it, and there is none.
            resumeDownloads()
            managerMade = true
        }
    }

    /**
     * Runs [block] with the manager, on the main thread. The cache is opened
     * FIRST and off the main thread: `SimpleCache` reads its index as it is
     * constructed, and the manager would otherwise open it where it is made.
     */
    private suspend fun <T> withManager(block: (DownloadManager) -> T): T {
        withContext(io) {
            cache.get()
            database.get()
        }
        return withContext(main) { block(manager) }
    }

    override suspend fun fetch(asset: OfflineAsset) = withManager { manager ->
        _fetches.update { current ->
            if (asset.key in current) current else current + (asset.key to OfflineFetch(OfflineFetchState.QUEUED))
        }
        manager.addDownload(
            DownloadRequest.Builder(asset.key, Uri.parse(asset.url))
                .setCustomCacheKey(asset.key)
                .setMimeType(asset.mime?.takeIf { it.isNotBlank() })
                .build(),
        )
    }

    override suspend fun remove(key: String) {
        withManager { manager ->
            manager.removeDownload(key)
            _fetches.update { it - key }
        }
        // The manager removes what it fetched; this takes a stream it has no record of.
        withContext(io) { runCatching { cache.get().removeResource(key) } }
    }

    override suspend fun removeAll() {
        // Nothing was ever stored: do not build a cache only to empty it.
        if (!storage.root.exists() || storage.root.list().isNullOrEmpty()) return
        withManager { manager ->
            manager.removeAllDownloads()
            _fetches.value = emptyMap()
        }
        withContext(io) {
            runCatching {
                val held = cache.get()
                held.keys.toList().forEach(held::removeResource)
            }
            files.removeAll()
        }
    }

    override suspend fun storedBytes(key: String): Long = withContext(io) {
        runCatching { cache.get().getCachedBytes(key, 0L, C.LENGTH_UNSET.toLong()) }
            .getOrDefault(0L)
            .coerceAtLeast(0L)
    }

    override suspend fun declaredBytes(key: String): Long = withContext(io) {
        runCatching { ContentMetadata.getContentLength(cache.get().getContentMetadata(key)) }
            .getOrDefault(OfflineFetch.UNKNOWN_LENGTH)
            .takeIf { it > 0L } ?: OfflineFetch.UNKNOWN_LENGTH
    }

    override suspend fun setWifiOnly(wifiOnly: Boolean) {
        this.wifiOnly = wifiOnly
        // Only a manager that exists has requirements to change; one made later reads the field.
        withContext(main) {
            if (managerMade) manager.requirements = Requirements(offlineNetworkRequirement(wifiOnly))
        }
    }

    override suspend fun fetchFile(url: String, folder: String, name: String): File? =
        withContext(io) { files.fetch(url, folder, name) }

    override suspend fun removeFiles(folder: String) = withContext(io) { files.remove(folder) }

    override suspend fun usedBytes(): Long = withContext(io) {
        if (!storage.root.exists()) return@withContext 0L
        storage.root.walkBottomUp().filter { it.isFile }.sumOf { it.length() }
    }

    override fun usableBytes(): Long = storage.usableBytes()

    // ── The manager's states, as ours ───────────────────────────────────

    private fun publishAll(downloadManager: DownloadManager) {
        downloadManager.currentDownloads.forEach { publish(downloadManager, it) }
        pollWhileRunning(downloadManager)
    }

    private fun publish(downloadManager: DownloadManager, download: Download) {
        val state = fetchStateOf(download.state, downloadManager.isWaitingForRequirements)
        if (state == null) {
            _fetches.update { it - download.request.id }
            return
        }
        val fetch = OfflineFetch(
            state = state,
            bytes = download.bytesDownloaded.coerceAtLeast(0L),
            totalBytes = download.contentLength.takeIf { it > 0L } ?: OfflineFetch.UNKNOWN_LENGTH,
        )
        _fetches.update { it + (download.request.id to fetch) }
    }

    /** The manager reports a state change, never a byte: progress is read while anything runs. */
    private fun pollWhileRunning(downloadManager: DownloadManager) {
        if (poll?.isActive == true) return
        poll = scope.launch(main) {
            while (downloadManager.currentDownloads.any { it.state == Download.STATE_DOWNLOADING }) {
                downloadManager.currentDownloads.forEach { publish(downloadManager, it) }
                delay(PROGRESS_POLL_MILLIS)
            }
        }
    }

    private companion object {
        /** A video and its sound together; a second copy waits its turn. */
        const val PARALLEL_FETCHES = 2
        const val PROGRESS_POLL_MILLIS = 500L
    }
}

/**
 * Media3's download state as ours; null for one being removed. A queued
 * fetch held by the network requirement is WAITING, which is what the row
 * says ("Waiting for Wi-Fi"). Pure, so the mapping is a table test.
 */
internal fun fetchStateOf(downloadState: Int, waitingForRequirements: Boolean): OfflineFetchState? =
    when (downloadState) {
        DOWNLOAD_COMPLETED -> OfflineFetchState.DONE
        DOWNLOAD_FAILED -> OfflineFetchState.FAILED
        DOWNLOAD_REMOVING -> null
        DOWNLOAD_DOWNLOADING, DOWNLOAD_RESTARTING ->
            if (waitingForRequirements) OfflineFetchState.WAITING else OfflineFetchState.RUNNING
        else -> if (waitingForRequirements) OfflineFetchState.WAITING else OfflineFetchState.QUEUED
    }

// `Download.STATE_*`, restated as constants so the mapping above is testable on the JVM without the class.
internal const val DOWNLOAD_DOWNLOADING = 2
internal const val DOWNLOAD_COMPLETED = 3
internal const val DOWNLOAD_FAILED = 4
internal const val DOWNLOAD_REMOVING = 5
internal const val DOWNLOAD_RESTARTING = 7
