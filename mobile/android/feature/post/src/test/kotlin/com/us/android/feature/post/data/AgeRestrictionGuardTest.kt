package com.us.android.feature.post.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.PostEditApi
import com.us.android.core.feed.data.PostEditDetailDto
import com.us.android.core.feed.data.PostEditPatch
import com.us.android.core.network.ApiEnvelope
import kotlinx.coroutines.test.runTest
import org.junit.Test
import java.io.IOException

/**
 * lv-9: when the create did not confirm 18+, the gate is set by PATCH (twice)
 * and, failing that, the video is made private — never left public and
 * ungated in silence.
 */
class AgeRestrictionGuardTest {

    private class FakeEditApi(
        /** Answers for the age PATCHes in order; an exception fails that call. */
        val ageAnswers: ArrayDeque<Any> = ArrayDeque(),
        var privateFails: Boolean = false,
    ) : PostEditApi {
        val patches = mutableListOf<PostEditPatch>()

        override suspend fun detail(postId: String): ApiEnvelope<PostEditDetailDto> = error("not used")

        override suspend fun update(postId: String, body: PostEditPatch): ApiEnvelope<PostEditDetailDto> {
            patches += body
            if (body.visibility != null) {
                if (privateFails) throw IOException("offline")
                return ApiEnvelope(PostEditDetailDto(id = postId, visibility = body.visibility!!))
            }
            return when (val next = ageAnswers.removeFirstOrNull() ?: IOException("offline")) {
                is Throwable -> throw next
                is Boolean -> ApiEnvelope(PostEditDetailDto(id = postId, ageRestricted = next))
                else -> error("bad answer")
            }
        }
    }

    @Test
    fun `a PATCH that answers age_restricted true confirms it`() = runTest {
        val api = FakeEditApi(ArrayDeque(listOf(true)))

        val outcome = PatchAgeRestrictionGuard(api).secure("p1", "public")

        assertThat(outcome).isEqualTo(AgeGateOutcome.Confirmed)
        assertThat(api.patches).containsExactly(PostEditPatch(ageRestricted = true))
    }

    @Test
    fun `one failed PATCH is retried before anything else`() = runTest {
        val api = FakeEditApi(ArrayDeque(listOf(IOException("blip"), true)))

        assertThat(PatchAgeRestrictionGuard(api).secure("p1", "public")).isEqualTo(AgeGateOutcome.Confirmed)
        assertThat(api.patches).hasSize(2)
    }

    @Test
    fun `two failures make a public video private and say so`() = runTest {
        val api = FakeEditApi()

        val outcome = PatchAgeRestrictionGuard(api).secure("p1", "public")

        assertThat(outcome).isEqualTo(AgeGateOutcome.Private(AgeGateOutcome.MADE_PRIVATE))
        assertThat(api.patches.last()).isEqualTo(PostEditPatch(visibility = "private"))
    }

    @Test
    fun `an unlisted video is made private too`() = runTest {
        val api = FakeEditApi()

        assertThat(PatchAgeRestrictionGuard(api).secure("p1", "unlisted"))
            .isEqualTo(AgeGateOutcome.Private(AgeGateOutcome.MADE_PRIVATE))
        assertThat(api.patches.last()).isEqualTo(PostEditPatch(visibility = "private"))
    }

    @Test
    fun `a server that answers false is not asked again and the video goes private`() = runTest {
        val api = FakeEditApi(ArrayDeque(listOf(false, true)))

        val outcome = PatchAgeRestrictionGuard(api).secure("p1", "followers")

        assertThat(outcome).isEqualTo(AgeGateOutcome.Private(AgeGateOutcome.MADE_PRIVATE))
        assertThat(api.patches).containsExactly(
            PostEditPatch(ageRestricted = true),
            PostEditPatch(visibility = "private"),
        ).inOrder()
    }

    @Test
    fun `an already private video stays private without another call`() = runTest {
        val api = FakeEditApi()

        val outcome = PatchAgeRestrictionGuard(api).secure("p1", "private")

        assertThat(outcome).isEqualTo(AgeGateOutcome.Private(AgeGateOutcome.ALREADY_PRIVATE))
        assertThat(api.patches.none { it.visibility != null }).isTrue()
    }

    @Test
    fun `when even private fails the creator is told the video may be exposed`() = runTest {
        val api = FakeEditApi(privateFails = true)

        val outcome = PatchAgeRestrictionGuard(api).secure("p1", "public")

        assertThat(outcome).isEqualTo(AgeGateOutcome.Exposed(AgeGateOutcome.EXPOSED))
        assertThat(outcome.notice).contains("Creator Hub")
    }
}
