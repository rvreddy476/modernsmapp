package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.network.di.NetworkModule
import org.junit.Test
import java.io.File

/**
 * feed-service's photo-carousel row from the post-screens lane (BP,
 * 795058cf), copied byte for byte and read the way the paging source reads
 * it: the share and save counts the rail prints, and the disclosures the
 * caption labels.
 */
class PostScreensFeedRowFixtureTest {

    private val app = NetworkModule.provideJson()
    private val copy = File("src/test/resources/contracts/posts/feed_row_photo_carousel.json")
    private val source = File("../../../../Architecture/services/feed-service/internal/service/testdata/contracts/posts/feed_row_photo_carousel.json")

    @Test
    fun `the copy is byte-identical to feed-service's golden`() {
        assertThat(copy.readBytes()).isEqualTo(source.readBytes())
    }

    @Test
    fun `the carousel row maps its counts, cover and disclosures`() {
        val item = app.decodeFromString(FeedItemDto.serializer(), copy.readText()).toDomain()

        assertThat(item.media.map { it.position }).containsExactly(0, 1, 2).inOrder()
        assertThat(item.counts.likes).isEqualTo(128)
        assertThat(item.counts.comments).isEqualTo(14)
        assertThat(item.counts.shares).isEqualTo(6)
        assertThat(item.counts.saves).isEqualTo(21)
        assertThat(item.controls.coverMediaId).isEqualTo("c4444444-4444-4444-8444-444444444444")
        assertThat(item.controls.commentAccess).isEqualTo("followers")
        assertThat(item.disclosure.isBranded).isTrue()
        assertThat(item.disclosure.aiGenerated).isTrue()
        assertThat(item.disclosure.paidPromotion).isFalse()
        assertThat(item.disclosure.linkUrl).isEqualTo("https://example.com/sunday-market")
        assertThat(item.title).isEqualTo("Sunday market")
        assertThat(item.hashtags).containsExactly("food", "market").inOrder()
    }

    @Test
    fun `a row from before the disclosures reads as nothing declared`() {
        val item = app.decodeFromString(FeedItemDto.serializer(), """{"id":"p","author_id":"a"}""").toDomain()
        assertThat(item.disclosure.isBranded).isFalse()
        assertThat(item.disclosure.aiGenerated).isFalse()
        assertThat(item.disclosure.linkUrl).isNull()
        assertThat(item.controls.commentAccess).isEmpty()
    }
}
