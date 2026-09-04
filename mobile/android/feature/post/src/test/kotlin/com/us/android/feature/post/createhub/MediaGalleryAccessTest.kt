package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The gallery's two pure rules: how much the app may read (from the grant
 * map), and what order the Recents grid puts its tiles in.
 *
 * The access rule is where the founder's phone went wrong (2026-09-04): it
 * held Android 14's "Select photos" grant with nothing selected, the old
 * "any grant counts" rule called that a working gallery, and the screen was
 * an empty grid with one Camera tile. Partial is its own state now, with
 * its own banner, and these are the table that says which grants are which.
 */
class MediaGalleryAccessTest {

    private val images = "android.permission.READ_MEDIA_IMAGES"
    private val video = "android.permission.READ_MEDIA_VIDEO"
    private val legacy = "android.permission.READ_EXTERNAL_STORAGE"
    private val selected = PARTIAL_ACCESS_PERMISSION

    // ── Access ──────────────────────────────────────────────────────────

    @Test
    fun `a media permission is full access, with or without the selected one`() {
        assertThat(mediaAccessOf(mapOf(images to true, selected to true))).isEqualTo(MediaAccess.Full)
        assertThat(mediaAccessOf(mapOf(images to true, selected to false))).isEqualTo(MediaAccess.Full)
        assertThat(mediaAccessOf(mapOf(video to true, selected to true))).isEqualTo(MediaAccess.Full)
    }

    /** Before Android 13 there is one storage permission and no partial state. */
    @Test
    fun `legacy storage permission is full access`() {
        assertThat(mediaAccessOf(mapOf(legacy to true))).isEqualTo(MediaAccess.Full)
    }

    /** The founder's phone: only "Select photos" held. A gallery, but a partial one. */
    @Test
    fun `only the user-selected permission is partial access`() {
        assertThat(mediaAccessOf(mapOf(images to false, selected to true))).isEqualTo(MediaAccess.Partial)
        assertThat(mediaAccessOf(mapOf(video to false, selected to true))).isEqualTo(MediaAccess.Partial)
    }

    @Test
    fun `nothing granted is denied`() {
        assertThat(mediaAccessOf(mapOf(images to false, selected to false))).isEqualTo(MediaAccess.Denied)
        assertThat(mediaAccessOf(mapOf(legacy to false))).isEqualTo(MediaAccess.Denied)
        assertThat(mediaAccessOf(emptyMap())).isEqualTo(MediaAccess.Denied)
    }

    /** The constant is the platform's own name, byte for byte: a typo here is silent partial-blindness. */
    @Test
    fun `the partial permission is the platform's READ_MEDIA_VISUAL_USER_SELECTED`() {
        assertThat(PARTIAL_ACCESS_PERMISSION).isEqualTo("android.permission.READ_MEDIA_VISUAL_USER_SELECTED")
    }

    // ── Tiles ───────────────────────────────────────────────────────────

    /** Camera first, Browse second, then the media in the order it was queried (newest first). */
    @Test
    fun `the grid is camera, browse, then the media in query order`() {
        assertThat(galleryTiles(listOf("newest", "older", "oldest")))
            .containsExactly(
                GalleryTile.Camera,
                GalleryTile.Browse,
                GalleryTile.Media("newest"),
                GalleryTile.Media("older"),
                GalleryTile.Media("oldest"),
            )
            .inOrder()
    }

    /** With nothing to show — partial with no selection, or an empty library — both action tiles remain. */
    @Test
    fun `an empty library still offers camera and browse`() {
        assertThat(galleryTiles(emptyList<String>()))
            .containsExactly(GalleryTile.Camera, GalleryTile.Browse)
            .inOrder()
    }
}
