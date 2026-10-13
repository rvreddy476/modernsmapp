package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.commerce.seller.MAX_PRODUCT_IMAGES
import com.us.android.feature.commerce.seller.ProductImageDraft
import com.us.android.feature.commerce.seller.addImages
import com.us.android.feature.commerce.seller.coverKey
import com.us.android.feature.commerce.seller.moveImage
import com.us.android.feature.commerce.seller.readyMediaIds
import com.us.android.feature.commerce.seller.removeImage
import com.us.android.feature.commerce.seller.wouldExceedCap
import org.junit.Test

/**
 * The seller's gallery: the cap, the ordering, and what gets sent.
 *
 * These are the parts a seller notices when they are wrong — nine photos
 * picked and one silently dropped, or a reorder that moves the wrong picture —
 * so they are pure functions with a table test rather than logic inside a
 * ViewModel that only a device can exercise.
 */
class ProductImagesTest {

    private fun drafts(vararg uris: String) = uris.map { ProductImageDraft(uri = it) }

    @Test
    fun `the cap is eight`() {
        assertThat(MAX_PRODUCT_IMAGES).isEqualTo(8)
    }

    @Test
    fun `picking keeps the seller's order`() {
        val added = addImages(emptyList(), listOf("a", "b", "c"))
        assertThat(added.map { it.uri }).containsExactly("a", "b", "c").inOrder()
    }

    @Test
    fun `picking past the cap takes what fits and refuses the rest`() {
        val eleven = (1..11).map { "u$it" }
        val added = addImages(emptyList(), eleven)

        assertThat(added).hasSize(MAX_PRODUCT_IMAGES)
        assertThat(added.map { it.uri })
            .containsExactlyElementsIn(eleven.take(MAX_PRODUCT_IMAGES))
            .inOrder()

        // And the caller can SAY so. Silently dropping the ninth photo is the
        // version of this a seller discovers on the product page.
        assertThat(wouldExceedCap(emptyList(), eleven.size)).isTrue()
        assertThat(wouldExceedCap(emptyList(), MAX_PRODUCT_IMAGES)).isFalse()
    }

    @Test
    fun `a full gallery accepts nothing more`() {
        val full = drafts(*(1..MAX_PRODUCT_IMAGES).map { "u$it" }.toTypedArray())
        assertThat(addImages(full, listOf("extra"))).isEqualTo(full)
    }

    @Test
    fun `the same photo picked twice is added once`() {
        val current = drafts("a", "b")
        val added = addImages(current, listOf("b", "c", "c"))
        assertThat(added.map { it.uri }).containsExactly("a", "b", "c").inOrder()
    }

    @Test
    fun `the first image is the cover`() {
        val current = drafts("a", "b", "c")
        assertThat(coverKey(current)).isEqualTo("a")
        assertThat(coverKey(emptyList())).isNull()
    }

    @Test
    fun `moving shifts one image and leaves the rest in order`() {
        val current = drafts("a", "b", "c")

        assertThat(moveImage(current, "c", -1).map { it.uri })
            .containsExactly("a", "c", "b").inOrder()
        assertThat(moveImage(current, "a", 1).map { it.uri })
            .containsExactly("b", "a", "c").inOrder()

        // Moving to the front is how "make cover" is expressed: one source of
        // truth for the cover, not a separate flag that can disagree.
        val promoted = moveImage(current, "c", -2)
        assertThat(promoted.map { it.uri }).containsExactly("c", "a", "b").inOrder()
        assertThat(coverKey(promoted)).isEqualTo("c")
    }

    @Test
    fun `a move off either end is a no-op, not a crash`() {
        val current = drafts("a", "b", "c")
        assertThat(moveImage(current, "a", -1)).isEqualTo(current)
        assertThat(moveImage(current, "c", 1)).isEqualTo(current)
        assertThat(moveImage(current, "missing", 1)).isEqualTo(current)
    }

    @Test
    fun `removing takes exactly one`() {
        val current = drafts("a", "b", "c")
        assertThat(removeImage(current, "b").map { it.uri })
            .containsExactly("a", "c").inOrder()
        assertThat(removeImage(current, "missing")).isEqualTo(current)
    }

    /**
     * The order of the ids IS the gallery order, and anything still uploading
     * is left out — attaching a half-uploaded gallery would silently publish
     * the product without the photos the seller is watching finish.
     */
    @Test
    fun `only confirmed images are sent, cover first`() {
        val gallery = listOf(
            ProductImageDraft(uri = "a", mediaId = "m-a"),
            ProductImageDraft(uri = "b"),
            ProductImageDraft(uri = "c", mediaId = "m-c"),
        )
        assertThat(readyMediaIds(gallery)).containsExactly("m-a", "m-c").inOrder()
    }

    @Test
    fun `progress is reported only while bytes are moving`() {
        assertThat(ProductImageDraft(uri = "a", uploaded = 50, total = 100).progress)
            .isEqualTo(0.5f)

        // Nothing known yet.
        assertThat(ProductImageDraft(uri = "a").progress).isNull()

        // Done: the card shows the picture, not a full bar.
        assertThat(ProductImageDraft(uri = "a", mediaId = "m", uploaded = 100, total = 100).progress)
            .isNull()
    }
}
