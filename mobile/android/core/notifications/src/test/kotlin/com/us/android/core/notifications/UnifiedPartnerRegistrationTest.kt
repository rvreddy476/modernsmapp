package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.data.*
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import io.mockk.*
import kotlinx.coroutines.test.runTest
import org.junit.Test
import java.util.Optional

class UnifiedPartnerRegistrationTest {
    @Test fun upgradeRegistersUnchangedTokenForUnifiedTarget() = runTest {
        val api = mockk<DeviceApi>()
        val store = mockk<PushTokenStore>(relaxed = true)
        every { store.token() } returns "same-fcm-token"
        every { store.registeredToken() } returns "same-fcm-token"
        coEvery { api.registerDevice(any()) } returns ApiEnvelope(data = DeviceDto(id = "actual-device"))
        val registrar = PushTokenRegistrar(api, store, mockk<ErrorMapper>(), Optional.of(PushApp.PARTNER))
        registrar.registerIfNeeded()
        coVerify(exactly = 1) { api.registerDevice(RegisterDeviceRequest("android", "same-fcm-token", "partner")) }
        verify { store.markRegistered("partner:same-fcm-token", "actual-device") }
    }
    @Test fun alreadyUpgradedInstallDoesNotReregisterOnEveryLaunch() = runTest {
        val api = mockk<DeviceApi>()
        val store = mockk<PushTokenStore>(relaxed = true)
        every { store.token() } returns "fcm"
        every { store.registeredToken() } returns "partner:fcm"
        val registrar = PushTokenRegistrar(api, store, mockk<ErrorMapper>(), Optional.of(PushApp.PARTNER))
        registrar.registerIfNeeded()
        coVerify(exactly = 0) { api.registerDevice(any()) }
    }
}
