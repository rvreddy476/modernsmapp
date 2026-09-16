package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.network.BlockedPersonDto
import com.us.android.feature.dating.onboarding.DatingChoices
import com.us.android.feature.dating.onboarding.OnboardingViewModel
import com.us.android.feature.dating.privacy.BlocksViewModel
import com.us.android.feature.dating.privacy.UNBLOCK_RESTORES_NOTHING
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** The block list with unblock, and who you want to see — including "Everyone". */
class BlocksAndPreferencesTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()

    // ── Blocks ──────────────────────────────────────────────────────────────

    @Test
    fun `the block list shows who was blocked, and unblocking removes the row`() = runTest {
        api.blocked = listOf(
            BlockedPersonDto(userId = "user-bad", firstName = "Asha", age = 30, blockedAt = "2026-09-14T10:00:00Z"),
            BlockedPersonDto(userId = "user-other", firstName = "Rhea", age = 28, blockedAt = "2026-09-15T10:00:00Z"),
        )
        val vm = BlocksViewModel(repository)

        assertThat(vm.state.value.blocked.map { it.userId }).containsExactly("user-bad", "user-other").inOrder()
        assertThat(vm.state.value.blocked.first().name).isEqualTo("Asha")

        vm.unblock("user-bad")

        assertThat(api.unblocks).containsExactly("user-bad")
        assertThat(vm.state.value.blocked.map { it.userId }).containsExactly("user-other")
    }

    @Test
    fun `unblocking says plainly that nothing is restored`() = runTest {
        api.blocked = listOf(BlockedPersonDto(userId = "user-bad", firstName = "Asha"))
        val vm = BlocksViewModel(repository)

        vm.unblock("user-bad")

        // The confirmation the person reads must not sound like an undo.
        assertThat(vm.state.value.message?.text).contains("Nothing that was ended has come back")
        assertThat(UNBLOCK_RESTORES_NOTHING).contains("doesn't bring anything back")
    }

    @Test
    fun `unblocking someone already unblocked is not an error`() = runTest {
        // The route is idempotent: removed=false, and the row still goes.
        api.blocked = emptyList()
        val vm = BlocksViewModel(repository)

        vm.unblock("user-gone")

        assertThat(api.unblocks).containsExactly("user-gone")
        assertThat(vm.state.value.blocked).isEmpty()
    }

    @Test
    fun `a blocked person with no name still has a row to unblock`() = runTest {
        api.blocked = listOf(BlockedPersonDto(userId = "user-bad", firstName = ""))
        val vm = BlocksViewModel(repository)

        assertThat(vm.state.value.blocked.single().name).isEqualTo("Someone you blocked")
    }

    // ── Preferences ─────────────────────────────────────────────────────────

    @Test
    fun `Everyone is offered and maps to the wire value everyone`() = runTest {
        val everyone = DatingChoices.interestedIn.last()
        assertThat(everyone.label).isEqualTo("Everyone")
        assertThat(everyone.value).isEqualTo("everyone")

        val vm = OnboardingViewModel(repository, session, FakeLocation())
        vm.savePreferences(interestedIn = everyone.value, minAge = 25, maxAge = 35, distanceKm = 25)

        assertThat(api.preferenceWrites.single().interestedInGender).isEqualTo("everyone")
        assertThat(vm.state.value.message).isNull()
    }

    @Test
    fun `the existing gender values keep working`() = runTest {
        val vm = OnboardingViewModel(repository, session, FakeLocation())

        listOf("woman", "man", "nonbinary").forEach { value ->
            vm.savePreferences(interestedIn = value, minAge = 25, maxAge = 35, distanceKm = 25)
        }

        assertThat(api.preferenceWrites.map { it.interestedInGender })
            .containsExactly("woman", "man", "nonbinary").inOrder()
        // "Show me" offers all four; "I am a" still offers only the three genders.
        assertThat(DatingChoices.interestedIn.map { it.value })
            .containsExactly("woman", "man", "nonbinary", "everyone").inOrder()
        assertThat(DatingChoices.genders.map { it.value }).doesNotContain("everyone")
    }

    @Test
    fun `a refused preference value is explained, not swallowed`() = runTest {
        api.preferencesWriteResponse = { refused(400, "INVALID_REQUEST") }
        val vm = OnboardingViewModel(repository, session, FakeLocation())

        vm.savePreferences(interestedIn = "everyone", minAge = 25, maxAge = 35, distanceKm = 25)

        assertThat(vm.state.value.saving).isFalse()
        assertThat(vm.state.value.message?.text).contains("Pick who you want to see")
    }
}
