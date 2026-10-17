package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.CompletableDeferred
import org.junit.Rule
import org.junit.Test

/**
 * Settings' Log out row (2026-10-17): it asks "Log out of Momentum on this
 * device?" first, and nothing signs out until that is confirmed.
 */
class SettingsLogoutViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private var signOuts = 0
    private val viewModel = SettingsLogoutViewModel(signOut = { signOuts++ })

    @Test
    fun `tapping Log out only asks`() {
        viewModel.requestLogout()

        assertThat(viewModel.state.value).isEqualTo(SettingsLogoutUiState(confirming = true))
        assertThat(signOuts).isEqualTo(0)
    }

    @Test
    fun `confirming signs out once and hands over to the signed-out destination`() {
        viewModel.requestLogout()
        viewModel.confirmLogout()

        assertThat(signOuts).isEqualTo(1)
        assertThat(viewModel.state.value).isEqualTo(SettingsLogoutUiState(signedOut = true))

        // A second confirm after the fact does nothing.
        viewModel.confirmLogout()
        assertThat(signOuts).isEqualTo(1)
    }

    @Test
    fun `a confirm with no dialog up signs nobody out`() {
        viewModel.confirmLogout()

        assertThat(signOuts).isEqualTo(0)
        assertThat(viewModel.state.value).isEqualTo(SettingsLogoutUiState())
    }

    @Test
    fun `Cancel closes the dialog and leaves the session alone`() {
        viewModel.requestLogout()
        viewModel.cancelLogout()
        viewModel.confirmLogout()

        assertThat(signOuts).isEqualTo(0)
        assertThat(viewModel.state.value).isEqualTo(SettingsLogoutUiState())
    }

    @Test
    fun `while signing out the row is busy and a second confirm is ignored`() {
        val gate = CompletableDeferred<Unit>()
        var calls = 0
        val slow = SettingsLogoutViewModel(signOut = {
            calls++
            gate.await()
        })

        slow.requestLogout()
        slow.confirmLogout()
        assertThat(slow.state.value.busy).isTrue()
        assertThat(slow.state.value.signedOut).isFalse()

        slow.confirmLogout()
        slow.cancelLogout()
        assertThat(calls).isEqualTo(1)
        assertThat(slow.state.value.busy).isTrue()

        gate.complete(Unit)
        assertThat(slow.state.value).isEqualTo(SettingsLogoutUiState(signedOut = true))
    }
}
