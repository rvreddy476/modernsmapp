package com.us.android.core.feed.offline

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.OfflinePrefs
import com.us.android.core.media.offline.OfflineAsset
import com.us.android.core.media.offline.OfflineFetch
import com.us.android.core.media.offline.OfflineMediaStore
import com.us.android.core.media.offline.OfflineStorage
import com.us.android.core.model.FeedItem
import com.us.android.core.model.SessionState
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton

/**
 * What a screen can ask of offline copies. An interface so the ViewModels
 * that read it (the More sheet's, Reels', the watch screen's, the Offline
 * page's) are tested against a fake.
 */
interface OfflineLibrary {
    /** Every copy on this device for the signed-in viewer, and where each stands. */
    val state: StateFlow<OfflineState>

    /** One quiet line when copies went on their own: expired, no longer allowed, or a save that failed. */
    val notices: SharedFlow<String>

    /** "Save on Wi-Fi only". */
    val wifiOnly: Flow<Boolean>

    suspend fun setWifiOnly(enabled: Boolean)

    /** Reads the index if it has not been read. Cheap, and safe to call from anywhere. */
    suspend fun ensureLoaded()

    /** Asks the server for a copy of [item] and starts storing it. Idempotent. */
    suspend fun save(item: FeedItem): OfflineSaveResult

    /** Cancels a save in flight, or removes a stored copy. The server is told when it can be. */
    suspend fun remove(postId: String)

    suspend fun removeAll()

    /**
     * Deletes what has expired (no network needed), then asks the server
     * about every copy whose last answer has aged out, or all of them when
     * [force]. No answer changes nothing.
     */
    suspend fun refresh(force: Boolean = false)

    /** The copy of [postId] when it can be played right now, with no network; null otherwise. */
    fun playable(postId: String): OfflineCopy?
}

/**
 * No offline copies at all: nothing stored, every save refused. For
 * previews and for tests of a screen whose subject is something else, as
 * `NoOpAnalyticsRecorder` is.
 */
object NoOfflineLibrary : OfflineLibrary {
    override val state: StateFlow<OfflineState> = MutableStateFlow(OfflineState(loaded = true))
    override val notices: SharedFlow<String> = MutableSharedFlow()
    override val wifiOnly: Flow<Boolean> = MutableStateFlow(true)
    override suspend fun setWifiOnly(enabled: Boolean) = Unit
    override suspend fun ensureLoaded() = Unit
    override suspend fun save(item: FeedItem): OfflineSaveResult = OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE)
    override suspend fun remove(postId: String) = Unit
    override suspend fun removeAll() = Unit
    override suspend fun refresh(force: Boolean) = Unit
    override fun playable(postId: String): OfflineCopy? = null
}

/** Schedules the background check. An interface so the state machine is tested without WorkManager. */
interface OfflineCheckScheduler {
    /** There are copies: check them when a network is there, and daily after. */
    fun schedule()

    /** There are none left: nothing to wake up for. */
    fun cancel()
}

/**
 * Offline copies on this device (2026-10-02): the state machine.
 *
 * founder, 2026-10-02: "Keep a copy is not direct download. It should be
 * like to see offline in the app only like YouTube, TikTok or Instagram.
 * Nobody can see download location."
 *
 * ## ONE COPY'S LIFE
 *
 *   idle → requesting (the grant) → saving(progress) ⇄ waiting (for Wi-Fi)
 *        → stored → removed (by the viewer, by expiry, or by the server's word)
 *
 * A save that is cancelled, that fails, or whose bytes are not the length
 * that was promised goes straight back to idle with nothing left on the
 * device. A save cut short by the process dying is resumed by [start] from
 * the byte it stopped at.
 *
 * ## WHO OWNS WHAT
 *
 * The server decides whether a copy may exist ([OfflineRemote]); the bytes
 * are `:core:media`'s ([OfflineMediaStore]); the list of copies is one file
 * beside the bytes ([OfflineIndex]). This class holds them together and
 * owns no bytes itself. Every rule it applies is a pure function in
 * `OfflineRules.kt`.
 *
 * ## ONE VIEWER
 *
 * Copies belong to the account that saved them. The index names its owner,
 * and a different signed-in viewer finds an empty list and has the previous
 * owner's bytes deleted ([guardOwner]). Sign-out wipes everything
 * ([wipeForSignOut]).
 *
 * A singleton for the reason `VideoLibraryStore` is: the More sheet's row,
 * the ring on the watch screen and the Offline page must agree the instant
 * a copy changes.
 */
@Singleton
// One collaborator per seam the machine is tested through, and one function per thing a screen can ask.
@Suppress("LongParameterList", "TooManyFunctions")
class OfflineCopies internal constructor(
    private val remote: OfflineRemote,
    private val store: OfflineMediaStore,
    private val index: OfflineIndex,
    private val prefs: OfflinePrefs,
    /** The signed-in viewer's id, or blank. */
    private val viewerId: () -> String,
    private val connection: () -> OfflineConnection,
    /** Epoch milliseconds. */
    private val now: () -> Long,
    private val scheduler: OfflineCheckScheduler,
    /** Saves and removals outlive the screen that asked for them. */
    private val scope: CoroutineScope,
    private val io: CoroutineDispatcher,
) : OfflineLibrary {

    @Inject
    constructor(
        remote: OfflineRepository,
        store: OfflineMediaStore,
        storage: OfflineStorage,
        prefs: OfflinePrefs,
        // A Provider, as `AnalyticsStore` takes it: the session reaches sign-out, sign-out reaches the
        // teardown tasks, and the offline teardown reaches this class. Resolved on first use, the
        // cycle never forms.
        session: Provider<SessionStateProvider>,
        connectivity: OfflineConnectivity,
        scheduler: OfflineCheckScheduler,
        @ApplicationScope scope: CoroutineScope,
        @Dispatcher(UsDispatcher.IO) io: CoroutineDispatcher,
    ) : this(
        remote = remote,
        store = store,
        index = OfflineIndex(file = { storage.indexFile }),
        prefs = prefs,
        viewerId = { (session.get().sessionState.value as? SessionState.Authenticated)?.userId.orEmpty() },
        connection = connectivity::current,
        now = System::currentTimeMillis,
        scheduler = scheduler,
        scope = scope,
        io = io,
    )

    private val _state = MutableStateFlow(OfflineState())
    override val state: StateFlow<OfflineState> = _state.asStateFlow()

    private val _notices = MutableSharedFlow<String>(extraBufferCapacity = NOTICE_BUFFER)
    override val notices: SharedFlow<String> = _notices.asSharedFlow()

    override val wifiOnly: Flow<Boolean> get() = prefs.wifiOnly

    /** Guards [file], [requesting] and [loaded]. */
    private val lock = Mutex()
    private var file = OfflineIndexFile()
    private val requesting = mutableSetOf<String>()
    private var loaded = false

    // ── Start ───────────────────────────────────────────────────────────

    /**
     * The application started: read the index, delete what expired while
     * the app was closed, pick unfinished saves back up, and ask the server
     * about the rest once a network is there. Off the cold-start path: it
     * returns at once and works on [scope].
     *
     * Nothing thrown here leaves the coroutine: this runs on every launch,
     * and storage that cannot be opened must cost the viewer their offline
     * copies for this run, never the app.
     */
    fun start() {
        scope.launch { runCatching { resumeAndCheck() } }
    }

    private suspend fun resumeAndCheck() {
        val unfinished = lock.withLock {
            load()
            guardOwner()
            sweepExpired()
            publish()
            visibleCopies().filterNot { it.stored }
        }
        if (unfinished.isNotEmpty()) {
            store.setWifiOnly(prefs.wifiOnly.first())
            unfinished.forEach { fetchStreams(it) }
        }
        if (lock.withLock { file.copies.isNotEmpty() }) scheduler.schedule()
        refresh()
    }

    override suspend fun ensureLoaded() {
        lock.withLock {
            if (loaded) return
            load()
            publish()
        }
    }

    /** Reads the index once and starts the two collections that keep the state current. Call under [lock]. */
    private suspend fun load() {
        if (loaded) return
        file = withContext(io) { index.read() }
        loaded = true
        scope.launch { store.fetches.collect { onFetches(it) } }
        scope.launch { prefs.wifiOnly.collect { store.setWifiOnly(it) } }
    }

    /**
     * Another account's copies are deleted the moment this one is known:
     * they were granted to someone else, and this viewer may not be allowed
     * to watch them at all. Call under [lock].
     */
    private suspend fun guardOwner() {
        val viewer = viewerId()
        if (viewer.isBlank() || file.copies.isEmpty() || file.ownerId == viewer) return
        wipeLocal()
    }

    // ── Save ────────────────────────────────────────────────────────────

    /** On [scope], so a save asked for from a sheet finishes after the sheet has gone. */
    override suspend fun save(item: FeedItem): OfflineSaveResult = scope.async {
        // Storage that cannot be opened is a save that could not be made, said in one line; never a crash.
        runCatching { saveNow(item) }.getOrElse { failure ->
            if (failure is CancellationException) throw failure
            OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE)
        }
    }.await()

    private suspend fun saveNow(item: FeedItem): OfflineSaveResult {
        val viewer = viewerId()
        if (viewer.isBlank()) return OfflineSaveResult.Refused(SIGN_IN_FIRST)
        val postId = item.id
        lock.withLock {
            load()
            guardOwner()
            // Already saving or stored: the same wish, already granted.
            if (postId in requesting || file.copies.any { it.postId == postId }) return OfflineSaveResult.Started
            requesting += postId
            publish()
        }
        return try {
            grantAndFetch(item, viewer)
        } finally {
            lock.withLock {
                requesting -= postId
                publish()
            }
        }
    }

    private suspend fun grantAndFetch(item: FeedItem, viewer: String): OfflineSaveResult {
        val deviceId = prefs.deviceId()
        val grant = when (val result = remote.grant(item.id, deviceId, now())) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return OfflineSaveResult.Refused(offlineRefusalMessage(result.error))
        }
        val needed = grant.video.sizeBytes + (grant.sound?.stream?.sizeBytes ?: 0L)
        if (!hasRoomFor(needed, store.usableBytes())) {
            // The grant counts against the viewer's hundred; give it back.
            remote.remove(item.id, deviceId)
            return OfflineSaveResult.Refused(NO_ROOM)
        }
        val copy = grant.toCopy(item, now())
        val wifiOnly = prefs.wifiOnly.first()
        lock.withLock {
            file = file.copy(ownerId = viewer, deviceId = deviceId, copies = file.copies + copy)
            persist()
            publish()
        }
        store.setWifiOnly(wifiOnly)
        fetchStreams(copy)
        scope.launch { fetchSmallFiles(copy, grant) }
        scheduler.schedule()
        return when (offlineGate(wifiOnly, connection())) {
            OfflineGate.WAIT_FOR_WIFI -> OfflineSaveResult.WaitingForWifi
            OfflineGate.GO, OfflineGate.WAIT_FOR_NETWORK -> OfflineSaveResult.Started
        }
    }

    private suspend fun fetchStreams(copy: OfflineCopy) {
        copy.streams.forEach { store.fetch(OfflineAsset(key = it.key, url = it.url, mime = it.mime)) }
    }

    /**
     * The poster and the caption tracks, beside the streams. Best effort: a
     * copy without a poster or without captions is still a copy, so nothing
     * here can fail the save.
     */
    private suspend fun fetchSmallFiles(copy: OfflineCopy, grant: OfflineGrant) {
        val poster = grant.posterUrl?.let { store.fetchFile(it, copy.folder, POSTER_FILE) }?.absolutePath
        val captions = copy.captions.map { caption ->
            val stored = store.fetchFile(caption.url, copy.folder, captionFileName(caption.language))
            caption.copy(file = stored?.absolutePath)
        }
        lock.withLock {
            // Removed while the files were on their way: take them back out.
            val current = file.copies.firstOrNull { it.postId == copy.postId }
            if (current == null) {
                store.removeFiles(copy.folder)
                return
            }
            replace(current.copy(posterFile = poster, captions = captions))
            persist()
            publish()
        }
    }

    // ── The fetches, as they arrive ─────────────────────────────────────

    /**
     * Every change in the streams' fetches. A copy whose streams have all
     * arrived is verified and becomes stored; one whose fetch was given up
     * on, or whose bytes are not the length that was promised, is discarded.
     */
    private suspend fun onFetches(fetches: Map<String, OfflineFetch>) {
        val discarded = mutableListOf<OfflineCopy>()
        lock.withLock {
            var stored = false
            for (copy in file.copies.filterNot { it.stored }) {
                when (summarizeFetches(copy.streams, fetches)) {
                    is OfflineFetchSummary.Running -> Unit
                    OfflineFetchSummary.Failed -> discarded += copy
                    OfflineFetchSummary.Done -> {
                        val size = verifiedSize(copy)
                        if (size == null) {
                            discarded += copy
                        } else {
                            replace(copy.copy(stored = true, sizeBytes = size))
                            stored = true
                        }
                    }
                }
            }
            discarded.forEach { removeLocal(it) }
            // Progress arrives twice a second; the index is written only when a copy changed hands.
            if (stored || discarded.isNotEmpty()) persist()
            publish()
        }
        if (discarded.isEmpty()) return
        _notices.tryEmit(COULD_NOT_SAVE_OFFLINE)
        tellServerRemoved(discarded.map { it.postId })
    }

    /** The bytes on the device when every stream is the length it should be; null when any is not. */
    private suspend fun verifiedSize(copy: OfflineCopy): Long? {
        var total = 0L
        for (stream in copy.streams) {
            val stored = store.storedBytes(stream.key)
            if (!sizeMatches(stream.sizeBytes, store.declaredBytes(stream.key), stored)) return null
            total += stored
        }
        return total
    }

    // ── Remove ──────────────────────────────────────────────────────────

    override suspend fun remove(postId: String) {
        scope.async {
            val removed = lock.withLock {
                load()
                val copy = file.copies.firstOrNull { it.postId == postId } ?: return@withLock false
                removeLocal(copy)
                persist()
                publish()
                true
            }
            if (removed) tellServerRemoved(listOf(postId))
        }.await()
    }

    override suspend fun removeAll() {
        scope.async {
            val ids = lock.withLock {
                load()
                val ids = file.copies.map { it.postId }
                store.removeAll()
                file = file.copy(copies = emptyList())
                persist()
                publish()
                ids
            }
            tellServerRemoved(ids)
        }.await()
    }

    /**
     * Sign-out. The device is wiped FIRST and unconditionally, because that
     * is the promise: the next account, and anyone holding the phone, finds
     * nothing. Then, while the session is still valid, the server is told
     * for each copy, for as long as [SIGN_OUT_REMOVE_MILLIS] allows; with no
     * network the rows age out on the server by their own expiry.
     */
    suspend fun wipeForSignOut() {
        val (ids, deviceId) = lock.withLock {
            // Read whatever is on disk, whoever it belongs to.
            if (!loaded) load()
            val held = file.copies.map { it.postId } to file.deviceId
            wipeLocal()
            requesting.clear()
            publish()
            held
        }
        scheduler.cancel()
        if (ids.isEmpty() || deviceId.isBlank()) return
        withTimeoutOrNull(SIGN_OUT_REMOVE_MILLIS) {
            ids.forEach { remote.remove(it, deviceId) }
        }
    }

    /** Every byte and the index. Call under [lock]. */
    private suspend fun wipeLocal() {
        // The index goes even if the bytes could not be reached: a copy the app no longer lists cannot be
        // played, and what is left on disk is taken by the next wipe.
        runCatching { store.removeAll() }
        withContext(io) { index.delete() }
        file = OfflineIndexFile()
    }

    /** One copy's bytes and files, and its row. Call under [lock]; the caller persists. */
    private suspend fun removeLocal(copy: OfflineCopy) {
        copy.streams.forEach { store.remove(it.key) }
        store.removeFiles(copy.folder)
        file = file.copy(copies = file.copies.filterNot { it.postId == copy.postId })
    }

    /** Best effort: a remove the server never hears of is found again by [reconcile], or ages out. */
    private suspend fun tellServerRemoved(postIds: List<String>) {
        val deviceId = lock.withLock { file.deviceId }.ifBlank { return }
        postIds.forEach { remote.remove(it, deviceId) }
        if (lock.withLock { file.copies.isEmpty() }) scheduler.cancel()
    }

    // ── Expiry and the server's word ────────────────────────────────────

    override suspend fun refresh(force: Boolean) {
        val (due, deviceId) = lock.withLock {
            load()
            guardOwner()
            sweepExpired()
            publish()
            val nowMs = now()
            visibleCopies().filter { force || recheckDue(it, nowMs) }.map { it.postId } to file.deviceId
        }
        if (due.isEmpty() || deviceId.isBlank()) return
        // NO ANSWER IS NOT A REVOCATION: a check that could not be made changes nothing.
        val answers = (remote.check(deviceId, due) as? AppResult.Success)?.data ?: return
        val removed = lock.withLock {
            val nowMs = now()
            val gone = mutableListOf<OfflineCopy>()
            for (postId in due) {
                val copy = file.copies.firstOrNull { it.postId == postId } ?: continue
                when (val verdict = offlineVerdict(copy, nowMs, answers[postId])) {
                    OfflineVerdict.Keep -> Unit
                    is OfflineVerdict.Confirmed ->
                        replace(copy.copy(expiresAtMs = verdict.expiresAtMs, lastCheckedAtMs = nowMs))
                    is OfflineVerdict.Delete -> {
                        removeLocal(copy)
                        gone += copy
                    }
                }
            }
            persist()
            publish()
            gone
        }
        sayRemoved(removed.size)
        reconcile(deviceId)
    }

    /**
     * Copies past their expiry go, with no network and no question asked.
     * Call under [lock].
     */
    private suspend fun sweepExpired() {
        val nowMs = now()
        val expired = file.copies.filter { offlineVerdict(it, nowMs, answer = null) is OfflineVerdict.Delete }
        if (expired.isEmpty()) return
        expired.forEach { removeLocal(it) }
        persist()
        sayRemoved(expired.size)
    }

    /**
     * Gives back the copies the server still holds for this device that the
     * device no longer has: a remove made with no network, a save that
     * failed after its grant. They count against the viewer's hundred until
     * they are. A save whose grant is on the wire is left alone.
     */
    private suspend fun reconcile(deviceId: String) {
        val held = (remote.held(deviceId) as? AppResult.Success)?.data ?: return
        val here = lock.withLock { file.copies.map { it.postId }.toSet() + requesting }
        (held - here).forEach { remote.remove(it, deviceId) }
        if (here.isEmpty()) scheduler.cancel()
    }

    private fun sayRemoved(count: Int) {
        if (count > 0) _notices.tryEmit(offlineRemovedNotice(count))
    }

    // ── Reading ─────────────────────────────────────────────────────────

    override suspend fun setWifiOnly(enabled: Boolean) = prefs.setWifiOnly(enabled)

    override fun playable(postId: String): OfflineCopy? =
        _state.value.copies[postId]
            ?.takeIf { it.phase == OfflinePhase.STORED }
            ?.copy
            ?.takeIf { it.isPlayable(now()) }

    /** This viewer's copies; none while the index belongs to someone else or nobody is signed in. */
    private fun visibleCopies(): List<OfflineCopy> {
        val viewer = viewerId()
        return if (viewer.isNotBlank() && file.ownerId == viewer) file.copies else emptyList()
    }

    private fun replace(copy: OfflineCopy) {
        file = file.copy(copies = file.copies.map { if (it.postId == copy.postId) copy else it })
    }

    private suspend fun persist() {
        val snapshot = file
        withContext(io) { index.write(snapshot) }
    }

    /** The index and the fetches, as the one value every screen reads. Call under [lock]. */
    private fun publish() {
        val fetches = store.fetches.value
        val entries = LinkedHashMap<String, OfflineEntry>()
        var used = 0L
        for (copy in visibleCopies()) {
            if (copy.stored) {
                entries[copy.postId] = OfflineEntry(OfflinePhase.STORED, progress = 1f, copy = copy)
                used += copy.sizeBytes
                continue
            }
            used += copy.streams.sumOf { fetches[it.key]?.bytes ?: 0L }
            val summary = summarizeFetches(copy.streams, fetches) as? OfflineFetchSummary.Running
            entries[copy.postId] = OfflineEntry(
                phase = if (summary?.waiting == true) OfflinePhase.WAITING else OfflinePhase.SAVING,
                progress = summary?.progress,
                copy = copy,
            )
        }
        requesting.forEach { postId -> entries.getOrPut(postId) { OfflineEntry(OfflinePhase.REQUESTING) } }
        _state.value = OfflineState(copies = entries, usedBytes = used, loaded = loaded)
    }

    private companion object {
        const val NOTICE_BUFFER = 4
        const val POSTER_FILE = "poster"

        /** Sign-out is not held up by a slow network for longer than this. */
        const val SIGN_OUT_REMOVE_MILLIS = 5_000L
        const val SIGN_IN_FIRST = "Sign in to save videos offline."
        const val NO_ROOM = "There isn't enough space on this device to save this offline."
    }
}

/** One line for copies that went on their own. */
fun offlineRemovedNotice(count: Int): String =
    if (count == 1) {
        "An offline copy was removed because it is no longer available."
    } else {
        "$count offline copies were removed because they are no longer available."
    }

/** The stored caption file's name for a language: letters and digits only, whatever the server sent. */
internal fun captionFileName(language: String): String =
    "captions_" + language.filter { it.isLetterOrDigit() }.ifEmpty { "track" } + ".vtt"
