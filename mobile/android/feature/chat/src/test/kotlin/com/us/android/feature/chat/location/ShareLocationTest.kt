package com.us.android.feature.chat.location

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatLocation
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** Share location (founder, 23 Oct 2026): rationale before the prompt, one fix, confirm. */
class ShareLocationTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    @Test
    fun `the system prompt is only ever reached through the rationale`() {
        val flow = ShareLocationFlow()
        // Out of order: nothing happens.
        assertThat(flow.onRationaleAccepted()).isEqualTo(ShareLocationEffect.None)
        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(ShareLocationEffect.None)
        assertThat(flow.step).isEqualTo(ShareLocationStep.Idle)

        assertThat(flow.onShareLocation(permissionGranted = false)).isEqualTo(ShareLocationEffect.None)
        assertThat(flow.step).isEqualTo(ShareLocationStep.ExplainingPermission)
        assertThat(flow.onRationaleAccepted()).isEqualTo(ShareLocationEffect.RequestPermission)
        assertThat(flow.onPermissionResult(granted = false, canAskAgain = false)).isEqualTo(ShareLocationEffect.None)
        assertThat(flow.step).isEqualTo(ShareLocationStep.Denied(canAskAgain = false))
        // A late fix after a refusal is ignored.
        flow.onLocationResult(ChatLocation(1.0, 2.0))
        assertThat(flow.step).isEqualTo(ShareLocationStep.Denied(canAskAgain = false))
    }

    @Test
    fun `granted already goes straight to one fix, and an impossible fix is unavailable`() {
        val flow = ShareLocationFlow()
        assertThat(flow.onShareLocation(permissionGranted = true)).isEqualTo(ShareLocationEffect.FetchLocation)
        flow.onLocationResult(ChatLocation(95.0, 2.0))
        assertThat(flow.step).isEqualTo(ShareLocationStep.Unavailable)

        val again = ShareLocationFlow()
        again.onShareLocation(permissionGranted = true)
        again.onLocationResult(null)
        assertThat(again.step).isEqualTo(ShareLocationStep.Unavailable)
    }

    private class FakeSource(var granted: Boolean, var fix: DeviceFix?) : ChatLocationSource {
        var fetches = 0
        override fun hasPermission() = granted
        override suspend fun current(): DeviceFix? {
            fetches++
            return fix
        }
    }

    private class FakeAddresses(private val address: String?) : ChatAddressLookup {
        override suspend fun reverse(fix: DeviceFix) = address
    }

    @Test
    fun `a fix becomes a rounded place with the typed name`() = runTest {
        val source = FakeSource(granted = true, fix = DeviceFix(12.9715987, 77.5945627, 8.6))
        val vm = ShareLocationViewModel(source, FakeAddresses("Kasturba Rd, Bengaluru"))
        vm.start()
        val located = vm.state.value.step as ShareLocationStep.Located
        assertThat(located.location).isEqualTo(ChatLocation(12.9716, 77.59456, 9.0, null, "Kasturba Rd, Bengaluru"))
        assertThat(source.fetches).isEqualTo(1)

        vm.onLabel("  Home  ")
        assertThat(vm.confirmed()).isEqualTo(ChatLocation(12.9716, 77.59456, 9.0, "Home", "Kasturba Rd, Bengaluru"))

        // Closing drops it.
        vm.clear()
        assertThat(vm.confirmed()).isNull()
        assertThat(vm.state.value.step).isEqualTo(ShareLocationStep.Idle)
    }

    @Test
    fun `without permission the sheet explains first and asks only on continue`() = runTest {
        val source = FakeSource(granted = false, fix = DeviceFix(1.0, 2.0, null))
        val vm = ShareLocationViewModel(source, FakeAddresses(null))
        vm.start()
        assertThat(vm.state.value.step).isEqualTo(ShareLocationStep.ExplainingPermission)
        assertThat(source.fetches).isEqualTo(0)
        assertThat(vm.confirmed()).isNull()

        vm.onRationaleAccepted()
        assertThat(vm.effects.first()).isEqualTo(ShareLocationEffect.RequestPermission)
        source.granted = true
        vm.onPermissionResult(granted = true, canAskAgain = true)
        assertThat(vm.state.value.step).isInstanceOf(ShareLocationStep.Located::class.java)
        assertThat(vm.confirmed()?.address).isNull()
    }
}
