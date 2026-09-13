package com.us.android.feature.kitchen.location

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class LocationPermissionFlowTest {

    @Test
    fun `without permission the rationale is shown and the system prompt is not requested`() {
        val flow = LocationPermissionFlow()

        val effect = flow.onUseCurrentLocation(permissionGranted = false)

        assertThat(effect).isEqualTo(LocationEffect.None)
        assertThat(flow.state).isEqualTo(LocationStepState.ExplainingPermission)
    }

    @Test
    fun `the permission is requested only after the partner continues from the rationale`() {
        val flow = LocationPermissionFlow()
        // Out of order: no rationale on screen yet, so nothing is requested.
        assertThat(flow.onRationaleAccepted()).isEqualTo(LocationEffect.None)
        assertThat(flow.state).isEqualTo(LocationStepState.Idle)

        flow.onUseCurrentLocation(permissionGranted = false)
        assertThat(flow.onRationaleAccepted()).isEqualTo(LocationEffect.RequestPermission)
        assertThat(flow.state).isEqualTo(LocationStepState.AwaitingPermission)
    }

    @Test
    fun `every path to a permission request passes through the rationale`() {
        val calls = listOf<(LocationPermissionFlow) -> LocationEffect>(
            { it.onUseCurrentLocation(false) },
            { it.onRationaleAccepted() },
            { it.onPermissionResult(granted = false, canAskAgain = true) },
            { it.onUseCurrentLocation(false) },
            { it.onRationaleDismissed(); LocationEffect.None },
            { it.onRationaleAccepted() },
            { it.onLocationResult(null); LocationEffect.None },
        )
        val flow = LocationPermissionFlow()
        var rationaleShown = false
        for (call in calls) {
            val effect = call(flow)
            if (flow.state == LocationStepState.ExplainingPermission) rationaleShown = true
            if (effect == LocationEffect.RequestPermission) {
                assertThat(rationaleShown).isTrue()
                rationaleShown = false
            }
        }
    }

    @Test
    fun `dismissing the rationale asks nothing and leaves manual entry`() {
        val flow = LocationPermissionFlow()
        flow.onUseCurrentLocation(permissionGranted = false)
        flow.onRationaleDismissed()
        assertThat(flow.state).isEqualTo(LocationStepState.Idle)
        assertThat(flow.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(LocationEffect.None)
    }

    @Test
    fun `granted fetches a fix, denied remembers whether it can ask again`() {
        val granted = LocationPermissionFlow()
        granted.onUseCurrentLocation(false)
        granted.onRationaleAccepted()
        assertThat(granted.onPermissionResult(granted = true, canAskAgain = true)).isEqualTo(LocationEffect.FetchLocation)
        granted.onLocationResult(Coordinates(12.9716, 77.5946))
        assertThat(granted.state).isEqualTo(LocationStepState.Located(Coordinates(12.9716, 77.5946)))

        val denied = LocationPermissionFlow()
        denied.onUseCurrentLocation(false)
        denied.onRationaleAccepted()
        assertThat(denied.onPermissionResult(granted = false, canAskAgain = false)).isEqualTo(LocationEffect.None)
        assertThat(denied.state).isEqualTo(LocationStepState.Denied(canAskAgain = false))
    }

    @Test
    fun `an already granted permission skips straight to a fix`() {
        val flow = LocationPermissionFlow()
        assertThat(flow.onUseCurrentLocation(permissionGranted = true)).isEqualTo(LocationEffect.FetchLocation)
        flow.onLocationResult(null)
        assertThat(flow.state).isEqualTo(LocationStepState.Unavailable)
    }
}
