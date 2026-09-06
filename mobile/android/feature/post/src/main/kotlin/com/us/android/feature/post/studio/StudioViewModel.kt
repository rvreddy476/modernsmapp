package com.us.android.feature.post.studio

import android.content.Context
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import androidx.work.WorkInfo
import androidx.work.WorkManager
import com.us.android.core.creator.engine.LegacyRecoveryRetrier
import com.us.android.core.creator.engine.ProjectStore
import com.us.android.core.creator.engine.SourceVault
import com.us.android.core.creator.model.Accessibility
import com.us.android.core.creator.model.Adjustments
import com.us.android.core.creator.model.AndroidCreatorProject
import com.us.android.core.creator.model.Canvas
import com.us.android.core.creator.model.CreatorCommand
import com.us.android.core.creator.model.CreatorReducer
import com.us.android.core.creator.model.Crop
import com.us.android.core.creator.model.EditSession
import com.us.android.core.creator.model.LayerText
import com.us.android.core.creator.model.PostText
import com.us.android.core.creator.model.ReduceResult
import com.us.android.core.creator.model.SafeZone
import com.us.android.core.creator.model.SourceAsset
import com.us.android.core.creator.model.TextStyle
import com.us.android.core.database.CreatorLegacyRecoveryEntity
import com.us.android.core.media.creator.CreatorFonts
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.ui.photoeditor.PhotoEditor
import com.us.android.feature.post.navigation.StudioRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.File
import java.security.SecureRandom
import javax.inject.Inject

/**
 * The Post Studio — a multi-page editor over one [EditSession].
 *
 * ## HOW STATE FLOWS
 *
 * Every edit is a [CreatorCommand] through the pure reducer; this ViewModel
 * owns nothing the document does not. Autosave debounces each applied command
 * into [ProjectStore] (durable within the frozen 250 ms budget of the last
 * mutation), so process death loses at most the un-debounced tail — and the
 * command history means undo survives within the session, while the document
 * survives across them.
 *
 * ## PUBLISHING
 *
 * Publish enqueues [PublishWorker] and OBSERVES it. The ViewModel never
 * publishes in-process: a screen rotation or a swipe-away must not kill a
 * half-uploaded carousel, and the worker's checkpoints make its restarts safe.
 */
// TooManyFunctions: the studio's whole command surface — every function is one
// user gesture mapped to one reducer command or one step transition. Splitting
// by tool would scatter a single edit session across files.
@Suppress("TooManyFunctions")
@HiltViewModel
class StudioViewModel @Inject constructor(
    @ApplicationContext private val appContext: Context,
    savedStateHandle: SavedStateHandle,
    private val vault: SourceVault,
    private val store: ProjectStore,
    private val recoveryRetrier: LegacyRecoveryRetrier,
    private val photoEditor: PhotoEditor,
    private val tracker: ReelPublishTracker,
) : ViewModel() {

    /**
     * Picker results handed over by the Create hub, imported exactly once —
     * the flag survives process death alongside the route arguments, so a
     * restored studio does not import the same photos twice.
     */
    private val initialUris: List<String> =
        if (savedStateHandle.get<Boolean>(KEY_URIS_IMPORTED) == true) {
            emptyList()
        } else {
            savedStateHandle[KEY_URIS_IMPORTED] = true
            runCatching { savedStateHandle.toRoute<StudioRoute>().initialUris }
                .getOrDefault(emptyList())
        }

    /**
     * These pictures came out of the advanced editor already edited.
     *
     * Read every time (unlike [initialUris], which is consumed once) because
     * it describes the route, not a pending action.
     */
    private val arrivedEdited: Boolean =
        runCatching { savedStateHandle.toRoute<StudioRoute>().alreadyEdited }.getOrDefault(false)

    /** Which of the two Instagram-style steps is on screen. */
    enum class Step { Edit, Share }

    data class PageUi(
        val pageId: String,
        val assetId: String,
        val sourcePath: String,
        val crop: Crop,
        val adjustments: Adjustments,
        val rotationDegMicros: Int,
        val altText: String,
        val decorative: Boolean,
        val textLayers: List<TextLayerUi>,
        /** Source pixel dimensions — what makes the Ratio crops REAL ratios. */
        val sourceWidthPx: Int = 1,
        val sourceHeightPx: Int = 1,
    ) {
        val altDecided: Boolean get() = decorative || altText.isNotBlank()
    }

    data class TextLayerUi(
        val layerId: String,
        val value: String,
        val fontAssetId: String,
        val colorArgb: String,
        val sizeCanvasMicros: Int,
    )

    sealed interface PublishUi {
        data object Idle : PublishUi
        data object Publishing : PublishUi
        data class Success(val postId: String) : PublishUi
        data class RetryableFailure(val reason: String) : PublishUi
        data class PermanentFailure(val reason: String) : PublishUi
    }

    data class RecoveryUi(
        val recoveryId: String,
        val kind: String,
        val text: String,
        val busy: Boolean = false,
        val message: String? = null,
    )

    data class StudioUiState(
        val loaded: Boolean = false,
        val step: Step = Step.Edit,
        val projectId: String = "",
        val postText: String = "",
        val postLanguage: String = "en",
        val pages: List<PageUi> = emptyList(),
        val selectedPageId: String? = null,
        val canUndo: Boolean = false,
        val canRedo: Boolean = false,
        val publish: PublishUi = PublishUi.Idle,
        /**
         * The publish is the worker's now and this screen is done. The host
         * navigates to the profile once; it never goes back to false, because
         * a studio whose project has left it has nothing more to edit.
         */
        val handedOff: Boolean = false,
        val recoveries: List<RecoveryUi> = emptyList(),
        /** A rejected command or import, surfaced once. */
        val notice: String? = null,
    ) {
        val selectedPage: PageUi? get() = pages.firstOrNull { it.pageId == selectedPageId }
        val decidedCount: Int get() = pages.count { it.altDecided }

        // Alt text is encouraged, never required: the share sheet nudges
        // toward describing photos, but an undescribed photo must not stop a
        // post from existing (founder call, 2026-09-01 — a mandatory
        // description was killing the whole flow).
        val canPublish: Boolean
            get() = pages.isNotEmpty() &&
                publish !is PublishUi.Publishing
    }

    private val _state = MutableStateFlow(StudioUiState())
    val state: StateFlow<StudioUiState> = _state.asStateFlow()

    private var session: EditSession = EditSession(newProject())
    private var autosave: Job? = null
    private var publishWatch: Job? = null

    init {
        viewModelScope.launch { loadOrCreate() }
    }

    // ── Loading ─────────────────────────────────────────────────────────

    private suspend fun loadOrCreate() {
        // Resume the most recent editable project; a published one starts fresh.
        val existing = store.all()
            .firstOrNull { it.status == AndroidCreatorProject.STATUS_EDITING }
        val project = existing?.let {
            (store.load(it.projectId) as? ProjectStore.LoadResult.Loaded)?.project
        } ?: newProject()

        session = EditSession(project)
        val recoveries = recoveryRetrier.pendingRecoveries()
            .filter { it.kind == CreatorLegacyRecoveryEntity.KIND_RETRYABLE_PUBLISH }
            .map { RecoveryUi(it.recoveryId, it.kind, it.text) }
        _state.update {
            fromSession().copy(loaded = true, recoveries = recoveries)
        }
        watchPublish(project.projectId)

        // The Create hub's hand-off: photos the user already picked before the
        // studio opened. Same import path as the in-studio picker.
        if (initialUris.isNotEmpty()) {
            onImagesPicked(initialUris.mapNotNull { runCatching { Uri.parse(it) }.getOrNull() })
        }
    }

    // ── Editing ─────────────────────────────────────────────────────────

    fun onImagesPicked(uris: List<Uri>) {
        viewModelScope.launch {
            var imported = 0
            // Bounded up front rather than checked mid-loop: the cap is a page
            // budget, and taking N slots then importing is one decision.
            val room = MAX_PAGES - session.current.pages.size
            for (uri in uris.take(room)) {
                val assetId = importAsset(uri, origin = "photoPicker") ?: continue
                apply(CreatorCommand.AddPage("p$assetId", "l$assetId", assetId))
                imported++
            }
            if (imported == 0 && uris.isNotEmpty()) {
                _state.update { it.copy(notice = "Those photos couldn't be added.") }
            }
            // Rebuild the whole UI projection: pages, thumbs, selection. The
            // missing refresh here shipped an editor that imported photos into
            // the vault and then showed an empty screen — caught on the
            // emulator, not by a test, which is exactly why item 14 exists.
            refresh()
            _state.update { state ->
                val landed = state.copy(selectedPageId = session.current.pages.lastOrNull()?.pageId)
                // Pictures that arrived already edited go straight to Share.
                // Checked HERE, not at the call site, because the import runs
                // in this coroutine — at the call site the page list is still
                // empty and the step would never move.
                //
                // Someone who has just finished in the advanced editor is not
                // asking to be dropped into a second set of editing tools; that
                // reads as the edit having been discarded. Back from Share
                // still reaches them.
                if (arrivedEdited && landed.pages.isNotEmpty()) landed.copy(step = Step.Share) else landed
            }
        }
    }

    /** Copies [uri] into the vault and imports it as a source asset: the new asset id, or null if unreadable or rejected. */
    private suspend fun importAsset(uri: Uri, origin: String): String? {
        val assetId = "a${newId(ID_CHARS)}"
        val entry = vault.importSource(uri, assetId) ?: return null
        val dims = decodeDims(entry.relativePath)
        val applied = apply(
            CreatorCommand.ImportAsset(
                SourceAsset(
                    assetId = assetId,
                    kind = "image",
                    vaultPath = entry.relativePath,
                    sha256 = entry.sha256,
                    bytes = entry.bytes,
                    mime = "image/jpeg",
                    widthPx = dims.first,
                    heightPx = dims.second,
                    origin = origin,
                ),
            ),
        )
        return assetId.takeIf { applied }
    }

    // ── Advanced photo editor ───────────────────────────────────────────

    /** The `:core:ui` port the screen launches; the studio's Edit pill exists only while it is ready. */
    val advancedEditor: PhotoEditor get() = photoEditor

    /**
     * The advanced editor's export replaces the selected page's photo.
     *
     * The document has no "swap source" command, so the replacement is four
     * legal ones — import the export, remove the old page, add the new one,
     * move it into the old slot — which keeps undo honest and the vault's
     * hashing and dimension probing on the one import path. The page's crop,
     * look, text and alt start over: the edit changed the pixels they were
     * made for.
     */
    fun onSelectedPageEdited(path: String) {
        val pageId = _state.value.selectedPageId ?: return
        viewModelScope.launch {
            val index = session.current.pages.indexOfFirst { it.pageId == pageId }
            val assetId = if (index < 0) null else importAsset(Uri.fromFile(File(path)), origin = "photoEditor")
            if (assetId == null) {
                _state.update { it.copy(notice = "The edited photo couldn't be added.") }
                return@launch
            }
            apply(CreatorCommand.RemovePage(pageId))
            apply(CreatorCommand.AddPage("p$assetId", "l$assetId", assetId))
            apply(CreatorCommand.MovePage("p$assetId", index))
            refresh()
            _state.update { it.copy(selectedPageId = "p$assetId") }
        }
    }

    /** The editor came back with nothing usable; the person reads why on the snackbar. */
    fun onPhotoEditFailed(message: String) = _state.update { it.copy(notice = message) }

    fun onSelectPage(pageId: String) = _state.update { it.copy(selectedPageId = pageId) }

    fun onMovePage(pageId: String, toIndex: Int) {
        apply(CreatorCommand.MovePage(pageId, toIndex))
        refresh()
    }

    /**
     * One drag swap from the thumbnail strip. The library reports adjacent
     * index swaps continuously during a drag; each is a legal [CreatorCommand]
     * so the document (and autosave, and undo) never sees anything but real
     * reorder operations.
     */
    fun onDragMove(fromIndex: Int, toIndex: Int) {
        val pages = _state.value.pages
        val page = pages.getOrNull(fromIndex) ?: return
        if (toIndex !in pages.indices) return
        onMovePage(page.pageId, toIndex)
    }

    // ── Steps ───────────────────────────────────────────────────────────

    /** Edit → Share. Always available with pages; the alt GATE blocks Share, not Next. */
    fun onNext() {
        if (_state.value.pages.isNotEmpty()) _state.update { it.copy(step = Step.Share) }
    }

    fun onBackToEdit() = _state.update { it.copy(step = Step.Edit) }

    /** From the Share screen's accessibility row: land on the first undecided page. */
    fun onJumpToUndecided() {
        val target = _state.value.pages.firstOrNull { !it.altDecided } ?: return
        _state.update { it.copy(step = Step.Edit, selectedPageId = target.pageId) }
    }

    fun onRemovePage(pageId: String) {
        apply(CreatorCommand.RemovePage(pageId))
        _state.update { state ->
            state.copy(selectedPageId = session.current.pages.firstOrNull()?.pageId)
        }
        refresh()
    }

    fun onCrop(crop: Crop) = selected {
        apply(CreatorCommand.SetCrop(it, crop))
        refresh()
    }

    fun onRotateQuarter() = selected { pageId ->
        val current = session.current.pages.first { it.pageId == pageId }
            .layers.filterIsInstance<com.us.android.core.creator.model.ImageLayer>()
            .first().transform.rotationDegMicros
        val next = (current + QUARTER_MICROS) % FULL_TURN_MICROS
        apply(CreatorCommand.SetRotation(pageId, next))
        refresh()
    }

    fun onAdjust(adjustments: Adjustments) = selected {
        apply(CreatorCommand.SetAdjustments(it, adjustments))
        refresh()
    }

    fun onAccessibility(altText: String, decorative: Boolean) = selected { pageId ->
        apply(CreatorCommand.SetAccessibility(pageId, Accessibility(altText.trim(), decorative)))
        refresh()
    }

    fun onPostTextChanged(value: String) {
        apply(CreatorCommand.SetPostText(PostText(value, session.current.postText.language)))
        refresh()
    }

    fun onLanguageChanged(language: String) {
        apply(CreatorCommand.SetPostText(PostText(session.current.postText.value, language)))
        refresh()
    }

    /**
     * A centred crop with the exact target aspect, computed from the SOURCE's
     * own pixel dimensions — which is what makes "Square" genuinely 1:1 rather
     * than a fixed rect that only looks square on one photo shape.
     */
    fun onCropRatio(targetAspect: Float) = selected { pageId ->
        val page = _state.value.pages.first { it.pageId == pageId }
        val sourceAspect = page.sourceWidthPx.toFloat() / page.sourceHeightPx
        val crop = if (sourceAspect > targetAspect) {
            // Source is wider than the target: full height, trimmed width.
            val w = (targetAspect / sourceAspect * MICROS_D).toInt()
            Crop((MICROS_I - w) / 2, 0, w, MICROS_I)
        } else {
            val h = (sourceAspect / targetAspect * MICROS_D).toInt()
            Crop(0, (MICROS_I - h) / 2, MICROS_I, h)
        }
        onCrop(crop)
    }

    fun onAddTextLayer(
        value: String,
        fontAssetId: String,
        colorArgb: String,
        sizeCanvasMicros: Int = DEFAULT_TEXT_SIZE_MICROS,
    ) = selected { pageId ->
        val font = CreatorFonts.ALL.firstOrNull { it.fontAssetId == fontAssetId } ?: return@selected
        apply(
            CreatorCommand.AddTextLayer(
                pageId = pageId,
                layerId = "t${newId(ID_CHARS)}",
                text = LayerText(value, session.current.postText.language),
                style = TextStyle(
                    fontAssetId = font.fontAssetId,
                    fontVersion = font.version,
                    fontSha256 = font.sha256,
                    license = font.license,
                    weight = DEFAULT_TEXT_WEIGHT,
                    sizeCanvasMicros = sizeCanvasMicros,
                    colorArgb = colorArgb,
                    align = "center",
                ),
                transform = CreatorReducer.IDENTITY_TRANSFORM,
            ),
        )
        refresh()
    }

    fun onRemoveTextLayer(layerId: String) = selected { pageId ->
        apply(CreatorCommand.RemoveTextLayer(pageId, layerId))
        refresh()
    }

    fun onUndo() {
        if (session.undo()) refresh()
    }

    fun onRedo() {
        if (session.redo()) refresh()
    }

    /** Reset = undo everything back to the session's base document. */
    fun onReset() {
        while (session.canUndo) session.undo()
        refresh()
    }

    fun onNoticeShown() = _state.update { it.copy(notice = null) }

    // ── Publishing ──────────────────────────────────────────────────────

    /**
     * Hand the post to the worker and GET OUT OF THE WAY.
     *
     * The founder's ask (2026-09-06): pressing Post returns them to their
     * profile, where the upload shows its progress, and lands them on the feed
     * when it finishes. So this no longer parks the viewer on a spinner
     * waiting for [PublishUi.Success] — it puts the publish on the shared
     * queue, where the profile grid draws it, and raises [handedOff] so the
     * screen leaves. Exactly what the reel surface already does.
     *
     * The preview is set BEFORE the worker is enqueued, so the tile is already
     * on the profile when the viewer arrives rather than appearing a beat
     * later when the worker first runs.
     */
    fun onPublish() {
        val current = _state.value
        if (!current.canPublish) return
        viewModelScope.launch {
            // The document must be durable BEFORE the worker starts: the worker
            // reads it from the store, not from this process.
            store.save(session.current, System.currentTimeMillis())
            tracker.setPreview(studioPublishPreview(session.current, vault))
            tracker.update(session.current.projectId, ReelPublishState.Preparing)
            PublishWorker.enqueue(appContext, session.current.projectId)
            _state.update { it.copy(publish = PublishUi.Publishing, handedOff = true) }
        }
    }

    fun onRetryPublish() {
        _state.update { it.copy(publish = PublishUi.Idle) }
        onPublish()
    }

    private fun watchPublish(projectId: String) {
        publishWatch?.cancel()
        publishWatch = viewModelScope.launch {
            WorkManager.getInstance(appContext)
                .getWorkInfosForUniqueWorkFlow(PublishWorker.uniqueName(projectId))
                .collect { infos ->
                    val info = infos.firstOrNull() ?: return@collect
                    _state.update { it.copy(publish = info.toPublishUi()) }
                }
        }
    }

    private fun WorkInfo.toPublishUi(): PublishUi = when (state) {
        WorkInfo.State.ENQUEUED, WorkInfo.State.RUNNING, WorkInfo.State.BLOCKED ->
            PublishUi.Publishing
        WorkInfo.State.SUCCEEDED ->
            PublishUi.Success(outputData.getString(PublishWorker.KEY_POST_ID).orEmpty())
        WorkInfo.State.FAILED ->
            PublishUi.PermanentFailure(
                outputData.getString(PublishWorker.KEY_FAILURE_REASON) ?: "Publishing failed.",
            )
        WorkInfo.State.CANCELLED -> PublishUi.Idle
    }

    // ── Recovery ────────────────────────────────────────────────────────

    fun onRetryRecovery(recoveryId: String) {
        _state.update { state ->
            state.copy(
                recoveries = state.recoveries.map {
                    if (it.recoveryId == recoveryId) it.copy(busy = true) else it
                },
            )
        }
        viewModelScope.launch {
            val result = recoveryRetrier.retry(recoveryId)
            _state.update { state ->
                when (result) {
                    is LegacyRecoveryRetrier.RetryResult.Published ->
                        state.copy(recoveries = state.recoveries.filterNot { it.recoveryId == recoveryId })
                    is LegacyRecoveryRetrier.RetryResult.Retryable ->
                        state.copy(
                            recoveries = state.recoveries.map {
                                if (it.recoveryId == recoveryId) {
                                    it.copy(busy = false, message = "Couldn't reach the server. Try again.")
                                } else {
                                    it
                                }
                            },
                        )
                    is LegacyRecoveryRetrier.RetryResult.Quarantined ->
                        state.copy(
                            recoveries = state.recoveries.map {
                                if (it.recoveryId == recoveryId) {
                                    it.copy(busy = false, message = "This draft can't be finished automatically.")
                                } else {
                                    it
                                }
                            },
                        )
                }
            }
        }
    }

    // ── Internals ───────────────────────────────────────────────────────

    /** Apply a command; a rejection surfaces as a notice rather than vanishing. */
    private fun apply(command: CreatorCommand): Boolean =
        when (val result = session.apply(command)) {
            is ReduceResult.Applied -> {
                scheduleAutosave()
                true
            }
            is ReduceResult.Rejected -> {
                _state.update { it.copy(notice = result.reason) }
                false
            }
        }

    private fun refresh() = _state.update { fromSession().copy(loaded = true) }

    private fun fromSession(): StudioUiState {
        val project = session.current
        val previous = _state.value
        return previous.copy(
            projectId = project.projectId,
            postText = project.postText.value,
            postLanguage = project.postText.language,
            pages = project.pages.map { page ->
                val image = page.layers
                    .filterIsInstance<com.us.android.core.creator.model.ImageLayer>().first()
                val source = project.sourceAssets.first { it.assetId == image.assetRef }
                PageUi(
                    pageId = page.pageId,
                    assetId = image.assetRef,
                    sourcePath = vault.resolve(source.vaultPath)?.absolutePath.orEmpty(),
                    crop = image.crop,
                    adjustments = image.adjustments,
                    rotationDegMicros = image.transform.rotationDegMicros,
                    altText = page.accessibility.altText,
                    decorative = page.accessibility.decorative,
                    sourceWidthPx = source.widthPx,
                    sourceHeightPx = source.heightPx,
                    textLayers = page.layers
                        .filterIsInstance<com.us.android.core.creator.model.TextLayer>()
                        .map {
                            TextLayerUi(
                                layerId = it.layerId,
                                value = it.text.value,
                                fontAssetId = it.style.fontAssetId,
                                colorArgb = it.style.colorArgb,
                                sizeCanvasMicros = it.style.sizeCanvasMicros,
                            )
                        },
                )
            },
            selectedPageId = previous.selectedPageId
                ?.takeIf { id -> project.pages.any { it.pageId == id } }
                ?: project.pages.firstOrNull()?.pageId,
            canUndo = session.canUndo,
            canRedo = session.canRedo,
        )
    }

    /** Durable within the frozen 250 ms budget of the LAST mutation. */
    private fun scheduleAutosave() {
        autosave?.cancel()
        autosave = viewModelScope.launch {
            delay(AUTOSAVE_DEBOUNCE_MILLIS)
            runCatching { store.save(session.current, System.currentTimeMillis()) }
        }
    }

    private fun decodeDims(relativePath: String): Pair<Int, Int> {
        val file = vault.resolve(relativePath) ?: return 1 to 1
        val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(file.absolutePath, options)
        return maxOf(options.outWidth, 1) to maxOf(options.outHeight, 1)
    }

    private fun newProject() = AndroidCreatorProject(
        projectId = newId(ULID_LENGTH),
        revision = 1,
        status = AndroidCreatorProject.STATUS_EDITING,
        createdAtMillis = System.currentTimeMillis(),
        updatedAtMillis = System.currentTimeMillis(),
        postText = PostText("", DEFAULT_LANGUAGE),
        canvas = Canvas(CANVAS_W, CANVAS_H, "4:5", SafeZone(0, 0, 0, 0)),
    )

    private fun newId(length: Int): String {
        val alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
        val random = SecureRandom()
        return buildString(length) {
            repeat(length) { append(alphabet[random.nextInt(alphabet.length)]) }
        }
    }

    private companion object {
        const val KEY_URIS_IMPORTED = "studioInitialUrisImported"
        const val MAX_PAGES = 10
        const val MICROS_I = 1_000_000
        const val MICROS_D = 1_000_000.0
        const val ULID_LENGTH = 26
        const val ID_CHARS = 8
        const val AUTOSAVE_DEBOUNCE_MILLIS = 250L
        const val QUARTER_MICROS = 90_000_000
        const val FULL_TURN_MICROS = 360_000_000
        const val DEFAULT_TEXT_WEIGHT = 500
        const val DEFAULT_TEXT_SIZE_MICROS = 52_000
        const val CANVAS_W = 1080
        const val CANVAS_H = 1350
        const val DEFAULT_LANGUAGE = "en"
    }
}

private inline fun StudioViewModel.selected(block: (String) -> Unit) {
    state.value.selectedPageId?.let(block)
}
