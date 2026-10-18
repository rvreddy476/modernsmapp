package com.us.android.feature.post.studio

import com.google.common.truth.Truth.assertThat
import com.us.android.core.creator.model.PostDetails
import org.junit.Test

/** The details form's rules (2026-10-18): what the description carries, the helpers, the cover, the round trip. */
class PostDetailsStateTest {

    @Test
    fun `the description's tags and mentions are read in order, once each, capped`() {
        val (tags, names) = descriptionTags("Rain #Monsoon with @asha and #monsoon #walk @Asha a#b")
        assertThat(tags).containsExactly("Monsoon", "walk").inOrder()
        assertThat(names).containsExactly("asha")

        val many = (1..40).joinToString(" ") { "#t$it" }
        assertThat(descriptionTags(many).first).hasSize(30)
    }

    @Test
    fun `the hash chip starts a tag on a word boundary`() {
        assertThat(insertHashMark("")).isEqualTo("#")
        assertThat(insertHashMark("rain")).isEqualTo("rain #")
        assertThat(insertHashMark("rain ")).isEqualTo("rain #")
    }

    @Test
    fun `a picked mention replaces a bare at sign and leaves a space`() {
        assertThat(insertMention("with @", "asha")).isEqualTo("with @asha ")
        assertThat(insertMention("with", "@asha")).isEqualTo("with @asha ")
        assertThat(insertMention("", "asha")).isEqualTo("@asha ")
        assertThat(insertMention("with", " ")).isEqualTo("with")
    }

    @Test
    fun `every switch is sent, and the cover only for a carousel`() {
        val ui = PostDetailsUi(coverIndex = 1, link = "https://example.com")
        val one = ui.toDetails("hi", pageCount = 1)
        assertThat(one.coverIndex).isNull()
        assertThat(one.allowComments).isTrue()
        assertThat(one.allowReuse).isTrue()
        assertThat(one.aiGenerated).isFalse()
        assertThat(one.linkUrl).isEqualTo("https://example.com")
        assertThat(ui.toDetails("hi", pageCount = 3).coverIndex).isEqualTo(1)
    }

    @Test
    fun `a bad link stops Post and is not sent`() {
        val ui = PostDetailsUi(link = "example.com")
        assertThat(ui.valid).isFalse()
        assertThat(ui.toDetails("", 1).linkUrl).isNull()
        assertThat(PostDetailsUi(link = "").valid).isTrue()
    }

    @Test
    fun `the cover goes back to the first photo when its photo is removed`() {
        assertThat(PostDetailsUi(coverIndex = 2).withPageCount(2).coverIndex).isEqualTo(0)
        assertThat(PostDetailsUi(coverIndex = 1).withPageCount(2).coverIndex).isEqualTo(1)
    }

    @Test
    fun `a saved draft's details come back as the form left them`() {
        val details = PostDetailsUi(
            title = "T",
            visibility = "private",
            allowComments = false,
            allowReuse = false,
            paidPromotion = true,
            isBranded = true,
            aiGenerated = true,
            link = "https://example.com",
            locationName = "Goa",
            coverIndex = 1,
        )
        val back = PostDetailsUi.from(details.toDetails("text", pageCount = 2))
        assertThat(back).isEqualTo(details)
        // Nothing saved reads as the form's defaults.
        assertThat(PostDetailsUi.from(PostDetails())).isEqualTo(PostDetailsUi())
        // An audience the form does not offer reads as public.
        assertThat(PostDetailsUi.from(PostDetails(visibility = "unlisted")).visibility).isEqualTo("public")
    }

    @Test
    fun `the audience row says who can view`() {
        assertThat(audienceLabel("public")).isEqualTo("Everyone can view this post")
        assertThat(audienceLabel("friends")).isEqualTo("Friends can view this post")
        assertThat(audienceLabel("private")).isEqualTo("Only you can view this post")
        assertThat(AUDIENCES).containsExactly("public", "friends", "private").inOrder()
    }

    @Test
    fun `recent places are newest first, once each, five at most`() {
        val five = listOf("a", "b", "c", "d", "e")
        assertThat(rememberLocation(five, " C ")).containsExactly("C", "a", "b", "d", "e").inOrder()
        assertThat(rememberLocation(five, "f")).containsExactly("f", "a", "b", "c", "d").inOrder()
        assertThat(rememberLocation(five, "  ")).isEqualTo(five)
    }
}
