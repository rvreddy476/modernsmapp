package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.EditProfileField
import com.us.android.core.profile.data.ProfileApi
import com.us.android.core.profile.data.ProfileClock
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.core.profile.data.dto.FollowRequestDto
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileMediaUpdateDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.RelationshipDto
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test
import java.time.Instant
import java.time.LocalDate

class EditProfileViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    /**
     * A hand-written fake rather than a mocking framework: the assertions here
     * are about what the request body CONTAINED, and keeping the last request
     * states that far more directly than a verify() argument captor.
     */
    private class FakeApi : ProfileApi {
        var ownProfile: ApiEnvelope<OwnProfileDto> = ApiEnvelope(LOADED)
        var updateResult: ApiEnvelope<OwnProfileDto>? = null
        val calls = mutableListOf<String>()
        var lastUpdate: UpdateProfileRequest? = null

        override suspend fun getProfile(userId: String) = ApiEnvelope(PublicProfileDto(userId = userId))

        override suspend fun getOwnProfile() = ownProfile.also { calls += "getOwnProfile" }

        override suspend fun getStats(userId: String) = ApiEnvelope(ProfileStatsDto())

        override suspend fun updateProfile(body: UpdateProfileRequest): ApiEnvelope<OwnProfileDto> {
            calls += "updateProfile"
            lastUpdate = body
            // Echoing the request back is what the live server does, and it is
            // what lets the "form is re-seeded from the response" test mean
            // something.
            return updateResult ?: ApiEnvelope(
                LOADED.copy(
                    displayName = body.displayName,
                    bio = body.bio,
                    category = body.category,
                    profession = body.profession,
                    website = body.website,
                    location = body.location,
                    profileThemeColor = body.profileThemeColor,
                ),
            )
        }

        override suspend fun updateAvatar(body: UpdateMediaIdRequest) =
            ApiEnvelope(ProfileMediaUpdateDto(avatarMediaId = body.mediaId))

        override suspend fun updateCover(body: UpdateMediaIdRequest) =
            ApiEnvelope(ProfileMediaUpdateDto(coverMediaId = body.mediaId))

        override suspend fun relationship(userId: String, otherId: String) =
            ApiEnvelope(RelationshipDto())

        override suspend fun follow(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("followed"))

        override suspend fun unfollow(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("unfollowed"))

        override suspend fun block(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("blocked"))

        override suspend fun unblock(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("unblocked"))

        override suspend fun cancelFollowRequest(targetId: String) = ApiEnvelope(GraphStatusDto("cancelled"))

        override suspend fun incomingFollowRequests(limit: Int, cursor: String?) =
            ApiEnvelope(emptyList<FollowRequestDto>())

        override suspend fun acceptFollowRequest(requesterId: String) = ApiEnvelope(GraphStatusDto("accepted"))

        override suspend fun declineFollowRequest(requesterId: String) = ApiEnvelope(GraphStatusDto("declined"))

        companion object {
            /** The 2026-08-17 repair viewer's `/me` payload. */
            val LOADED = OwnProfileDto(
                userId = "719e2958-f412-44ca-b94a-b00060a7fccb",
                displayName = "Android Repair",
                firstName = "Android",
                lastName = "Repair",
                bio = "Native bearer contract verified",
                category = "personal",
                profession = "android-contract",
                website = "",
                location = "",
                profileThemeColor = "#1A73E8",
            )
        }
    }

    /** 00:30 IST on 2026-09-15 — still the 14th in UTC. */
    private val clock = ProfileClock { Instant.parse("2026-09-14T19:00:00Z") }

    private fun viewModel(api: FakeApi) =
        EditProfileViewModel(ProfileRepository(api, ErrorMapper(json)), clock)

    private fun editingState(vm: EditProfileViewModel) =
        vm.state.value as EditProfileUiState.Editing

    // ── Seeding ─────────────────────────────────────────────────────────

    @Test
    fun `the form is seeded from the loaded me snapshot`() = runTest {
        val api = FakeApi()

        val state = editingState(viewModel(api))

        assertThat(api.calls).contains("getOwnProfile")
        assertThat(state.form.displayName).isEqualTo("Android Repair")
        assertThat(state.form.profession).isEqualTo("android-contract")
        assertThat(state.form.profileThemeColor).isEqualTo("#1A73E8")
        assertThat(state.isDirty).isFalse()
    }

    /**
     * No editable form without a snapshot. Opening an empty form on a failed
     * load would let a full-replacement save write blanks over everything.
     */
    @Test
    fun `a failed load yields an error, never an empty form`() = runTest {
        val api = FakeApi().apply {
            ownProfile = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }

        val state = viewModel(api).state.value

        assertThat(state).isInstanceOf(EditProfileUiState.Error::class.java)
        assertThat((state as EditProfileUiState.Error).retryable).isTrue()
    }

    // ── Dirty tracking ──────────────────────────────────────────────────

    @Test
    fun `editing a field marks the form dirty and enables save`() = runTest {
        val vm = viewModel(FakeApi())

        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        val state = editingState(vm)
        assertThat(state.isDirty).isTrue()
        assertThat(state.canSave).isTrue()
    }

    /** Dirty is a value comparison, so typing back the original clears it. */
    @Test
    fun `restoring the loaded value clears the dirty flag`() = runTest {
        val vm = viewModel(FakeApi())
        vm.onFieldChange(EditProfileField.DISPLAY_NAME, "Something else")

        vm.onFieldChange(EditProfileField.DISPLAY_NAME, "Android Repair")

        assertThat(editingState(vm).isDirty).isFalse()
    }

    @Test
    fun `a pristine form cannot be saved and issues no request`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)

        vm.save()

        assertThat(editingState(vm).canSave).isFalse()
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    // ── The full-snapshot contract ──────────────────────────────────────

    /**
     * The single most important assertion in this feature. One field was
     * edited; all seven must still be on the wire, six of them carrying the
     * values `/me` returned. Anything less and the server clears them.
     */
    @Test
    fun `saving sends every field, not just the edited one`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        val sent = requireNotNull(api.lastUpdate)
        assertThat(sent.location).isEqualTo("Hyderabad")
        assertThat(sent.displayName).isEqualTo("Android Repair")
        assertThat(sent.bio).isEqualTo("Native bearer contract verified")
        assertThat(sent.category).isEqualTo("personal")
        assertThat(sent.profession).isEqualTo("android-contract")
        assertThat(sent.profileThemeColor).isEqualTo("#1A73E8")
        assertThat(sent.website).isEmpty()
    }

    /**
     * Clearing a field is a legitimate edit and must reach the server as an
     * empty string. This is the case a "only send what changed AND is
     * non-blank" optimisation would break.
     */
    @Test
    fun `clearing a field sends it as an empty string`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.BIO, "")

        vm.save()

        val sent = requireNotNull(api.lastUpdate)
        assertThat(sent.bio).isEmpty()
        assertThat(sent.displayName).isEqualTo("Android Repair")
    }

    // ── Validation ──────────────────────────────────────────────────────

    @Test
    fun `an invalid theme colour blocks the request and marks the field`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.THEME_COLOR, "blue")

        vm.save()

        val state = editingState(vm)
        assertThat(state.errorFor(EditProfileField.THEME_COLOR)).isNotNull()
        assertThat(state.message).isNotNull()
        assertThat(state.isSaving).isFalse()
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    @Test
    fun `an invalid website blocks the request and marks the field`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.WEBSITE, "not a url")

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.WEBSITE)).isNotNull()
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    @Test
    fun `an over-long bio blocks the request`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.BIO, "x".repeat(301))

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.BIO)).isNotNull()
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    /**
     * Blank must stay valid on every field. The server permits it, real
     * accounts are in that state, and this is the only screen that can undo it.
     */
    @Test
    fun `blank fields are valid and save normally`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.DISPLAY_NAME, "")
        vm.onFieldChange(EditProfileField.PROFESSION, "")
        vm.onFieldChange(EditProfileField.THEME_COLOR, "")

        vm.save()

        assertThat(api.calls).contains("updateProfile")
        assertThat(editingState(vm).fieldErrors).isEmpty()
    }

    @Test
    fun `editing a marked field clears its error`() = runTest {
        val vm = viewModel(FakeApi())
        vm.onFieldChange(EditProfileField.WEBSITE, "not a url")
        vm.save()

        vm.onFieldChange(EditProfileField.WEBSITE, "example.com")

        assertThat(editingState(vm).errorFor(EditProfileField.WEBSITE)).isNull()
    }

    // ── Save outcomes ───────────────────────────────────────────────────

    @Test
    fun `a successful save re-seeds the form from the response and clears dirty`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        val state = editingState(vm)
        assertThat(state.saved).isTrue()
        assertThat(state.isSaving).isFalse()
        assertThat(state.original.location).isEqualTo("Hyderabad")
        assertThat(state.isDirty).isFalse()
    }

    /**
     * A rejected save changed nothing server-side, so the form must keep every
     * character the user typed — losing a page of edits to a transient failure
     * is worse than the failure.
     */
    @Test
    fun `a failed save reports and preserves the edited form`() = runTest {
        val api = FakeApi().apply {
            updateResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        val state = editingState(vm)
        assertThat(state.message).isNotNull()
        assertThat(state.isSaving).isFalse()
        assertThat(state.saved).isFalse()
        assertThat(state.form.location).isEqualTo("Hyderabad")
        assertThat(state.isDirty).isTrue()
    }

    /** Retrying after a failure sends the same complete snapshot again. */
    @Test
    fun `retrying a failed save sends the full snapshot again`() = runTest {
        val api = FakeApi().apply {
            updateResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")
        vm.save()

        api.updateResult = null
        vm.save()

        val sent = requireNotNull(api.lastUpdate)
        assertThat(sent.location).isEqualTo("Hyderabad")
        assertThat(sent.displayName).isEqualTo("Android Repair")
        assertThat(editingState(vm).saved).isTrue()
    }

    @Test
    fun `dismissing the message leaves the form untouched`() = runTest {
        val api = FakeApi().apply {
            updateResult = ApiEnvelope(error = ApiErrorBody(code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")
        vm.save()

        vm.dismissMessage()

        val state = editingState(vm)
        assertThat(state.message).isNull()
        assertThat(state.form.location).isEqualTo("Hyderabad")
    }

    // ── Identity fields: profile-service's 422 rules ────────────────────

    @Test
    fun `a first name with digits blocks the request and marks the field`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.FIRST_NAME, "Raj2")

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.FIRST_NAME)).isNotNull()
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    @Test
    fun `a 51 character first name blocks the request`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.FIRST_NAME, "a".repeat(51))

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.FIRST_NAME)).isEqualTo("Use 50 characters or fewer")
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    @Test
    fun `a 50 character first name is sent`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.FIRST_NAME, "a".repeat(50))

        vm.save()

        assertThat(requireNotNull(api.lastUpdate).firstName).isEqualTo("a".repeat(50))
    }

    /** OAuth and older accounts have no first name; the form still sends "" and that must save. */
    @Test
    fun `an empty first name with none stored still saves`() = runTest {
        val api = FakeApi().apply { ownProfile = ApiEnvelope(FakeApi.LOADED.copy(firstName = "")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        assertThat(requireNotNull(api.lastUpdate).firstName).isEmpty()
        assertThat(editingState(vm).fieldErrors).isEmpty()
    }

    /**
     * The legacy-name case. profile-service skips a first name whose trimmed
     * value equals the stored one, so a name from before the rules is sent
     * back as it is and does not stand in the way of other edits.
     */
    @Test
    fun `an unchanged out-of-policy stored first name does not block saving other fields`() = runTest {
        val api = FakeApi().apply { ownProfile = ApiEnvelope(FakeApi.LOADED.copy(firstName = "Agent 007")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        val state = editingState(vm)
        assertThat(state.errorFor(EditProfileField.FIRST_NAME)).isNull()
        assertThat(state.fieldErrors).isEmpty()
        assertThat(requireNotNull(api.lastUpdate).firstName).isEqualTo("Agent 007")
        assertThat(state.saved).isTrue()
    }

    @Test
    fun `editing an out-of-policy stored first name to another invalid value is refused`() = runTest {
        val api = FakeApi().apply { ownProfile = ApiEnvelope(FakeApi.LOADED.copy(firstName = "Agent 007")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.FIRST_NAME, "Agent 008")

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.FIRST_NAME))
            .isEqualTo("Use letters, spaces, hyphens, apostrophes and periods only")
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    @Test
    fun `editing an out-of-policy stored first name to a valid value saves`() = runTest {
        val api = FakeApi().apply { ownProfile = ApiEnvelope(FakeApi.LOADED.copy(firstName = "Agent 007")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.FIRST_NAME, "Agent")

        vm.save()

        val state = editingState(vm)
        assertThat(state.fieldErrors).isEmpty()
        assertThat(requireNotNull(api.lastUpdate).firstName).isEqualTo("Agent")
        assertThat(state.saved).isTrue()
    }

    @Test
    fun `the date of birth picker stops at the 18th birthday today in India`() = runTest {
        assertThat(editingState(viewModel(FakeApi())).latestBirthDate).isEqualTo(LocalDate.of(2008, 9, 15))
    }

    @Test
    fun `an under-18 date of birth blocks the request and marks the field`() = runTest {
        val api = FakeApi()
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.DATE_OF_BIRTH, "2008-09-16")

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.DATE_OF_BIRTH))
            .isEqualTo("You must be at least 18 years old")
        assertThat(api.calls).doesNotContain("updateProfile")
    }

    /** The server skips an unchanged DOB, so the client must not block on one either. */
    @Test
    fun `an unchanged out-of-policy date of birth does not block other edits`() = runTest {
        val api = FakeApi().apply { ownProfile = ApiEnvelope(FakeApi.LOADED.copy(dob = "2015-01-01T00:00:00Z")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        assertThat(requireNotNull(api.lastUpdate).dob).isEqualTo("2015-01-01T00:00:00Z")
    }

    @Test
    fun `each server field refusal is marked on its field`() = runTest {
        val expected = mapOf(
            "FIRST_NAME_INVALID" to EditProfileField.FIRST_NAME,
            "DOB_INVALID" to EditProfileField.DATE_OF_BIRTH,
            "DOB_REQUIRED" to EditProfileField.DATE_OF_BIRTH,
            "DOB_IN_FUTURE" to EditProfileField.DATE_OF_BIRTH,
            "DOB_TOO_EARLY" to EditProfileField.DATE_OF_BIRTH,
            "DOB_UNDER_MINIMUM_AGE" to EditProfileField.DATE_OF_BIRTH,
            "DOB_MISMATCH_REGISTRATION" to EditProfileField.DATE_OF_BIRTH,
        )

        expected.forEach { (code, field) ->
            val api = FakeApi().apply { updateResult = ApiEnvelope(error = ApiErrorBody(code = code)) }
            val vm = viewModel(api)
            vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

            vm.save()

            val state = editingState(vm)
            assertWithMessage(code).that(state.errorFor(field)).isNotNull()
            assertWithMessage(code).that(state.saved).isFalse()
            assertWithMessage(code).that(state.form.location).isEqualTo("Hyderabad")
        }
    }

    @Test
    fun `a registration mismatch says how far the date can move`() = runTest {
        val api = FakeApi().apply {
            updateResult = ApiEnvelope(error = ApiErrorBody(code = "DOB_MISMATCH_REGISTRATION"))
        }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.DATE_OF_BIRTH, "1995-01-01")

        vm.save()

        assertThat(editingState(vm).errorFor(EditProfileField.DATE_OF_BIRTH)).isEqualTo(
            "Date of birth can only be corrected by up to a year from the one you signed up with",
        )
    }

    @Test
    fun `an unknown server code falls back to the generic message`() = runTest {
        val api = FakeApi().apply { updateResult = ApiEnvelope(error = ApiErrorBody(code = "SOMETHING_NEW")) }
        val vm = viewModel(api)
        vm.onFieldChange(EditProfileField.LOCATION, "Hyderabad")

        vm.save()

        val state = editingState(vm)
        assertThat(state.fieldErrors).isEmpty()
        assertThat(state.message?.text).isEqualTo("We couldn't save your changes. Nothing was lost — try again.")
    }
}
