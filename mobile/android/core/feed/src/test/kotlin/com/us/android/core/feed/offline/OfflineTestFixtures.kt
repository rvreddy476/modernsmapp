package com.us.android.core.feed.offline

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.OfflinePrefs
import com.us.android.core.media.offline.OfflineAsset
import com.us.android.core.media.offline.OfflineFetch
import com.us.android.core.media.offline.OfflineFetchState
import com.us.android.core.media.offline.OfflineMediaStore
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.ReelSound
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import java.io.File

/*
 * What the offline tests share: a post, a grant, and hand-written fakes of
 * the three seams the copy state machine is built on (the server, the bytes,
 * the preferences). Each fake keeps what it was asked, in order, so a test
 * reads as "this happened, then the machine did that".
 */

internal const val VIEWER = "viewer-1"
internal const val DEVICE = "device-1"
internal const val DAY_MS = 24L * 60 * 60 * 1000

/** A ready video post. */
@Suppress("LongParameterList")
internal fun videoPost(
    id: String = "p1",
    authorId: String = "creator-1",
    allowDownload: Boolean = true,
    contentType: String = "long_video",
    processing: Boolean = false,
    scheduled: Boolean = false,
    sound: ReelSound? = null,
    kind: String = "video",
) = FeedItem(
    id = id,
    authorId = authorId,
    author = FeedAuthor(id = authorId, displayName = "Raghu", username = "raghu", avatarMediaId = "av1"),
    text = "caption of $id",
    title = "Title of $id",
    visibility = "public",
    feedContentType = contentType,
    postType = kind,
    createdAt = "2026-10-01T10:00:00Z",
    isPinned = false,
    media = listOf(
        FeedMedia(mediaId = "m-$id", kind = kind, status = "ready", width = 1280, height = 720, durationMs = 725_000L),
    ),
    counts = FeedCounts(likes = 3, comments = 1, reposts = 0, views = 40),
    viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
    isRepostable = true,
    isProcessing = processing,
    isScheduled = scheduled,
    controls = FeedPostControls(allowDownload = allowDownload),
    hashtags = listOf("build"),
    sound = sound,
)

internal fun reelSound(id: String = "s1") = ReelSound(
    id = id,
    title = "Monsoon",
    artist = "Raghu",
    startMs = 1_500L,
    durationMs = 28_400L,
    useCount = 2,
    sourcePostId = null,
    creatorUserId = null,
)

/** What the server grants for [postId]: a 1000-byte video expiring thirty days after [nowMs]. */
@Suppress("LongParameterList")
internal fun grant(
    postId: String = "p1",
    nowMs: Long = 0L,
    sizeBytes: Long = 1_000L,
    kind: OfflineKind? = OfflineKind.VIDEO,
    sound: OfflineGrantSound? = null,
    captions: List<OfflineGrantCaption> = emptyList(),
    posterUrl: String? = "https://api.test/v1/media/poster-$postId/serve",
) = OfflineGrant(
    postId = postId,
    kind = kind,
    expiresAtMs = nowMs + 30 * DAY_MS,
    recheckAfterSeconds = 172_800L,
    title = "Granted $postId",
    channelName = "Raghu Builds",
    durationMs = 725_000L,
    posterUrl = posterUrl,
    video = OfflineGrantStream("https://api.test/v1/media/m-$postId/serve/720p", "video/mp4", sizeBytes),
    captions = captions,
    sound = sound,
)

/** The server, scripted. */
internal class FakeRemote : OfflineRemote {
    /** What a grant answers, by post; a post not here is granted the default [grant]. */
    val grants = mutableMapOf<String, AppResult<OfflineGrant>>()

    /** What the next check answers; null is "the check could not be made". */
    var checkAnswers: Map<String, OfflineCheckAnswer>? = emptyMap()

    /** The copies the server says it holds for the device; null is "the list could not be read". */
    var held: List<String>? = emptyList()

    /** Removes fail (no network) while true. */
    var removeFails = false

    /** Every call in the order it reached the server. */
    val calls = mutableListOf<String>()

    /** Runs as a grant is answered, before the machine sees it. */
    var onGrant: suspend () -> Unit = {}

    /** Runs as a check is answered, before the machine sees it. */
    var onCheck: suspend () -> Unit = {}

    override suspend fun grant(postId: String, deviceId: String, nowMs: Long): AppResult<OfflineGrant> {
        calls += "grant:$postId:$deviceId"
        onGrant()
        return grants[postId] ?: AppResult.Success(grant(postId, nowMs))
    }

    override suspend fun check(
        deviceId: String,
        postIds: List<String>,
    ): AppResult<Map<String, OfflineCheckAnswer>> {
        calls += "check:$deviceId:${postIds.sorted().joinToString(",")}"
        onCheck()
        return checkAnswers?.let { AppResult.Success(it) } ?: AppResult.Failure(AppError.NoNetwork())
    }

    override suspend fun held(deviceId: String): AppResult<List<String>> {
        calls += "held:$deviceId"
        return held?.let { AppResult.Success(it) } ?: AppResult.Failure(AppError.NoNetwork())
    }

    override suspend fun remove(postId: String, deviceId: String): AppResult<Unit> {
        calls += "remove:$postId:$deviceId"
        return if (removeFails) AppResult.Failure(AppError.NoNetwork()) else AppResult.Success(Unit)
    }

    fun removes(): List<String> = calls.filter { it.startsWith("remove:") }
}

/** The bytes, scripted: a test moves a fetch along and says what is "on the device". */
internal class FakeMediaStore : OfflineMediaStore {
    private val _fetches = MutableStateFlow<Map<String, OfflineFetch>>(emptyMap())
    override val fetches: StateFlow<Map<String, OfflineFetch>> = _fetches

    val asked = mutableListOf<OfflineAsset>()
    val removed = mutableListOf<String>()
    val stored = mutableMapOf<String, Long>()
    val declared = mutableMapOf<String, Long>()
    val files = mutableListOf<String>()
    var wifiOnly: Boolean? = null
    var usable: Long = 10L * 1024 * 1024 * 1024
    var removedAll = 0

    /** The storage cannot be opened: every call that touches the bytes throws. */
    var broken = false

    override suspend fun fetch(asset: OfflineAsset) {
        check(!broken) { "storage cannot be opened" }
        asked += asset
        _fetches.update { it + (asset.key to OfflineFetch(OfflineFetchState.QUEUED)) }
    }

    fun running(key: String, bytes: Long, total: Long) {
        _fetches.update { it + (key to OfflineFetch(OfflineFetchState.RUNNING, bytes, total)) }
    }

    fun waiting(key: String) {
        _fetches.update { it + (key to OfflineFetch(OfflineFetchState.WAITING)) }
    }

    /** The fetch finished and [bytes] are on the device. */
    fun done(key: String, bytes: Long) {
        stored[key] = bytes
        _fetches.update { it + (key to OfflineFetch(OfflineFetchState.DONE, bytes, bytes)) }
    }

    fun failed(key: String) {
        _fetches.update { it + (key to OfflineFetch(OfflineFetchState.FAILED)) }
    }

    override suspend fun remove(key: String) {
        removed += key
        stored -= key
        _fetches.update { it - key }
    }

    override suspend fun removeAll() {
        check(!broken) { "storage cannot be opened" }
        removedAll += 1
        stored.clear()
        files.clear()
        _fetches.value = emptyMap()
    }

    override suspend fun storedBytes(key: String): Long = stored[key] ?: 0L

    override suspend fun declaredBytes(key: String): Long = declared[key] ?: OfflineFetch.UNKNOWN_LENGTH

    override suspend fun setWifiOnly(wifiOnly: Boolean) {
        this.wifiOnly = wifiOnly
    }

    override suspend fun fetchFile(url: String, folder: String, name: String): File? {
        files += "$folder/$name"
        return File("/private/no_backup/offline_copies/files/$folder/$name")
    }

    override suspend fun removeFiles(folder: String) {
        files.removeAll { it.startsWith("$folder/") }
    }

    override suspend fun usedBytes(): Long = stored.values.sum()

    override fun usableBytes(): Long = usable
}

internal class FakePrefs(wifiOnly: Boolean = true, private val device: String = DEVICE) : OfflinePrefs {
    override val wifiOnly = MutableStateFlow(wifiOnly)

    override suspend fun setWifiOnly(enabled: Boolean) {
        wifiOnly.value = enabled
    }

    override suspend fun deviceId(): String = device
}

internal class FakeScheduler : OfflineCheckScheduler {
    var scheduled = 0
    var cancelled = 0

    override fun schedule() {
        scheduled += 1
    }

    override fun cancel() {
        cancelled += 1
    }
}
