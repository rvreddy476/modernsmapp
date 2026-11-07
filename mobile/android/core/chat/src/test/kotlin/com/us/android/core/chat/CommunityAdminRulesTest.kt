package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunityAdminFailure
import com.us.android.core.chat.data.CommunityApi
import com.us.android.core.chat.data.CommunityMemberDto
import com.us.android.core.chat.data.CommunityMembershipApi
import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.CommunityRepository.Companion.MAX_ROSTER_PAGES
import com.us.android.core.chat.data.CommunityRepository.Companion.ROSTER_PAGE
import com.us.android.core.chat.data.adminCandidatesOf
import com.us.android.core.chat.data.isCommunityUnavailable
import com.us.android.core.chat.data.toCommunityAdminFailure
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * Communities (channel-service, 7 Nov 2026): who may be made an admin, and
 * what each refusal of an admin change means.
 */
class CommunityAdminRulesTest {

    @Test
    fun `only members who are not owner, admin or banned may be made admins`() {
        val roster = listOf(
            CommunityMemberDto("owner-1", "owner"),
            CommunityMemberDto("admin-1", "admin"),
            CommunityMemberDto("banned-1", "banned"),
            CommunityMemberDto("member-1", "subscriber"),
            CommunityMemberDto("mod-1", "Moderator"),
            CommunityMemberDto("", "subscriber"),
            CommunityMemberDto("member-1", "subscriber"),
        )

        assertThat(adminCandidatesOf(roster)).containsExactly("member-1", "mod-1").inOrder()
    }

    @Test
    fun `each refusal of an admin change is told apart`() {
        assertThat(AppError.Unknown(code = "ADMIN_MUST_BE_MEMBER", statusCode = 422).toCommunityAdminFailure())
            .isEqualTo(CommunityAdminFailure.NotAMember)
        assertThat(AppError.Unknown(code = "MEMBER_BANNED", statusCode = 422).toCommunityAdminFailure())
            .isEqualTo(CommunityAdminFailure.Banned)
        assertThat(AppError.Forbidden(code = "FORBIDDEN").toCommunityAdminFailure())
            .isEqualTo(CommunityAdminFailure.NotAllowed)
        assertThat(AppError.NotFound(code = "NOT_FOUND").toCommunityAdminFailure())
            .isEqualTo(CommunityAdminFailure.Unavailable)
        assertThat(AppError.Unknown(code = "VALIDATION_ERROR", statusCode = 422).toCommunityAdminFailure())
            .isEqualTo(CommunityAdminFailure.Other)
        assertThat(AppError.NoNetwork().toCommunityAdminFailure()).isEqualTo(CommunityAdminFailure.Other)
    }

    @Test
    fun `a 404 means not there for this viewer - private, removed or suspended alike`() {
        assertThat(AppError.NotFound(code = "NOT_FOUND").isCommunityUnavailable()).isTrue()
        assertThat(AppError.NotFound().isCommunityUnavailable()).isTrue()
        assertThat(AppError.Server(statusCode = 503, code = null).isCommunityUnavailable()).isFalse()
        assertThat(AppError.Forbidden(code = "INVITE_REQUIRED").isCommunityUnavailable()).isFalse()
    }

    // ── the roster read the picker offers from ────────────────────────────

    /** Answers roster pages in order; past the script, the read fails. */
    private class RosterApi(
        private val pages: List<() -> List<CommunityMemberDto>>,
    ) : CommunityMembershipApi by unscripted<CommunityMembershipApi>() {
        val offsets = mutableListOf<Int>()

        override suspend fun members(id: String, limit: Int, offset: Int): ApiEnvelope<List<CommunityMemberDto>> {
            offsets += offset
            val page = pages.getOrNull(offsets.size - 1) ?: throw IOException("offline")
            return ApiEnvelope(data = page())
        }
    }

    private fun repository(api: RosterApi) =
        CommunityRepository(unscripted<CommunityApi>(), api, ErrorMapper(Json { ignoreUnknownKeys = true }))

    private fun fullPage(from: Int): () -> List<CommunityMemberDto> =
        { (from until from + ROSTER_PAGE).map { CommunityMemberDto("m$it", "subscriber") } }

    private val failing: () -> List<CommunityMemberDto> = { throw IOException("offline") }

    @Test
    fun `the roster is read until a short page`() = runTest {
        val api = RosterApi(listOf(fullPage(0), { listOf(CommunityMemberDto("last", "subscriber")) }))

        val candidates = (repository(api).adminCandidates("c1") as AppResult.Success).data

        assertThat(api.offsets).containsExactly(0, ROSTER_PAGE).inOrder()
        assertThat(candidates).hasSize(ROSTER_PAGE + 1)
        assertThat(candidates.last()).isEqualTo("last")
    }

    @Test
    fun `the roster read stops at its page cap`() = runTest {
        val api = RosterApi(List(MAX_ROSTER_PAGES + 2) { fullPage(it * ROSTER_PAGE) })

        val candidates = (repository(api).adminCandidates("c1") as AppResult.Success).data

        assertThat(api.offsets).hasSize(MAX_ROSTER_PAGES)
        assertThat(candidates).hasSize(MAX_ROSTER_PAGES * ROSTER_PAGE)
    }

    @Test
    fun `a first page that fails is a failure, a later one keeps what was read`() = runTest {
        val first = repository(RosterApi(listOf(failing))).adminCandidates("c1")
        val later = repository(RosterApi(listOf(fullPage(0), failing))).adminCandidates("c1")

        assertThat(first).isInstanceOf(AppResult.Failure::class.java)
        assertThat((later as AppResult.Success).data).hasSize(ROSTER_PAGE)
    }
}

private inline fun <reified T : Any> unscripted(): T = java.lang.reflect.Proxy.newProxyInstance(
    T::class.java.classLoader,
    arrayOf(T::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as T
