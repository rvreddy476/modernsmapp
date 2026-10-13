package com.us.android.feature.doorsteppro

import com.us.android.feature.doorsteppro.apply.ApplyUiState
import com.us.android.feature.doorsteppro.onboarding.BankFormData
import com.us.android.feature.doorsteppro.ui.UploadUi
import org.junit.Assert.*
import org.junit.Test

class PartnerBankTest {
    private fun valid() = BankFormData("Test Partner", "000123456789", "000123456789", "SBIN0001234", "State Bank", proof = UploadUi.Uploaded("proof-id"))
    @Test fun `bank proof and matching account details are mandatory`() {
        assertTrue(valid().errors().isEmpty())
        listOf(valid().copy(holder=""),valid().copy(account="123"),valid().copy(confirm="123"),valid().copy(ifsc="WRONG"),valid().copy(bankName=""),valid().copy(proof=UploadUi.Idle),valid().copy(proofKind="other"),valid().copy(branchName="A")).forEach { assertTrue(it.errors().isNotEmpty()) }
    }
    @Test fun `sensitive form state is redacted from string representations`() {
        assertFalse(valid().toString().contains("000123456789"))
        assertFalse(valid().request().toString().contains("000123456789"))
        assertFalse(ApplyUiState(pan="ABCPE1234F",bank=valid()).toString().contains("ABCPE1234F"))
    }
    @Test fun `application requires PAN identity proof selfie gender and review consent`() {
        val state = ApplyUiState()
        assertTrue(state.errorsFor(0).containsKey("gender"))
        assertTrue(state.errorsFor(2).containsKey("pan"))
        assertTrue(state.errorsFor(2).containsKey("identity"))
        assertTrue(state.errorsFor(2).containsKey("selfie"))
        assertTrue(state.errorsFor(3).containsKey("proof"))
        assertTrue(state.errorsFor(4).containsKey("consent"))
        assertTrue(state.copy(pan="ABCPE1234F",identity=UploadUi.Uploaded("id"),selfie=UploadUi.Uploaded("selfie-id")).errorsFor(2).isEmpty())
    }
}
