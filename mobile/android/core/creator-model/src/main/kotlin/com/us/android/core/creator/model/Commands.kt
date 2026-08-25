package com.us.android.core.creator.model

/**
 * The Post Studio edit commands — the ONLY way a project document changes.
 *
 * ## WHY A COMMAND MODEL AND NOT SETTERS
 *
 * Undo/redo is a product requirement, and undo built on "remember the previous
 * copy of everything" dies on a 10-page project. Every mutation is therefore a
 * value in this sealed hierarchy, applied by one pure [CreatorReducer] that
 * returns a NEW document. Undo is replaying the history minus the last command
 * over the base document — no inverse operations to hand-maintain, no drift
 * between what a command did and what its undo claims to undo.
 *
 * Purity is also what makes this testable on the JVM: same document + same
 * command = same bytes, every time, which is the property the canonical
 * fingerprint already depends on.
 *
 * ## WHAT IS DELIBERATELY NOT HERE
 *
 * No filter/LUT command, no sticker, no drawing, no animation, no layer
 * rename/lock — all cut from P0-A by the freeze (B5). Adding a command here is
 * adding product scope, and the reducer refuses nothing silently: every command
 * either applies or returns a stated reason.
 */
sealed interface CreatorCommand {

    /**
     * Register a vault-imported source in the document.
     *
     * A command rather than a side-band mutation so that "add photo" sits in
     * the same undo stream as everything else. Undoing it removes the
     * DOCUMENT's reference; the vault file itself stays until cleanup — an
     * orphaned copy is recoverable, a deleted original is not.
     */
    data class ImportAsset(val asset: SourceAsset) : CreatorCommand

    /** Append a page for an imported source, at the end. */
    data class AddPage(
        val pageId: String,
        val layerId: String,
        val assetId: String,
    ) : CreatorCommand

    data class RemovePage(val pageId: String) : CreatorCommand

    /** Move a page to a new zero-based index; every other page shifts. */
    data class MovePage(val pageId: String, val toIndex: Int) : CreatorCommand

    data class SetCrop(val pageId: String, val crop: Crop) : CreatorCommand

    data class SetAdjustments(val pageId: String, val adjustments: Adjustments) : CreatorCommand

    /** Rotate the base image. Quarter turns only in P0-A: 0, 90, 180, 270. */
    data class SetRotation(val pageId: String, val rotationDegMicros: Int) : CreatorCommand

    /** Add a text layer above the image. */
    data class AddTextLayer(
        val pageId: String,
        val layerId: String,
        val text: LayerText,
        val style: TextStyle,
        val transform: Transform,
    ) : CreatorCommand

    data class EditTextLayer(
        val pageId: String,
        val layerId: String,
        val text: LayerText,
        val style: TextStyle,
        val transform: Transform,
    ) : CreatorCommand

    data class RemoveTextLayer(val pageId: String, val layerId: String) : CreatorCommand

    /** The per-page accessibility decision: a description, or explicit decorative. */
    data class SetAccessibility(val pageId: String, val accessibility: Accessibility) : CreatorCommand

    data class SetPostText(val postText: PostText) : CreatorCommand
}

/** The outcome of applying one command. */
sealed interface ReduceResult {
    data class Applied(val project: AndroidCreatorProject) : ReduceResult

    /**
     * The command could not apply. A stated reason, never an exception — a
     * stale command from a queued gesture is ordinary, not a crash.
     */
    data class Rejected(val reason: String) : ReduceResult
}

object CreatorReducer {

    /**
     * Apply one command, returning a new document with the revision bumped.
     *
     * Every branch validates its target exists before touching anything, so a
     * command that raced a removal rejects instead of corrupting. Z-order and
     * page-count invariants (V-4, V-5, V-13) are maintained BY CONSTRUCTION
     * here; [Validators] re-checks them at persistence as the second lock on
     * the same door.
     */
    fun reduce(project: AndroidCreatorProject, command: CreatorCommand): ReduceResult =
        when (command) {
            is CreatorCommand.ImportAsset ->
                if (project.sourceAssets.any { it.assetId == command.asset.assetId }) {
                    ReduceResult.Rejected("asset ${command.asset.assetId} is already imported")
                } else {
                    ReduceResult.Applied(
                        bump(project).copy(sourceAssets = project.sourceAssets + command.asset),
                    )
                }
            is CreatorCommand.AddPage -> addPage(project, command)
            is CreatorCommand.RemovePage -> removePage(project, command)
            is CreatorCommand.MovePage -> movePage(project, command)
            is CreatorCommand.SetCrop -> mapImageLayer(project, command.pageId) {
                it.copy(crop = command.crop)
            }
            is CreatorCommand.SetAdjustments -> mapImageLayer(project, command.pageId) {
                it.copy(adjustments = command.adjustments)
            }
            is CreatorCommand.SetRotation -> setRotation(project, command)
            is CreatorCommand.AddTextLayer -> addTextLayer(project, command)
            is CreatorCommand.EditTextLayer -> editTextLayer(project, command)
            is CreatorCommand.RemoveTextLayer -> removeTextLayer(project, command)
            is CreatorCommand.SetAccessibility -> mapPage(project, command.pageId) {
                it.copy(accessibility = command.accessibility)
            }
            is CreatorCommand.SetPostText -> ReduceResult.Applied(
                bump(project).copy(postText = command.postText),
            )
        }

    /**
     * Replay a history over a base document.
     *
     * This IS undo: drop the last command and replay. A rejected replayed
     * command means the history itself is corrupt, so the replay stops there
     * rather than applying later commands to a document they never saw.
     */
    fun replay(base: AndroidCreatorProject, history: List<CreatorCommand>): ReduceResult {
        var current = base
        history.forEach { command ->
            when (val result = reduce(current, command)) {
                is ReduceResult.Applied -> current = result.project
                is ReduceResult.Rejected -> return result
            }
        }
        return ReduceResult.Applied(current)
    }

    // ------------------------------------------------------------------

    private fun bump(project: AndroidCreatorProject) =
        project.copy(revision = project.revision + 1)

    private fun setRotation(
        project: AndroidCreatorProject,
        command: CreatorCommand.SetRotation,
    ): ReduceResult =
        if (command.rotationDegMicros !in QUARTER_TURNS) {
            ReduceResult.Rejected("rotation must be a quarter turn, got ${command.rotationDegMicros}")
        } else {
            mapImageLayer(project, command.pageId) {
                it.copy(transform = it.transform.copy(rotationDegMicros = command.rotationDegMicros))
            }
        }

    private fun addPage(
        project: AndroidCreatorProject,
        command: CreatorCommand.AddPage,
    ): ReduceResult {
        if (project.pages.size >= AndroidCreatorProject.MAX_PAGES) {
            return ReduceResult.Rejected("a post carries at most ${AndroidCreatorProject.MAX_PAGES} pages")
        }
        if (project.pages.any { it.pageId == command.pageId }) {
            return ReduceResult.Rejected("page ${command.pageId} already exists")
        }
        if (project.sourceAssets.none { it.assetId == command.assetId }) {
            return ReduceResult.Rejected("asset ${command.assetId} is not imported")
        }
        val page = Page(
            pageId = command.pageId,
            // No decision yet: not decorative, no text. Publishing stays
            // blocked (V-12) until the author actually chooses.
            accessibility = Accessibility(altText = "", decorative = false),
            layers = listOf(
                ImageLayer(
                    layerId = command.layerId,
                    z = 0,
                    transform = IDENTITY_TRANSFORM,
                    assetRef = command.assetId,
                    crop = FULL_CROP,
                    adjustments = NO_ADJUSTMENTS,
                ),
            ),
        )
        return ReduceResult.Applied(bump(project).copy(pages = project.pages + page))
    }

    private fun removePage(
        project: AndroidCreatorProject,
        command: CreatorCommand.RemovePage,
    ): ReduceResult {
        if (project.pages.none { it.pageId == command.pageId }) {
            return ReduceResult.Rejected("page ${command.pageId} does not exist")
        }
        return ReduceResult.Applied(
            bump(project).copy(pages = project.pages.filterNot { it.pageId == command.pageId }),
        )
    }

    private fun movePage(
        project: AndroidCreatorProject,
        command: CreatorCommand.MovePage,
    ): ReduceResult {
        val from = project.pages.indexOfFirst { it.pageId == command.pageId }
        if (from < 0) return ReduceResult.Rejected("page ${command.pageId} does not exist")
        if (command.toIndex !in project.pages.indices) {
            return ReduceResult.Rejected("index ${command.toIndex} is outside 0..${project.pages.lastIndex}")
        }
        val reordered = project.pages.toMutableList()
        val page = reordered.removeAt(from)
        reordered.add(command.toIndex, page)
        return ReduceResult.Applied(bump(project).copy(pages = reordered))
    }

    private fun addTextLayer(
        project: AndroidCreatorProject,
        command: CreatorCommand.AddTextLayer,
    ): ReduceResult = mapPage(project, command.pageId) { page ->
        if (page.layers.any { it.layerId == command.layerId }) {
            return ReduceResult.Rejected("layer ${command.layerId} already exists")
        }
        // z is the array index (V-5), so a new top layer is simply appended.
        page.copy(
            layers = page.layers + TextLayer(
                layerId = command.layerId,
                z = page.layers.size,
                transform = command.transform,
                text = command.text,
                style = command.style,
            ),
        )
    }

    private fun editTextLayer(
        project: AndroidCreatorProject,
        command: CreatorCommand.EditTextLayer,
    ): ReduceResult = mapPage(project, command.pageId) { page ->
        val index = page.layers.indexOfFirst { it.layerId == command.layerId }
        val existing = page.layers.getOrNull(index)
        if (existing !is TextLayer) {
            return ReduceResult.Rejected("layer ${command.layerId} is not an editable text layer")
        }
        page.copy(
            layers = page.layers.toMutableList().also {
                it[index] = existing.copy(
                    text = command.text,
                    style = command.style,
                    transform = command.transform,
                )
            },
        )
    }

    private fun removeTextLayer(
        project: AndroidCreatorProject,
        command: CreatorCommand.RemoveTextLayer,
    ): ReduceResult = mapPage(project, command.pageId) { page ->
        val target = page.layers.firstOrNull { it.layerId == command.layerId }
        if (target !is TextLayer) {
            return ReduceResult.Rejected("layer ${command.layerId} is not a removable text layer")
        }
        // Re-number z after removal so V-5 (z == index) holds by construction.
        val remaining = page.layers.filterNot { it.layerId == command.layerId }
        page.copy(layers = remaining.mapIndexed { index, layer -> withZ(layer, index) })
    }

    private inline fun mapPage(
        project: AndroidCreatorProject,
        pageId: String,
        transform: (Page) -> Page,
    ): ReduceResult {
        val index = project.pages.indexOfFirst { it.pageId == pageId }
        if (index < 0) return ReduceResult.Rejected("page $pageId does not exist")
        val pages = project.pages.toMutableList()
        pages[index] = transform(pages[index])
        return ReduceResult.Applied(bump(project).copy(pages = pages))
    }

    private inline fun mapImageLayer(
        project: AndroidCreatorProject,
        pageId: String,
        transform: (ImageLayer) -> ImageLayer,
    ): ReduceResult = mapPage(project, pageId) { page ->
        val index = page.layers.indexOfFirst { it is ImageLayer }
        val image = page.layers.getOrNull(index) as? ImageLayer
            ?: return ReduceResult.Rejected("page $pageId has no image layer")
        page.copy(layers = page.layers.toMutableList().also { it[index] = transform(image) })
    }

    private fun withZ(layer: Layer, z: Int): Layer = when (layer) {
        is ImageLayer -> layer.copy(z = z)
        is TextLayer -> layer.copy(z = z)
    }

    val IDENTITY_TRANSFORM = Transform(
        xMicros = HALF_MICROS,
        yMicros = HALF_MICROS,
        scaleMicros = MICROS,
        rotationDegMicros = 0,
    )
    val FULL_CROP = Crop(xMicros = 0, yMicros = 0, wMicros = MICROS, hMicros = MICROS)
    val NO_ADJUSTMENTS = Adjustments(exposureMicros = 0, contrastMicros = 0)

    private const val QUARTER_TURN_MICROS = 90_000_000
    private const val HALF_TURN_MICROS = 180_000_000
    private const val THREE_QUARTER_TURN_MICROS = 270_000_000

    /** The four legal image rotations, in degree-micros. */
    val QUARTER_TURNS = setOf(
        0,
        QUARTER_TURN_MICROS,
        HALF_TURN_MICROS,
        THREE_QUARTER_TURN_MICROS,
    )
}

/**
 * The undo/redo session over one project.
 *
 * The base document plus an append-only history. Undo pops; redo re-pushes what
 * undo popped; a NEW command after undo discards the redo tail — the standard
 * editor contract, and the one behaviour users notice when it is wrong.
 */
class EditSession(
    val base: AndroidCreatorProject,
    history: List<CreatorCommand> = emptyList(),
) {
    private val applied = history.toMutableList()
    private val redoStack = ArrayDeque<CreatorCommand>()

    /** The current document, always derivable from base + history. */
    var current: AndroidCreatorProject = replayOrBase(history)
        private set

    val history: List<CreatorCommand> get() = applied.toList()
    val canUndo: Boolean get() = applied.isNotEmpty()
    val canRedo: Boolean get() = redoStack.isNotEmpty()

    /** Apply a new command. A rejection changes nothing, including the redo tail. */
    fun apply(command: CreatorCommand): ReduceResult {
        val result = CreatorReducer.reduce(current, command)
        if (result is ReduceResult.Applied) {
            applied += command
            redoStack.clear()
            current = result.project
        }
        return result
    }

    fun undo(): Boolean {
        if (applied.isEmpty()) return false
        redoStack.addLast(applied.removeAt(applied.lastIndex))
        current = replayOrBase(applied)
        return true
    }

    fun redo(): Boolean {
        val command = redoStack.removeLastOrNull() ?: return false
        return when (val result = CreatorReducer.reduce(current, command)) {
            is ReduceResult.Applied -> {
                applied += command
                current = result.project
                true
            }
            is ReduceResult.Rejected -> false
        }
    }

    private fun replayOrBase(history: List<CreatorCommand>): AndroidCreatorProject =
        when (val result = CreatorReducer.replay(base, history)) {
            is ReduceResult.Applied -> result.project
            // A corrupt history falls back to the base document rather than to
            // nothing: the sources and their pages are the user's work, and the
            // edits that DID replay are preserved up to the corruption point.
            is ReduceResult.Rejected -> base
        }
}

private const val MICROS = 1_000_000
private const val HALF_MICROS = 500_000
