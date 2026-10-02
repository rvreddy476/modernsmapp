package com.us.android.core.datastore

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * Offline copies' two preferences, on the real preferences file.
 *
 * What this protects: the device id being made ONCE (a second id would
 * orphan every copy the first one was granted for: the server checks
 * validity per device), and Wi-Fi-only being ON for a viewer who never
 * touched the switch.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class OfflinePrefsTest {

    private fun settings() = SettingsDataStore(RuntimeEnvironment.getApplication())

    private fun prefs() = DataStoreOfflinePrefs(settings())

    @Before
    fun setUp() = runTest { settings().clear() }

    @Test
    fun `the device id is made once and a later reader finds the same one`() = runTest {
        val first = prefs().deviceId()
        val second = prefs().deviceId()

        assertThat(first).isNotEmpty()
        assertThat(first.length).isAtMost(MAX_DEVICE_ID_LENGTH)
        assertThat(second).isEqualTo(first)
    }

    @Test
    fun `saving is wifi only until the viewer says otherwise`() = runTest {
        assertThat(prefs().wifiOnly.first()).isTrue()

        prefs().setWifiOnly(false)

        assertThat(prefs().wifiOnly.first()).isFalse()
    }

    @Test
    fun `a stored id is kept and never regenerated`() {
        assertThat(offlineDeviceIdOr("abc") { error("must not generate") }).isEqualTo("abc")
    }

    @Test
    fun `a missing, blank or oversized id is replaced by a new one within the server's bound`() {
        assertThat(offlineDeviceIdOr(null) { "new" }).isEqualTo("new")
        assertThat(offlineDeviceIdOr("  ") { "new" }).isEqualTo("new")
        assertThat(offlineDeviceIdOr("x".repeat(MAX_DEVICE_ID_LENGTH + 1)) { "new" }).isEqualTo("new")
        assertThat(offlineDeviceIdOr(null) { "y".repeat(200) }).hasLength(MAX_DEVICE_ID_LENGTH)
    }
}
