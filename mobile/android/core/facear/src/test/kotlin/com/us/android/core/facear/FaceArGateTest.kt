package com.us.android.core.facear

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The gate's five states, and the one that only exists because two Banuba
 * product lines share a process.
 */
class FaceArGateTest {

    /**
     * A scripted SDK: whether something else already initialised the native
     * SDK, what the licence answers, and how often each entry point was used.
     */
    private class FakeSdk(
        private val licenceValid: Boolean? = true,
        private val rejectToken: Boolean = false,
        private val initFailure: Throwable? = null,
        private val preInitialised: Boolean = false,
    ) : FaceArSdk {
        var initialisations = 0
        var licenceReads = 0
        var checks = 0

        override fun alreadyInitialised(): Boolean = preInitialised

        override fun initialize(token: String): FaceArLicence? {
            initialisations++
            initFailure?.let { throw it }
            return licence(token)
        }

        override fun licence(token: String): FaceArLicence? {
            licenceReads++
            if (rejectToken) return null
            return FaceArLicence { onState ->
                checks++
                licenceValid?.let(onState)
            }
        }
    }

    @Test
    fun `no token is Unlicensed and ensure never touches the sdk`() {
        val sdk = FakeSdk()
        val gate = FaceArGate(FaceArConfig(licenseToken = "  "), sdk)

        assertThat(gate.state.value).isEqualTo(FaceArState.Unlicensed)
        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Unlicensed)
        assertThat(sdk.initialisations).isEqualTo(0)
        assertThat(sdk.licenceReads).isEqualTo(0)
    }

    @Test
    fun `a token starts Initialising until ensure is called`() {
        val sdk = FakeSdk()
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), sdk)

        assertThat(gate.state.value).isEqualTo(FaceArState.Initialising)
        assertThat(sdk.initialisations).isEqualTo(0)
    }

    @Test
    fun `a valid licence is Ready`() {
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), FakeSdk(licenceValid = true))

        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Ready)
        assertThat(gate.available.value).isTrue()
    }

    @Test
    fun `an expired or revoked licence is Invalid`() {
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), FakeSdk(licenceValid = false))

        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Invalid)
        assertThat(gate.available.value).isFalse()
    }

    @Test
    fun `a rejected token is Failed`() {
        val sdk = FakeSdk(rejectToken = true)
        val gate = FaceArGate(FaceArConfig(licenseToken = "truncated"), sdk)

        gate.ensure()

        assertThat(gate.state.value).isInstanceOf(FaceArState.Failed::class.java)
        assertThat(sdk.initialisations).isEqualTo(1)
    }

    @Test
    fun `an sdk that fails to start is Failed with its message and is not retried`() {
        val sdk = FakeSdk(initFailure = IllegalStateException("libbanuba.so not found"))
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), sdk)

        gate.ensure()
        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Failed("libbanuba.so not found"))
        assertThat(sdk.initialisations).isEqualTo(1)
    }

    @Test
    fun `a pending licence answer stays Initialising`() {
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), FakeSdk(licenceValid = null))

        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Initialising)
        assertThat(gate.available.value).isFalse()
    }

    /**
     * THE ONE-PROCESS CASE.
     *
     * The reel studio may have brought a Banuba licence up already. The gate
     * must then read the licence that exists rather than initialising a second
     * time — and still reach Ready.
     */
    @Test
    fun `when the other Banuba product already initialised, the token is not handed over again`() {
        val sdk = FakeSdk(licenceValid = true, preInitialised = true)
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), sdk)

        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Ready)
        assertThat(sdk.initialisations).isEqualTo(0)
        assertThat(sdk.licenceReads).isEqualTo(1)
    }

    @Test
    fun `an already-initialised process with no readable licence is Failed, not Ready`() {
        val sdk = FakeSdk(rejectToken = true, preInitialised = true)
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), sdk)

        gate.ensure()

        assertThat(gate.state.value).isInstanceOf(FaceArState.Failed::class.java)
        assertThat(sdk.initialisations).isEqualTo(0)
    }

    @Test
    fun `a second ensure does not initialise or re-check`() {
        val sdk = FakeSdk(licenceValid = true)
        val gate = FaceArGate(FaceArConfig(licenseToken = "token"), sdk)

        gate.ensure()
        gate.ensure()
        gate.ensure()

        assertThat(gate.state.value).isEqualTo(FaceArState.Ready)
        assertThat(sdk.initialisations).isEqualTo(1)
        assertThat(sdk.checks).isEqualTo(1)
    }

    // ─── what the SDK itself said, kept apart from what we concluded ─

    @Test
    fun `the licence answer is recorded exactly as the sdk gave it`() {
        val valid = FaceArGate(FaceArConfig(licenseToken = "token"), FakeSdk(licenceValid = true))
        val expired = FaceArGate(FaceArConfig(licenseToken = "token"), FakeSdk(licenceValid = false))

        valid.ensure()
        expired.ensure()

        assertThat(valid.licenceValid.value).isTrue()
        assertThat(expired.licenceValid.value).isFalse()
    }

    @Test
    fun `not asked and answered no are different answers`() {
        // The diagnostics panel has to tell these apart: "the licence says no"
        // sends you to the token, "nobody asked" sends you to the start-up
        // path. A false standing in for null would hide the second entirely.
        val unlicensed = FaceArGate(FaceArConfig(licenseToken = "  "), FakeSdk())
        val failed = FaceArGate(
            FaceArConfig(licenseToken = "token"),
            FakeSdk(initFailure = IllegalStateException("libbanuba.so not found")),
        )
        val rejected = FaceArGate(FaceArConfig(licenseToken = "x"), FakeSdk(rejectToken = true))
        val pending = FaceArGate(FaceArConfig(licenseToken = "x"), FakeSdk(licenceValid = null))

        listOf(unlicensed, failed, rejected, pending).forEach { gate ->
            gate.ensure()
            assertThat(gate.licenceValid.value).isNull()
        }
    }

    @Test
    fun `the config never prints the token`() {
        assertThat(FaceArConfig(licenseToken = "secret-token-value").toString())
            .isEqualTo("FaceArConfig(licensed=true)")
    }
}
