package com.us.android.feature.post.createhub.banuba

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class BanubaGateTest {

    /** A scripted SDK: what initialize returns, what the licence answers, and how often each was called. */
    private class FakeSdk(
        private val licenceValid: Boolean? = true,
        private val rejectToken: Boolean = false,
        private val startFailure: Throwable? = null,
    ) : BanubaSdk {
        var starts = 0
        var initialisations = 0
        var checks = 0

        override fun startGraph() {
            starts++
            startFailure?.let { throw it }
        }

        override fun initialize(token: String): BanubaLicence? {
            initialisations++
            if (rejectToken) return null
            return BanubaLicence { onState ->
                checks++
                licenceValid?.let(onState)
            }
        }
    }

    @Test
    fun `no token is Unlicensed and ensure never touches the sdk`() {
        val sdk = FakeSdk()
        val gate = BanubaGate(BanubaConfig(licenseToken = "  "), sdk)

        assertEquals(BanubaState.Unlicensed, gate.state.value)
        gate.ensure()
        assertEquals(BanubaState.Unlicensed, gate.state.value)
        assertEquals(0, sdk.starts)
        assertEquals(0, sdk.initialisations)
    }

    @Test
    fun `a token starts Initialising until ensure is called`() {
        val sdk = FakeSdk()
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), sdk)

        assertEquals(BanubaState.Initialising, gate.state.value)
        assertEquals(0, sdk.starts)
    }

    @Test
    fun `a rejected token is Failed`() {
        val sdk = FakeSdk(rejectToken = true)
        val gate = BanubaGate(BanubaConfig(licenseToken = "truncated"), sdk)

        gate.ensure()

        assertTrue(gate.state.value is BanubaState.Failed)
        assertEquals(1, sdk.starts)
        assertEquals(1, sdk.initialisations)
    }

    @Test
    fun `an expired or revoked licence is Invalid`() {
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), FakeSdk(licenceValid = false))

        gate.ensure()

        assertEquals(BanubaState.Invalid, gate.state.value)
    }

    @Test
    fun `a valid licence is Ready`() {
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), FakeSdk(licenceValid = true))

        gate.ensure()

        assertEquals(BanubaState.Ready, gate.state.value)
    }

    @Test
    fun `a pending licence answer stays Initialising`() {
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), FakeSdk(licenceValid = null))

        gate.ensure()

        assertEquals(BanubaState.Initialising, gate.state.value)
    }

    @Test
    fun `a second ensure does not start or initialise again`() {
        val sdk = FakeSdk(licenceValid = true)
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), sdk)

        gate.ensure()
        gate.ensure()
        gate.ensure()

        assertEquals(BanubaState.Ready, gate.state.value)
        assertEquals(1, sdk.starts)
        assertEquals(1, sdk.initialisations)
        assertEquals(1, sdk.checks)
    }

    @Test
    fun `an sdk that fails to start is Failed with its message and is not retried`() {
        val sdk = FakeSdk(startFailure = IllegalStateException("libbanuba.so not found"))
        val gate = BanubaGate(BanubaConfig(licenseToken = "token"), sdk)

        gate.ensure()
        gate.ensure()

        assertEquals(BanubaState.Failed("libbanuba.so not found"), gate.state.value)
        assertEquals(1, sdk.starts)
        assertEquals(0, sdk.initialisations)
    }

    @Test
    fun `the config never prints the token`() {
        val config = BanubaConfig(licenseToken = "secret-token-value")

        assertEquals("BanubaConfig(licensed=true)", config.toString())
    }
}
