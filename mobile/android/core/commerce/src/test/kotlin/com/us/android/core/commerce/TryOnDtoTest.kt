package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.network.ProductBodyDto
import com.us.android.core.commerce.repository.toTryOnDescriptor
import com.us.android.core.facear.TryOnKind
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The `try_on` contract, decoded with the app's real Json.
 *
 * Two claims are being defended here. First, an older server that sends no
 * `try_on` means "not capable" and nothing else. Second — and this is the one
 * worth the custom serializer — a `try_on` of the WRONG SHAPE must not take
 * the product detail page down with it. The server side of this contract is
 * being written in parallel, which is exactly when shapes move.
 */
class TryOnDtoTest {

    private val json = NetworkModule.provideJson()

    private fun product(tryOn: String?): ProductBodyDto = json.decodeFromString(
        ProductBodyDto.serializer(),
        buildString {
            append("""{"id":"p1","title":"Aviators","seller_id":"s1"""")
            tryOn?.let { append(""","try_on":$it""") }
            append("}")
        },
    )

    @Test
    fun `a capable product decodes its kind, slug and shades`() {
        val dto = product(
            """
            {"capable":true,"kind":"eyewear","effect_slug":"momentum-eyewear-v1",
             "variants":[{"id":"v-1","label":"Crimson","hex":"#C21F3A"},
                         {"id":"v-2","label":"Onyx","hex":"#101014"}]}
            """.trimIndent(),
        )

        val descriptor = toTryOnDescriptor(dto.tryOn)

        assertThat(descriptor?.kind).isEqualTo(TryOnKind.EYEWEAR)
        assertThat(descriptor?.effectSlug).isEqualTo("momentum-eyewear-v1")
        assertThat(descriptor?.variants?.map { it.id }).containsExactly("v-1", "v-2").inOrder()
        assertThat(descriptor?.variants?.first()?.hex).isEqualTo("#C21F3A")
        assertThat(descriptor?.isUsable).isTrue()
    }

    @Test
    fun `an older server that sends no try_on means not capable`() {
        val dto = product(null)

        assertThat(dto.tryOn).isNull()
        assertThat(toTryOnDescriptor(dto.tryOn)).isNull()
    }

    @Test
    fun `capable false is not a try-on, however complete the rest of the object is`() {
        val dto = product(
            """{"capable":false,"kind":"makeup","effect_slug":"lips-v2",
                "variants":[{"id":"v-1","label":"Rose","hex":"#E06A8B"}]}""",
        )

        assertThat(dto.tryOn?.capable).isFalse()
        assertThat(toTryOnDescriptor(dto.tryOn)).isNull()
    }

    @Test
    fun `an object with no capable field does not turn try-on on`() {
        val dto = product("""{"kind":"makeup","effect_slug":"lips-v2"}""")

        assertThat(toTryOnDescriptor(dto.tryOn)).isNull()
    }

    @Test
    fun `a half-configured product is not a try-on`() {
        val noSlug = product("""{"capable":true,"kind":"eyewear","effect_slug":""}""")
        val unknownKind = product("""{"capable":true,"kind":"footwear","effect_slug":"shoes-v1"}""")
        val blankKind = product("""{"capable":true,"effect_slug":"shoes-v1"}""")

        listOf(noSlug, unknownKind, blankKind).forEach { dto ->
            assertThat(toTryOnDescriptor(dto.tryOn)).isNull()
        }
    }

    @Test
    fun `a try_on of the wrong shape means not capable and does NOT fail the product read`() {
        listOf(
            "null",
            "[]",
            "3",
            """"eyewear"""",
            "true",
            // A recognisable object whose variants are the wrong type: the
            // nested decode fails, and the product still loads.
            """{"capable":true,"kind":"eyewear","effect_slug":"s","variants":"none"}""",
            // A wrong-typed `capable`.
            """{"capable":"yes","kind":"eyewear","effect_slug":"s"}""",
        ).forEach { shape ->
            val dto = product(shape)

            assertThat(dto.title).isEqualTo("Aviators")
            assertThat(toTryOnDescriptor(dto.tryOn)).isNull()
        }
    }

    @Test
    fun `a variant with no id is dropped, and one with no label falls back to its id`() {
        val dto = product(
            """{"capable":true,"kind":"jewellery","effect_slug":"neck-v1",
                "variants":[{"id":"","label":"Ghost"},{"id":"v-9","label":""},
                            {"id":"v-3","label":"Gold","hex":"  "}]}""",
        )

        val variants = toTryOnDescriptor(dto.tryOn)?.variants.orEmpty()

        assertThat(variants.map { it.id }).containsExactly("v-9", "v-3").inOrder()
        assertThat(variants.first().label).isEqualTo("v-9")
        // A blank hex is no colour, not an empty colour.
        assertThat(variants.last().hex).isNull()
    }

    @Test
    fun `a capable product with no shades at all is still a try-on on the effect's own default`() {
        val dto = product("""{"capable":true,"kind":"watch","effect_slug":"wrist-v1"}""")

        val descriptor = toTryOnDescriptor(dto.tryOn)

        assertThat(descriptor?.isUsable).isTrue()
        assertThat(descriptor?.variants).isEmpty()
    }

    @Test
    fun `a variant's own js is carried through, and a blank one is not a script`() {
        val dto = product(
            """{"capable":true,"kind":"eyewear","effect_slug":"frames-v1",
                "variants":[{"id":"v-1","label":"Sunset","hex":"#C21F3A",
                             "js":"setGradient({top:'#C21F3A'})"},
                            {"id":"v-2","label":"Plain","hex":"#101014","js":"  "},
                            {"id":"v-3","label":"None","hex":"#FFFFFF"}]}""",
        )

        val variants = toTryOnDescriptor(dto.tryOn)?.variants.orEmpty()

        assertThat(variants[0].js).isEqualTo("setGradient({top:'#C21F3A'})")
        // Blank is not a script: it would make the call builder hand an empty
        // evalJs to the effect instead of the generated call.
        assertThat(variants[1].js).isNull()
        assertThat(variants[2].js).isNull()
    }

    @Test
    fun `an unknown field inside try_on is ignored rather than fatal`() {
        val dto = product(
            """{"capable":true,"kind":"makeup","effect_slug":"lips-v2","opacity":0.6}""",
        )

        assertThat(toTryOnDescriptor(dto.tryOn)?.kind).isEqualTo(TryOnKind.MAKEUP)
    }
}
