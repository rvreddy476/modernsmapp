package com.us.android.core.facear

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The JS call, per kind and per variant.
 *
 * This is the contract between Momentum and every effect bundle anyone writes
 * — see the module README — so the tests assert the exact bytes, not a shape.
 */
class TryOnJsCallTest {

    private fun descriptor(
        kind: TryOnKind,
        slug: String = "momentum-eyewear-v1",
        variants: List<TryOnVariant> = listOf(
            TryOnVariant(id = "v-42", label = "Crimson", hex = "#C21F3A"),
        ),
        capable: Boolean = true,
    ) = TryOnDescriptor(capable = capable, kind = kind, effectSlug = slug, variants = variants)

    private fun method(d: TryOnDescriptor, v: TryOnVariant?): String? =
        (tryOnJsCall(d, v) as? TryOnJsCall.Method)?.method

    private fun args(
        d: TryOnDescriptor,
        v: TryOnVariant?,
        look: TryOnLook = TryOnLook.DEFAULT,
    ): String? = (tryOnJsCall(d, v, look) as? TryOnJsCall.Method)?.arguments

    @Test
    fun `each kind calls the method its bundle must define`() {
        val expected = mapOf(
            TryOnKind.EYEWEAR to "setFrameColour",
            TryOnKind.MAKEUP to "setShade",
            TryOnKind.JEWELLERY to "setMetal",
            TryOnKind.WATCH to "setStrap",
        )

        expected.forEach { (kind, name) ->
            val d = descriptor(kind)
            assertThat(method(d, d.variants.first())).isEqualTo(name)
        }
    }

    @Test
    fun `a colour variant carries the normalised hex and the same colour as unit floats`() {
        val d = descriptor(TryOnKind.MAKEUP)

        assertThat(args(d, d.variants.first()))
            .isEqualTo("""{"variant":"v-42","hex":"#C21F3A","rgb":[0.761,0.122,0.227]$MAKEUP_LOOK}""")
    }

    @Test
    fun `the eval form is the same call as source`() {
        val d = descriptor(TryOnKind.EYEWEAR)

        assertThat(tryOnJsCall(d, d.variants.first())?.script)
            .isEqualTo("""setFrameColour({"variant":"v-42","hex":"#C21F3A","rgb":[0.761,0.122,0.227]})""")
    }

    @Test
    fun `a bare hex without the hash and in lower case normalises the same way`() {
        val d = descriptor(
            TryOnKind.MAKEUP,
            variants = listOf(TryOnVariant(id = "v-1", label = "Crimson", hex = " c21f3a ")),
        )

        assertThat(args(d, d.variants.first()))
            .isEqualTo("""{"variant":"v-1","hex":"#C21F3A","rgb":[0.761,0.122,0.227]$MAKEUP_LOOK}""")
    }

    @Test
    fun `black and white land on the ends of the unit range`() {
        val d = descriptor(
            TryOnKind.JEWELLERY,
            variants = listOf(
                TryOnVariant(id = "b", label = "Onyx", hex = "#000000"),
                TryOnVariant(id = "w", label = "Platinum", hex = "#FFFFFF"),
            ),
        )

        assertThat(args(d, d.variants[0]))
            .isEqualTo("""{"variant":"b","hex":"#000000","rgb":[0.000,0.000,0.000]}""")
        assertThat(args(d, d.variants[1]))
            .isEqualTo("""{"variant":"w","hex":"#FFFFFF","rgb":[1.000,1.000,1.000]}""")
    }

    @Test
    fun `a variant with no colour sends only the variant, so the bundle keeps its default`() {
        val d = descriptor(
            TryOnKind.EYEWEAR,
            variants = listOf(TryOnVariant(id = "medium", label = "Medium", hex = null)),
        )

        assertThat(args(d, d.variants.first())).isEqualTo("""{"variant":"medium"}""")
    }

    @Test
    fun `a malformed hex degrades to no colour rather than to a wrong one`() {
        listOf("", "#FFF", "crimson", "#C21F3AFF", "#GGGGGG", "0xC21F3A").forEach { bad ->
            val d = descriptor(
                TryOnKind.MAKEUP,
                variants = listOf(TryOnVariant(id = "v", label = "Shade", hex = bad)),
            )

            assertThat(args(d, d.variants.first()))
                .isEqualTo("""{"variant":"v"$MAKEUP_LOOK}""")
        }
    }

    @Test
    fun `a variant id off the wire is JSON-escaped, not pasted into the script`() {
        val hostile = """v"-\42"""
        val d = descriptor(
            TryOnKind.WATCH,
            variants = listOf(TryOnVariant(id = hostile, label = "Odd", hex = null)),
        )

        assertThat(args(d, d.variants.first())).isEqualTo("""{"variant":"v\"-\\42"}""")
    }

    // ─── the server's escape hatch ───────────────────────────────────

    @Test
    fun `a variant with its own js replaces the generated call verbatim`() {
        val script = "setGradient({top:'#C21F3A',bottom:'#101014'})"
        val d = descriptor(
            TryOnKind.EYEWEAR,
            variants = listOf(
                TryOnVariant(id = "v-42", label = "Sunset", hex = "#C21F3A", js = script),
            ),
        )

        val call = tryOnJsCall(d, d.variants.first())

        assertThat(call).isEqualTo(TryOnJsCall.Script(script))
        assertThat(call?.script).isEqualTo(script)
        // Not the generated form — the whole point of the field is that the
        // generated call could not express the look.
        assertThat(call).isNotInstanceOf(TryOnJsCall.Method::class.java)
    }

    @Test
    fun `a blank js is not a script, and the generated call still happens`() {
        listOf("", "   ", "\n").forEach { blank ->
            val d = descriptor(
                TryOnKind.MAKEUP,
                variants = listOf(
                    TryOnVariant(id = "v-1", label = "Rose", hex = "#E06A8B", js = blank),
                ),
            )

            assertThat(args(d, d.variants.first()))
                .isEqualTo("""{"variant":"v-1","hex":"#E06A8B","rgb":[0.878,0.416,0.545]$MAKEUP_LOOK}""")
        }
    }

    @Test
    fun `a js variant on an unusable descriptor is still no call`() {
        val d = descriptor(
            TryOnKind.UNKNOWN,
            variants = listOf(TryOnVariant(id = "v", label = "x", js = "doThing()")),
        )

        assertThat(tryOnJsCall(d, d.variants.first())).isNull()
    }

    // ─── refusals ────────────────────────────────────────────────────

    @Test
    fun `no chosen variant is no call, so the effect keeps its own default`() {
        val d = descriptor(TryOnKind.EYEWEAR)

        assertThat(tryOnJsCall(d, null)).isNull()
    }

    @Test
    fun `a variant that is not one of the descriptor's own is refused`() {
        val d = descriptor(TryOnKind.EYEWEAR)

        val foreign = TryOnVariant(id = "someone-elses", label = "Nope", hex = "#000000")

        assertThat(tryOnJsCall(d, foreign)).isNull()
    }

    @Test
    fun `an unusable descriptor produces no call at all`() {
        val notCapable = descriptor(TryOnKind.EYEWEAR, capable = false)
        val noSlug = descriptor(TryOnKind.EYEWEAR, slug = "  ")
        val unknownKind = descriptor(TryOnKind.UNKNOWN)

        listOf(notCapable, noSlug, unknownKind).forEach { d ->
            assertThat(tryOnJsCall(d, d.variants.first())).isNull()
        }
    }

    // ─── the two settings the wire cannot carry ──────────────────────

    @Test
    fun `makeup carries the chosen finish and coverage, in the prefab's own vocabulary`() {
        val d = descriptor(TryOnKind.MAKEUP)

        // `glitter` and `high` are makeup_lipsshine/schema.json's own enum
        // values. They are what may reach the effect — not "Glitter", not
        // "Full", which are the words on our buttons.
        assertThat(
            args(d, d.variants.first(), TryOnLook(TryOnFinish.GLITTER, TryOnCoverage.HIGH)),
        ).isEqualTo(
            """{"variant":"v-42","hex":"#C21F3A","rgb":[0.761,0.122,0.227],""" +
                """"finish":"glitter","coverage":"high"}""",
        )
    }

    @Test
    fun `every finish and coverage maps to the exact string the prefab schema enumerates`() {
        assertThat(TryOnFinish.entries.map { it.wire }).containsExactly("shine", "glitter")
        assertThat(TryOnCoverage.entries.map { it.wire }).containsExactly("low", "mid", "high")
        assertThat(TryOnFinish.DEFAULT.wire).isEqualTo("shine")
        assertThat(TryOnCoverage.DEFAULT.wire).isEqualTo("mid")
    }

    @Test
    fun `no kind but makeup is sent a finish or a coverage`() {
        // Base.setPrefabSettings THROWS on a state key the prefab's class does
        // not implement, so sending makeup_lipsshine's settings to an eyewear
        // or watch bundle would turn a shade change into a dead effect. The
        // kinds that have no such settings must get the bare payload.
        listOf(TryOnKind.EYEWEAR, TryOnKind.JEWELLERY, TryOnKind.WATCH).forEach { kind ->
            val d = descriptor(kind)

            val arguments = args(
                d,
                d.variants.first(),
                TryOnLook(TryOnFinish.GLITTER, TryOnCoverage.HIGH),
            )

            assertThat(arguments).doesNotContain("finish")
            assertThat(arguments).doesNotContain("coverage")
        }
    }

    // ─── hold to compare ─────────────────────────────────────────────

    @Test
    fun `a bare call is the same look with no colour, which is how compare works`() {
        val d = descriptor(TryOnKind.MAKEUP)

        val bare = tryOnJsCall(
            d,
            d.variants.first(),
            TryOnLook(TryOnFinish.GLITTER, TryOnCoverage.LOW),
            bare = true,
        )

        // No `hex` and no `rgb` — which is exactly the payload a variant with
        // no colour already produces, and which `momentum_lipstick` answers by
        // clearing. The finish and coverage stay, so releasing the control
        // restores the same look rather than the default one.
        assertThat((bare as? TryOnJsCall.Method)?.arguments)
            .isEqualTo("""{"variant":"v-42","finish":"glitter","coverage":"low"}""")
    }

    @Test
    fun `a variant with its own js has no bare form, so compare is not offered for it`() {
        val d = descriptor(
            TryOnKind.MAKEUP,
            variants = listOf(
                TryOnVariant(id = "v-42", label = "Sunset", hex = "#C21F3A", js = "setGradient()"),
            ),
        )

        // Nothing here knows which part of a seller's script was the colour,
        // and guessing would change the look in an unknown way while the
        // shopper believes they are seeing their bare face.
        assertThat(tryOnJsCall(d, d.variants.first(), bare = true)).isNull()
        // The normal call is untouched.
        assertThat(tryOnJsCall(d, d.variants.first())).isEqualTo(TryOnJsCall.Script("setGradient()"))
    }

    @Test
    fun `the kind vocabulary is the server's, and an unseen kind is UNKNOWN`() {
        assertThat(TryOnKind.from("eyewear")).isEqualTo(TryOnKind.EYEWEAR)
        assertThat(TryOnKind.from("MAKEUP")).isEqualTo(TryOnKind.MAKEUP)
        assertThat(TryOnKind.from("jewellery")).isEqualTo(TryOnKind.JEWELLERY)
        // A US importer's spelling still puts a necklace on a neck.
        assertThat(TryOnKind.from("jewelry")).isEqualTo(TryOnKind.JEWELLERY)
        assertThat(TryOnKind.from("watch")).isEqualTo(TryOnKind.WATCH)
        assertThat(TryOnKind.from("footwear")).isEqualTo(TryOnKind.UNKNOWN)
        assertThat(TryOnKind.from(null)).isEqualTo(TryOnKind.UNKNOWN)
        assertThat(TryOnKind.from("")).isEqualTo(TryOnKind.UNKNOWN)
    }

    private companion object {
        /**
         * What a MAKEUP payload carries beyond the colour, at the defaults.
         *
         * Spelled out once here rather than repeated in eight string literals,
         * because it is a suffix on an exact-bytes assertion: if it changes,
         * every makeup expectation has to change with it, and one that was
         * missed would pass by accident.
         */
        const val MAKEUP_LOOK = ",\"finish\":\"shine\",\"coverage\":\"mid\""
    }
}
