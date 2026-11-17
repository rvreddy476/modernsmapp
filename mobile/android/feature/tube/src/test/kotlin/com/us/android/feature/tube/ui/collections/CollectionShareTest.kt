package com.us.android.feature.tube.ui.collections

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.CollectionVisibility
import com.us.android.core.feed.data.VideoCollection
import org.junit.Test

/**
 * Share on a collection (lv-31), the web's `canShareCollection` and
 * `collectionShareUrl`: Public and Unlisted lists someone made share their
 * page; a Private list, Watch later and Liked videos have no Share.
 */
class CollectionShareTest {

    private fun list(visibility: CollectionVisibility, watchLater: Boolean = false, liked: Boolean = false) =
        VideoCollection(
            id = ID,
            title = "Road trips",
            itemCount = 3,
            isPrivate = visibility == CollectionVisibility.PRIVATE,
            isWatchLater = watchLater,
            visibility = visibility,
            isLiked = liked,
        )

    @Test
    fun `public and unlisted lists share their web page with a share ref`() {
        listOf(CollectionVisibility.PUBLIC, CollectionVisibility.UNLISTED).forEach { visibility ->
            val link = collectionShareLink(list(visibility))
            assertThat(link).startsWith("https://cleestudio.com/posttube/playlists/$ID?ref=")
            assertThat(link).endsWith("&utm_source=share")
        }
    }

    @Test
    fun `a private list and the system lists have no share at all`() {
        assertThat(collectionShareLink(list(CollectionVisibility.PRIVATE))).isNull()
        assertThat(collectionShareLink(list(CollectionVisibility.PUBLIC, watchLater = true))).isNull()
        assertThat(collectionShareLink(list(CollectionVisibility.PUBLIC, liked = true))).isNull()
    }

    @Test
    fun `the shared text is the title and then the link on its own line`() {
        assertThat(collectionShareText("https://x/y", " Road trips ")).isEqualTo("Road trips\nhttps://x/y")
        assertThat(collectionShareText("https://x/y", "")).isEqualTo("https://x/y")
    }

    private companion object {
        const val ID = "b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33"
    }
}
