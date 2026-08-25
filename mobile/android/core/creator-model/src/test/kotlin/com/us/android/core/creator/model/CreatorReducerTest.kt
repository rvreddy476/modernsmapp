package com.us.android.core.creator.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * CS-LB-2 — non-destructive editing: the reducer, replay, and undo/redo.
 *
 * ## THE PROPERTY UNDER TEST
 *
 * Undo here is not an inverse operation — it is replaying history minus the
 * last command over the base document. That is only correct if the reducer is
 * pure: same document + same command = same result, always. Most tests below
 * are therefore equality assertions between documents reached by different
 * routes, which is exactly the property the canonical fingerprint depends on.
 */
class CreatorReducerTest {

    private fun asset(id: String) = SourceAsset(
        assetId = id,
        kind = "image",
        vaultPath = "sources/$id.bin",
        sha256 = "a".repeat(64),
        bytes = 42,
        mime = "image/jpeg",
        widthPx = 1000,
        heightPx = 1000,
        origin = "photoPicker",
    )

    private fun project(vararg assets: String) = AndroidCreatorProject(
        projectId = "01J9Z4K7QW8XN2VB3M5R7T9Y0C",
        revision = 1,
        status = AndroidCreatorProject.STATUS_EDITING,
        createdAtMillis = 1,
        updatedAtMillis = 1,
        postText = PostText("three evenings", "en"),
        canvas = Canvas(1080, 1350, "4:5", SafeZone(0, 0, 0, 0)),
        sourceAssets = assets.map { asset(it) },
    )

    private fun style() = TextStyle(
        fontAssetId = "inter",
        fontVersion = "4.000",
        fontSha256 = "b".repeat(64),
        license = "OFL-1.1",
        weight = 500,
        sizeCanvasMicros = 50_000,
        colorArgb = "#FFFFFFFF",
        align = "center",
    )

    private fun applied(result: ReduceResult) = (result as ReduceResult.Applied).project

    private fun withPages(vararg ids: String): AndroidCreatorProject {
        var current = project(*ids)
        ids.forEach { id ->
            current = applied(CreatorReducer.reduce(current, CreatorCommand.AddPage("p$id", "l$id", id)))
        }
        return current
    }

    // ------------------------------------------------------------------
    // Structural commands
    // ------------------------------------------------------------------

    @Test
    fun `adding a page builds a valid single-image page with no invented accessibility`() {
        val result = applied(
            CreatorReducer.reduce(project("a1"), CreatorCommand.AddPage("p1", "l1", "a1")),
        )

        val page = result.pages.single()
        assertThat(page.layers.single()).isInstanceOf(ImageLayer::class.java)
        assertThat(page.layers.single().z).isEqualTo(0)
        // No decision was invented: not decorative, no text — publish stays blocked.
        assertThat(page.accessibility).isEqualTo(Accessibility("", false))
        assertThat(Validators.validate(result)).isEmpty()
    }

    @Test
    fun `a page for an unimported asset is rejected`() {
        val result = CreatorReducer.reduce(project("a1"), CreatorCommand.AddPage("p1", "l1", "a9"))

        assertThat(result).isInstanceOf(ReduceResult.Rejected::class.java)
    }

    @Test
    fun `the eleventh page is rejected`() {
        var current = project(*(1..10).map { "a$it" }.toTypedArray())
        (1..10).forEach {
            current = applied(CreatorReducer.reduce(current, CreatorCommand.AddPage("p$it", "l$it", "a$it")))
        }

        val eleventh = CreatorReducer.reduce(current, CreatorCommand.AddPage("p11", "l11", "a1"))

        assertThat(eleventh).isInstanceOf(ReduceResult.Rejected::class.java)
    }

    /** The C,A,B ordering the whole carousel contract exists for. */
    @Test
    fun `moving pages produces the author's order`() {
        var current = withPages("a", "b", "c")

        // a,b,c -> move c to front -> c,a,b
        current = applied(CreatorReducer.reduce(current, CreatorCommand.MovePage("pc", 0)))

        assertThat(current.pages.map { it.pageId }).containsExactly("pc", "pa", "pb").inOrder()
        assertThat(Validators.validate(current)).isEmpty()
    }

    @Test
    fun `removing a text layer renumbers z so the document stays valid`() {
        var current = withPages("a")
        listOf("t1" to "one", "t2" to "two").forEach { (id, value) ->
            current = applied(
                CreatorReducer.reduce(
                    current,
                    CreatorCommand.AddTextLayer(
                        pageId = "pa",
                        layerId = id,
                        text = LayerText(value, "en"),
                        style = style(),
                        transform = CreatorReducer.IDENTITY_TRANSFORM,
                    ),
                ),
            )
        }

        current = applied(CreatorReducer.reduce(current, CreatorCommand.RemoveTextLayer("pa", "t1")))

        val zs = current.pages.single().layers.map { it.z }
        assertThat(zs).containsExactly(0, 1).inOrder()
        assertThat(Validators.validate(current)).isEmpty()
    }

    /** The base image is not a text layer and cannot be removed as one. */
    @Test
    fun `the image layer cannot be removed`() {
        val current = withPages("a")

        val result = CreatorReducer.reduce(current, CreatorCommand.RemoveTextLayer("pa", "la"))

        assertThat(result).isInstanceOf(ReduceResult.Rejected::class.java)
    }

    // ------------------------------------------------------------------
    // Purity and replay
    // ------------------------------------------------------------------

    /** Same document, same command, same bytes — the property undo rests on. */
    @Test
    fun `the reducer is deterministic to the byte`() {
        val base = withPages("a", "b")
        val command = CreatorCommand.SetCrop("pa", Crop(0, 0, 500_000, 500_000))

        val first = Canonical.encode(applied(CreatorReducer.reduce(base, command)))
        val second = Canonical.encode(applied(CreatorReducer.reduce(base, command)))

        assertThat(first).isEqualTo(second)
    }

    @Test
    fun `replay reaches the same document as stepwise application`() {
        val base = project("a", "b")
        val history = listOf(
            CreatorCommand.AddPage("pa", "la", "a"),
            CreatorCommand.AddPage("pb", "lb", "b"),
            CreatorCommand.MovePage("pb", 0),
            CreatorCommand.SetAccessibility("pa", Accessibility("tea glasses", false)),
        )

        var stepwise = base
        history.forEach { stepwise = applied(CreatorReducer.reduce(stepwise, it)) }
        val replayed = applied(CreatorReducer.replay(base, history))

        assertThat(Canonical.encode(replayed)).isEqualTo(Canonical.encode(stepwise))
    }

    // ------------------------------------------------------------------
    // The edit session: undo/redo
    // ------------------------------------------------------------------

    @Test
    fun `undo returns the exact previous document`() {
        val session = EditSession(project("a"))
        session.apply(CreatorCommand.AddPage("pa", "la", "a"))
        val beforeCrop = Canonical.encode(session.current)

        session.apply(CreatorCommand.SetCrop("pa", Crop(0, 0, 500_000, 500_000)))
        session.undo()

        assertThat(Canonical.encode(session.current)).isEqualTo(beforeCrop)
    }

    @Test
    fun `redo reapplies what undo removed`() {
        val session = EditSession(project("a"))
        session.apply(CreatorCommand.AddPage("pa", "la", "a"))
        session.apply(CreatorCommand.SetAdjustments("pa", Adjustments(100_000, 0)))
        val afterAdjust = Canonical.encode(session.current)

        session.undo()
        session.redo()

        assertThat(Canonical.encode(session.current)).isEqualTo(afterAdjust)
    }

    /** The standard editor contract: a new command after undo discards redo. */
    @Test
    fun `a new command after undo clears the redo tail`() {
        val session = EditSession(project("a", "b"))
        session.apply(CreatorCommand.AddPage("pa", "la", "a"))
        session.apply(CreatorCommand.AddPage("pb", "lb", "b"))
        session.undo()

        session.apply(CreatorCommand.SetPostText(PostText("rewritten", "en")))

        assertThat(session.canRedo).isFalse()
        assertThat(session.current.pages.map { it.pageId }).containsExactly("pa")
    }

    /** A rejected command changes nothing — not the document, not the stacks. */
    @Test
    fun `a rejected command leaves the session untouched`() {
        val session = EditSession(project("a"))
        session.apply(CreatorCommand.AddPage("pa", "la", "a"))
        val before = Canonical.encode(session.current)

        val result = session.apply(CreatorCommand.MovePage("does-not-exist", 0))

        assertThat(result).isInstanceOf(ReduceResult.Rejected::class.java)
        assertThat(Canonical.encode(session.current)).isEqualTo(before)
        assertThat(session.canUndo).isTrue()
    }

    /** Source hashes never change through any edit — CS-LB-2's core claim. */
    @Test
    fun `no command touches a source asset`() {
        val session = EditSession(project("a"))
        val sourcesBefore = session.current.sourceAssets

        session.apply(CreatorCommand.AddPage("pa", "la", "a"))
        session.apply(CreatorCommand.SetCrop("pa", Crop(0, 0, 250_000, 250_000)))
        session.apply(CreatorCommand.SetAdjustments("pa", Adjustments(-500_000, 300_000)))
        session.undo()

        assertThat(session.current.sourceAssets).isEqualTo(sourcesBefore)
    }
}
