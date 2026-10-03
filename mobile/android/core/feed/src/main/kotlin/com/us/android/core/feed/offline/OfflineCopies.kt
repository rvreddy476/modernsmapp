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
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

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
     * [force]. No answer changes nothing. Copies the server confirmed are
     * then renewed, each at most once a day.
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
 * A stored copy lasts thirty days from its grant, and is RENEWED while the
 * device is online: after a check the server answered, the grant is
 * repeated for each copy it confirmed ([renew]), so a copy only runs out on
 * a phone that stayed offline for the whole thirty days.
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
 * Copies belong to the account that saved them, and nobody else on this
 * device ever lists, plays or counts them: the index keeps each account's
 * copies apart ([settleOwner]) and every stream's key carries its owner.
 *
 * founder, 2026-10-02: sign-out KEEPS the copies for 48 hours
 * ([holdForSignOut]). They are stamped with the time and shown to nobody.
 * The same account signing back in inside the 48 hours finds them again;
 * past it they are deleted, bytes and rows, the next time the app runs
 * (on start, in the background check, and when any account signs in). A
 * session that ended without the sign-out path (a token that expired) is
 * stamped the moment it is noticed.
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
    /** The signed-in viewer's id; blank when nobody is signed in; null while the session is not known yet. */
    private val viewerId: () -> String?,
    /** [viewerId] as it changes: a sign-in, a sign-out, a session that ended on its own. */
    private val viewerChanges: () -> Flow<String?>,
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
        viewerId = { session.get().sessionState.value.offlineViewerId() },
        viewerChanges = { session.get().sessionState.map { it.offlineViewerId() }.distinctUntilChanged() },
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

    /** Guards [file], [requesting], [loaded], [owed] and [emptied]. */
    private val lock = Mutex()
    private var file = OfflineIndexFile()
    private val requesting = mutableSetOf<String>()
    private var loaded = false

    /** Copies deleted after their 48 hours that the server has yet to be told of: post id to device id. */
    private val owed = mutableListOf<Pair<String, String>>()

    /** The last held copy was just deleted: there is nothing left to wake up for. */
    private var emptied = false

    // ── Start ───────────────────────────────────────────────────────────

    /**
     * The application started: read the index, delete what expired while
     * the app was closed and what a signed-out account left more than 48
     * hours ago, pick unfinished saves back up, and ask the server about
     * the rest once a network is there. Off the cold-start path: it returns
     * at once and works on [scope]. The same pass runs again whenever the
     * signed-in account changes ([load]).
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
            settleOwner()
            sweepExpired()
            publish()
            visibleCopies().filterNot { it.stored }
        }
        if (unfinished.isNotEmpty()) {
            store.setWifiOnly(prefs.wifiOnly.first())
            unfinished.forEach { fetchStreams(it) }
        }
        // Held copies need the background check too: it is what deletes them when their 48 hours are up.
        if (lock.withLock { holdsAnything() }) scheduler.schedule()
        refresh()
    }

    override suspend fun ensureLoaded() {
        lock.withLock {
            if (loaded) return
            load()
            publish()
        }
    }

    /** Reads the index once and starts the three collections that keep the state current. Call under [lock]. */
    private suspend fun load() {
        if (loaded) return
        file = withContext(io) { index.read() }
        loaded = true
        scope.launch { store.fetches.collect { onFetches(it) } }
        scope.launch { prefs.wifiOnly.collect { store.setWifiOnly(it) } }
        // A sign-in, a sign-out, a session that ended on its own: the copies are settled for whoever is
        // there now, without waiting for the next launch.
        var seen = viewerId()
        scope.launch {
            viewerChanges().collect { viewer ->
                if (viewer != seen) {
                    seen = viewer
                    runCatching { resumeAndCheck() }
                }
            }
        }
    }

    /**
     * Makes the index agree with who is signed in. Call under [lock].
     *
     *  - Copies held for 48 hours since their owner signed out are deleted,
     *    whoever is signed in now ([purgeHeld]).
     *  - Nobody signed in, and copies with no stamp: the session ended
     *    without the sign-out path. They are stamped now.
     *  - Their owner is signed in: the stamp comes off, and they are theirs
     *    again.
     *  - Someone else is signed in: the copies in front go behind, stamped,
     *    and this viewer's own held copies (if any) come to the front.
     *
     * Nothing is decided while the session is not known yet.
     */
    private suspend fun settleOwner() {
        val viewer = viewerId() ?: return
        val nowMs = now()
        var changed = purgeHeld(viewer, nowMs)
        when {
            viewer.isBlank() -> if (file.copies.isNotEmpty() && file.signedOutAtMs == null) {
                file = file.copy(signedOutAtMs = nowMs)
                changed = true
            }
            file.ownerId == viewer -> if (file.signedOutAtMs != null) {
                file = file.copy(signedOutAtMs = null)
                changed = true
            }
            else -> changed = bringForward(viewer, nowMs) || changed
        }
        if (changed) persist()
    }

    /**
     * Puts the copies in front behind (stamped, if they were not), and
     * [viewer]'s own held copies in front. True when the index changed.
     * Call under [lock].
     */
    private fun bringForward(viewer: String, nowMs: Long): Boolean {
        val behind = file.held.toMutableList()
        val parked = file.copies.isNotEmpty()
        if (parked) {
            behind += OfflineHeldSet(
                ownerId = file.ownerId,
                deviceId = file.deviceId,
                signedOutAtMs = file.signedOutAtMs ?: nowMs,
                copies = file.copies,
            )
        }
        val mine = behind.firstOrNull { it.ownerId == viewer }
        if (mine != null) behind -= mine
        file = OfflineIndexFile(
            ownerId = viewer,
            deviceId = mine?.deviceId.orEmpty(),
            copies = mine?.copies.orEmpty(),
            held = behind,
        )
        return parked || mine != null
    }

    /**
     * Deletes every set of copies whose owner signed out 48 hours ago or
     * more: bytes and rows. The server is owed a DELETE for each only when
     * the signed-in viewer IS that owner; a DELETE is the caller's own, so
     * under anyone else's session it could only hit the wrong account's
     * copy, and with no session the rows age out on the server. True when
     * the index changed. Call under [lock].
     */
    private suspend fun purgeHeld(viewer: String, nowMs: Long): Boolean {
        var changed = false
        val stamp = file.signedOutAtMs
        if (stamp != null && signOutHoldOver(stamp, nowMs)) {
            owe(viewer, file.ownerId, file.deviceId, file.copies)
            file.copies.forEach { deleteBytes(it) }
            file = file.copy(copies = emptyList(), signedOutAtMs = null)
            changed = true
        }
        val over = file.held.filter { signOutHoldOver(it.signedOutAtMs, nowMs) }
        if (over.isNotEmpty()) {
            over.forEach { set ->
                owe(viewer, set.ownerId, set.deviceId, set.copies)
                set.copies.forEach { deleteBytes(it) }
            }
            file = file.copy(held = file.held - over.toSet())
            changed = true
        }
        if (changed && !holdsAnything()) {
            // Nothing is left for anyone: take whatever a failed delete may have left behind as well.
            runCatching { store.removeAll() }
            emptied = true
        }
        return changed
    }

    private fun owe(viewer: String, ownerId: String, deviceId: String, copies: List<OfflineCopy>) {
        if (viewer.isBlank() || viewer != ownerId || deviceId.isBlank()) return
        owed += copies.map { it.postId to deviceId }
    }

    /**
     * Tells the server of the copies [purgeHeld] deleted for the signed-in
     * viewer. Best effort; one the viewer has since saved again is left
     * alone. Never called under [lock].
     */
    private suspend fun giveBackOwed() {
        val (due, cancel) = lock.withLock {
            val due = owed.toList()
            owed.clear()
            val cancel = emptied && !holdsAnything() && requesting.isEmpty()
            emptied = false
            due to cancel
        }
        if (cancel) scheduler.cancel()
        for ((postId, deviceId) in due) {
            val savedAgain = lock.withLock { postId in requesting || visibleCopies().any { it.postId == postId } }
            if (!savedAgain) remote.remove(postId, deviceId)
        }
    }

    /** A copy for this viewer, or one held for somebody else, is on the device. Call under [lock]. */
    private fun holdsAnything(): Boolean = file.copies.isNotEmpty() || file.held.isNotEmpty()

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
        val viewer = viewerId().orEmpty()
        if (viewer.isBlank()) return OfflineSaveResult.Refused(SIGN_IN_FIRST)
        val postId = item.id
        lock.withLock {
            load()
            settleOwner()
            // The account changed between the tap and here: the copies in front are not this viewer's.
            if (file.ownerId != viewer) return OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE)
            // Already saving or stored: the same wish, already granted.
            if (postId in requesting || file.copies.any { it.postId == postId }) return OfflineSaveResult.Started
            requesting += postId
            publish()
        }
        // Before the grant, so a DELETE owed for an old copy of this post cannot land after it.
        giveBackOwed()
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
        val copy = grant.toCopy(item, now(), viewer)
        val wifiOnly = prefs.wifiOnly.first()
        val kept = lock.withLock {
            // Signed out, or someone else signed in, while the grant was on the wire: it is nobody's copy now.
            // (Sign-out forgets the saves in flight, which is how one is known here.)
            if (item.id !in requesting || !isFront(viewer)) return@withLock false
            file = file.copy(deviceId = deviceId, copies = file.copies + copy)
            persist()
            publish()
            true
        }
        if (!kept) {
            remote.remove(item.id, deviceId)
            return OfflineSaveResult.Refused(COULD_NOT_SAVE_OFFLINE)
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
            // Removed while the files were on their way: take them back out. Unless the copy only went
            // behind (its owner signed out), in which case its files stay with it.
            val current = file.copies.firstOrNull { it.postId == copy.postId && it.video.key == copy.video.key }
            if (current == null) {
                val heldNow = file.held.any { set -> set.copies.any { it.video.key == copy.video.key } }
                if (!heldNow) store.removeFiles(copy.folder)
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
                settleOwner()
                // Only ever this viewer's own copy: one held for another account has the same post id.
                val copy = visibleCopies().firstOrNull { it.postId == postId } ?: return@withLock false
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
                settleOwner()
                val mine = visibleCopies()
                if (mine.isEmpty()) return@withLock emptyList()
                // Everything in one sweep when it is all this viewer's; copy by copy when another account's
                // copies are held here too, so theirs are not touched.
                if (file.held.isEmpty()) store.removeAll() else mine.forEach { deleteBytes(it) }
                file = file.copy(copies = emptyList())
                persist()
                publish()
                mine.map { it.postId }
            }
            tellServerRemoved(ids)
        }.await()
    }

    /**
     * Sign-out (founder, 2026-10-02). The stored copies STAY on the device,
     * stamped with the time and hidden from everyone from this moment: the
     * same account signing back in within 48 hours finds them again, and
     * past that they are deleted the next time the app runs ([purgeHeld]).
     * Nothing is said to the server for a copy that is kept.
     *
     * A save still in flight is not a copy yet: it is cancelled, its bytes
     * go, and the server is told while the session is still valid, for as
     * long as [SIGN_OUT_REMOVE_MILLIS] allows.
     */
    suspend fun holdForSignOut() {
        val (cancelled, deviceId, nothingKept) = lock.withLock {
            // Read whatever is on disk, whoever it belongs to.
            if (!loaded) load()
            requesting.clear()
            val unfinished = file.copies.filterNot { it.stored }
            unfinished.forEach { copy ->
                runCatching { deleteBytes(copy) }
                file = file.copy(copies = file.copies - copy)
            }
            if (file.copies.isNotEmpty() && file.signedOutAtMs == null) file = file.copy(signedOutAtMs = now())
            // A stamp that could not be written is put back on the next start: nobody is signed in by then.
            runCatching { persist() }
            publish()
            Triple(unfinished.map { it.postId }, file.deviceId, !holdsAnything())
        }
        // The background check stays while anything is held: it is what deletes the copies after 48 hours.
        if (nothingKept) scheduler.cancel()
        if (cancelled.isEmpty() || deviceId.isBlank()) return
        withTimeoutOrNull(SIGN_OUT_REMOVE_MILLIS) {
            cancelled.forEach { remote.remove(it, deviceId) }
        }
    }

    /** One copy's bytes and files, and its row. Call under [lock]; the caller persists. */
    private suspend fun removeLocal(copy: OfflineCopy) {
        deleteBytes(copy)
        file = file.copy(copies = file.copies.filterNot { it.postId == copy.postId })
    }

    /** One copy's streams and small files, whichever account it belongs to. Call under [lock]. */
    private suspend fun deleteBytes(copy: OfflineCopy) {
        copy.streams.forEach { store.remove(it.key) }
        store.removeFiles(copy.folder)
    }

    /** Best effort: a remove the server never hears of is found again by [reconcile], or ages out. */
    private suspend fun tellServerRemoved(postIds: List<String>) {
        val deviceId = lock.withLock { file.deviceId }.ifBlank { return }
        postIds.forEach { remote.remove(it, deviceId) }
        if (lock.withLock { !holdsAnything() }) scheduler.cancel()
    }

    // ── Expiry and the server's word ────────────────────────────────────

    override suspend fun refresh(force: Boolean) {
        val (viewer, due, deviceId) = lock.withLock {
            load()
            settleOwner()
            sweepExpired()
            publish()
            val nowMs = now()
            val due = visibleCopies().filter { force || recheckDue(it, nowMs) }.map { it.postId }
            Triple(viewerId().orEmpty(), due, file.deviceId)
        }
        giveBackOwed()
        if (due.isEmpty() || deviceId.isBlank()) return
        // NO ANSWER IS NOT A REVOCATION: a check that could not be made changes nothing.
        val answers = (remote.check(deviceId, due) as? AppResult.Success)?.data ?: return
        val renewable = mutableListOf<String>()
        val removed = lock.withLock {
            // The account changed while the server was being asked: these answers are about someone else's copies.
            if (!isFront(viewer)) return
            val nowMs = now()
            val gone = mutableListOf<OfflineCopy>()
            for (postId in due) {
                val copy = file.copies.firstOrNull { it.postId == postId } ?: continue
                val answer = answers[postId]
                when (val verdict = offlineVerdict(copy, nowMs, answer)) {
                    OfflineVerdict.Keep -> Unit
                    is OfflineVerdict.Confirmed -> {
                        replace(copy.copy(expiresAtMs = verdict.expiresAtMs, lastCheckedAtMs = nowMs))
                        // Only a copy the server just confirmed, and did not say it would refuse.
                        if ((answer as? OfflineCheckAnswer.Valid)?.renewable == true) renewable += postId
                    }
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
        reconcile(viewer, deviceId)
        renew(viewer, deviceId, renewable)
    }

    /**
     * Renews copies while the device is online (founder, 2026-10-02): the
     * grant is repeated for each of [postIds], which the server has just
     * confirmed, and its new expiry is stored.
     *
     *  - At most once per copy in 24 hours ([renewDue]). The try is written
     *    down BEFORE the call, so two checks at once, or a process that dies
     *    mid-way, cannot make it twice.
     *  - One at a time, [RENEW_GAP_MILLIS] apart: a hundred copies must not
     *    arrive at the server as a burst.
     *  - A renewal that fails changes nothing. Refused (403, 404), over the
     *    limit (409) or no network: the copy stays exactly as it was, and
     *    only a check ([offlineVerdict]) or its own expiry can delete it.
     *  - No video bytes move, so "Save on Wi-Fi only" does not apply.
     */
    private suspend fun renew(viewer: String, deviceId: String, postIds: List<String>) {
        var renewed = 0
        for (postId in postIds) {
            val claimed = lock.withLock {
                val nowMs = now()
                val copy = frontCopy(viewer, postId)?.takeIf { renewDue(it, nowMs) } ?: return@withLock false
                replace(copy.copy(lastRenewAtMs = nowMs))
                persist()
                true
            }
            if (!claimed) continue
            if (renewed++ > 0) delay(RENEW_GAP_MILLIS)
            val grant = (remote.grant(postId, deviceId, now()) as? AppResult.Success)?.data ?: continue
            lock.withLock {
                val copy = frontCopy(viewer, postId) ?: return@withLock
                replace(
                    copy.copy(
                        expiresAtMs = grant.expiresAtMs,
                        recheckAfterSeconds = grant.recheckAfterSeconds,
                        // The grant IS the server's word that the copy may be kept.
                        lastCheckedAtMs = now(),
                    ),
                )
                persist()
                publish()
            }
        }
    }

    /** [viewer] is still signed in and the copies in front are still theirs. Call under [lock]. */
    private fun isFront(viewer: String): Boolean =
        viewer.isNotBlank() && viewerId() == viewer && file.ownerId == viewer && file.signedOutAtMs == null

    /** [viewer]'s copy of [postId], while they are still the one signed in. Call under [lock]. */
    private fun frontCopy(viewer: String, postId: String): OfflineCopy? =
        if (isFront(viewer)) file.copies.firstOrNull { it.postId == postId } else null

    /**
     * Copies past their expiry go, with no network and no question asked:
     * the viewer's own, and the ones held for an account that signed out
     * (being held never outlasts the grant). Call under [lock].
     */
    private suspend fun sweepExpired() {
        val nowMs = now()
        val heldAfter = file.held.map { set ->
            val (gone, kept) = set.copies.partition { it.isExpired(nowMs) }
            gone.forEach { deleteBytes(it) }
            set.copy(copies = kept)
        }.filter { it.copies.isNotEmpty() }
        val heldChanged = heldAfter != file.held
        if (heldChanged) file = file.copy(held = heldAfter)
        val expired = file.copies.filter { it.isExpired(nowMs) }
        if (expired.isEmpty() && !heldChanged) return
        expired.forEach { removeLocal(it) }
        persist()
        // Said only to the viewer whose copies they were.
        if (file.signedOutAtMs == null) sayRemoved(expired.size)
    }

    private fun OfflineCopy.isExpired(nowMs: Long): Boolean =
        offlineVerdict(this, nowMs, answer = null) is OfflineVerdict.Delete

    /**
     * Gives back the copies the server still holds for this device that the
     * device no longer has: a remove made with no network, a save that
     * failed after its grant. They count against the viewer's hundred until
     * they are. A save whose grant is on the wire is left alone.
     */
    private suspend fun reconcile(viewer: String, deviceId: String) {
        val held = (remote.held(deviceId) as? AppResult.Success)?.data ?: return
        val (here, nothingKept) = lock.withLock {
            // The list was the viewer's; it is compared with the viewer's copies or with nothing.
            if (!isFront(viewer)) return
            (file.copies.map { it.postId }.toSet() + requesting) to !holdsAnything()
        }
        (held - here).forEach { remote.remove(it, deviceId) }
        if (here.isEmpty() && nothingKept) scheduler.cancel()
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

    /**
     * This viewer's copies; none while the copies in front belong to someone
     * else, nobody is signed in, or their owner has signed out and they are
     * only being held. Copies held behind ([OfflineIndexFile.held]) are
     * never returned to anybody.
     */
    private fun visibleCopies(): List<OfflineCopy> {
        val viewer = viewerId().orEmpty()
        return if (isFront(viewer)) file.copies else emptyList()
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

        /** Between one renewal and the next. */
        const val RENEW_GAP_MILLIS = 1_000L
        const val SIGN_IN_FIRST = "Sign in to save videos offline."
        const val NO_ROOM = "There isn't enough space on this device to save this offline."
    }
}

/**
 * Who the offline copies are for, read from the session: the user's id when
 * there is a real session; blank when there is none (signed out, or part-way
 * through signing in); null while the stored credentials are still being
 * read, which decides nothing.
 */
internal fun SessionState.offlineViewerId(): String? = when (this) {
    SessionState.Unknown -> null
    is SessionState.Authenticated -> userId
    else -> ""
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
