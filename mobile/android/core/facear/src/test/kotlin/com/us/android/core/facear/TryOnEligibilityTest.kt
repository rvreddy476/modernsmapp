package com.us.android.core.facear

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Ten combinations of licence state and product capability, decided once.
 *
 * The policy this pins down: a licence problem on a try-on-capable product is
 * worth a line, because the viewer would otherwise read a missing feature; no
 * licence at all, or one still starting, is worth silence.
 */
class TryOnEligibilityTest {

    private val capable = TryOnDescriptor(
        capable = true,
        kind = TryOnKind.EYEWEAR,
        effectSlug = "momentum-eyewear-v1",
        variants = listOf(TryOnVariant("v-1", "Crimson", "#C21F3A")),
    )

    private val everyState = listOf(
        FaceArState.Unlicensed,
        FaceArState.Initialising,
        FaceArState.Ready,
        FaceArState.Invalid,
        FaceArState.Failed("libbanuba.so not found"),
    )

    @Test
    fun `a capable product with a live licence offers try-on`() {
        assertThat(tryOnEligibility(capable, FaceArState.Ready))
            .isEqualTo(TryOnEligibility.Offer)
    }

    @Test
    fun `no licence and a starting licence are both silent`() {
        assertThat(tryOnEligibility(capable, FaceArState.Unlicensed))
            .isEqualTo(TryOnEligibility.Hidden)
        assertThat(tryOnEligibility(capable, FaceArState.Initialising))
            .isEqualTo(TryOnEligibility.Hidden)
    }

    @Test
    fun `an expired licence says so in one line, and names no vendor`() {
        val answer = tryOnEligibility(capable, FaceArState.Invalid)

        assertThat(answer).isInstanceOf(TryOnEligibility.Unavailable::class.java)
        val reason = (answer as TryOnEligibility.Unavailable).reason
        assertThat(reason).isEqualTo(LICENCE_EXPIRED)
        assertThat(reason.lowercase()).doesNotContain("banuba")
    }

    @Test
    fun `a failed start says so without quoting the sdk's message`() {
        val answer = tryOnEligibility(capable, FaceArState.Failed("libbanuba.so not found"))

        assertThat(answer).isEqualTo(TryOnEligibility.Unavailable(TRY_ON_UNAVAILABLE))
    }

    @Test
    fun `an older server that sends no try_on object shows nothing, whatever the licence says`() {
        everyState.forEach { state ->
            assertThat(tryOnEligibility(null, state)).isEqualTo(TryOnEligibility.Hidden)
        }
    }

    @Test
    fun `a product that is not try-on capable shows nothing, whatever the licence says`() {
        everyState.forEach { state ->
            assertThat(tryOnEligibility(TryOnDescriptor.NOT_CAPABLE, state))
                .isEqualTo(TryOnEligibility.Hidden)
        }
    }

    @Test
    fun `a half-configured product shows nothing even with a live licence`() {
        val noSlug = capable.copy(effectSlug = "")
        val unknownKind = capable.copy(kind = TryOnKind.UNKNOWN)

        listOf(noSlug, unknownKind).forEach { descriptor ->
            assertThat(tryOnEligibility(descriptor, FaceArState.Ready))
                .isEqualTo(TryOnEligibility.Hidden)
        }
    }

    @Test
    fun `the product's own variant maps onto a try-on variant by id, and an unknown id maps to none`() {
        assertThat(capable.variantById("v-1")?.label).isEqualTo("Crimson")
        assertThat(capable.variantById("v-nope")).isNull()
        assertThat(capable.variantById(null)).isNull()
    }
}
