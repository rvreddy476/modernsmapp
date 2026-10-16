package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.FileReportRequest
import com.us.android.core.engagement.data.ReportApi
import com.us.android.core.engagement.data.ReportDto
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.model.PersonalProfile
import com.us.android.core.model.Profile
import com.us.android.core.model.ProfileCounts
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReportReason
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * Reporting an account from its profile (audit T-03): trust-safety receives
 * entity type `user` and the PROFILE's user id, through the same
 * [ReportRepository] a post's report uses — and the viewer's own profile can
 * never be reported.
 */
class ProfileReportViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val mapper = ErrorMapper(Json { ignoreUnknownKeys = true })

    private class FakeReportApi : ReportApi {
        val reports = mutableListOf<FileReportRequest>()
        var alreadyReported = false
        var offline = false

        override suspend fun fileReport(body: FileReportRequest): ApiEnvelope<ReportDto> {
            reports += body
            if (offline) throw java.io.IOException("offline")
            if (alreadyReported) {
                throw HttpException(
                    Response.error<Any>(
                        409,
                        """{"error":{"code":"ACTIVE_REPORT_EXISTS","message":"already"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
            return ApiEnvelope(data = ReportDto(id = "r1", status = "open"), meta = null)
        }
    }

    private fun viewModel(api: FakeReportApi) = ProfileReportViewModel(ReportRepository(api, mapper))

    @Test
    fun `a report on someone's profile files entity user with the profile's user id`() = runTest {
        val api = FakeReportApi()
        val vm = viewModel(api)

        vm.report(otherProfile, UsReportReason.IMPERSONATION, "pretending to be me")

        assertThat(api.reports).containsExactly(
            FileReportRequest(
                entityType = "user",
                entityId = OTHER_USER_ID,
                reason = "impersonation",
                details = "pretending to be me",
            ),
        )
        assertThat(ReportRepository.ENTITY_USER).isEqualTo("user")
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Sent)
    }

    @Test
    fun `an account already reported says so rather than failing`() = runTest {
        val api = FakeReportApi().apply { alreadyReported = true }
        val vm = viewModel(api)

        vm.report(otherProfile, UsReportReason.SPAM, "")

        assertThat(vm.report.value).isEqualTo(UsPostReportState.AlreadyReported)
    }

    @Test
    fun `a failed report can be tried again`() = runTest {
        val api = FakeReportApi().apply { offline = true }
        val vm = viewModel(api)

        vm.report(otherProfile, UsReportReason.SPAM, "")
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Failed)

        api.offline = false
        vm.report(otherProfile, UsReportReason.SPAM, "")
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Sent)
        assertThat(api.reports).hasSize(2)
    }

    @Test
    fun `the viewer's own profile is never reported`() = runTest {
        val api = FakeReportApi()
        val vm = viewModel(api)

        vm.report(ownProfile, UsReportReason.SPAM, "")

        assertThat(api.reports).isEmpty()
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Idle)
    }

    @Test
    fun `opening the menu again starts a fresh report`() = runTest {
        val vm = viewModel(FakeReportApi())
        vm.report(otherProfile, UsReportReason.SPAM, "")
        assertThat(vm.report.value).isEqualTo(UsPostReportState.Sent)

        vm.reset()

        assertThat(vm.report.value).isEqualTo(UsPostReportState.Idle)
    }

    private companion object {
        const val OTHER_USER_ID = "03b43cb4-420b-4d2d-baae-2ade11490b19"

        val otherProfile = Profile(
            userId = OTHER_USER_ID,
            displayName = "Ada Lovelace",
            bio = "",
            category = "personal",
            profession = "",
            website = "",
            location = "",
            badgeFlags = 0,
            isVerified = false,
            verificationLevel = "",
            statusText = "",
            statusEmoji = "",
            profileThemeColor = "",
            memberSinceBadge = false,
            counts = ProfileCounts(followers = 0, following = 0, friends = 0, posts = 0),
            createdAt = "2026-08-16T17:53:05Z",
        )

        /** `personal` is only present on the owner's own payload — that is what makes it "own". */
        val ownProfile = otherProfile.copy(
            userId = "66668bc2-a3f6-40a5-9cdd-c998dcf72f29",
            personal = PersonalProfile(
                firstName = "Me",
                lastName = "",
                dateOfBirth = "",
                gender = "",
                timezone = "",
                introMediaUrl = "",
                ctaUrl = "",
                updatedAt = "",
            ),
        )
    }
}
