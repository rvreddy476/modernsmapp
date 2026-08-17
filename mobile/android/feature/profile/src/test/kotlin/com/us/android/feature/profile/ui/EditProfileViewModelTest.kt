package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.profile.data.EditProfileField
import com.us.android.feature.profile.data.ProfileApi
import com.us.android.feature.profile.data.ProfileRepository
import com.us.android.feature.profile.data.dto.GraphStatusDto
import com.us.android.feature.profile.data.dto.GraphUserIdRequest
import com.us.android.feature.profile.data.dto.OwnProfileDto
import com.us.android.feature.profile.data.dto.ProfileStatsDto
import com.us.android.feature.profile.data.dto.PublicProfileDto
import com.us.android.feature.profile.data.dto.UpdateProfileRequest
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Rule
import org.junit.Test

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

        override suspend fun follow(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("followed"))

        override suspend fun unfollow(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("unfollowed"))

        override suspend fun block(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("blocked"))

        override suspend fun unblock(body: GraphUserIdRequest) = ApiEnvelope(GraphStatusDto("unblocked"))

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

    private fun viewModel(api: FakeApi) =
        EditProfileViewModel(ProfileRepository(api, ErrorMapper(json)))

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
}
