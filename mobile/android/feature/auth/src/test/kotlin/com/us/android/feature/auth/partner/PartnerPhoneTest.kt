package com.us.android.feature.auth.partner

import org.junit.Assert.*
import org.junit.Test

class PartnerPhoneTest {
    @Test fun resendTimerCatchesUpAfterAppWasInBackground() {
        assertEquals(30, resendSeconds(30_000, 0))
        assertEquals(1, resendSeconds(30_000, 29_999))
        assertEquals(0, resendSeconds(30_000, 45_000))
    }
    @Test fun registrationRequiresContactNameGenderAndConsent() {
        val s = PartnerPhoneState(register = true, phone = "9876543210", first = "Test", last = "Partner", gender = "others", consent = true)
        assertNull(partnerPhoneError(s))
        assertNotNull(partnerPhoneError(s.copy(phone = "123")))
        assertNotNull(partnerPhoneError(s.copy(first = "")))
        assertNotNull(partnerPhoneError(s.copy(last = "")))
        assertNotNull(partnerPhoneError(s.copy(gender = "")))
        assertNotNull(partnerPhoneError(s.copy(consent = false)))
    }
    @Test fun signInRequiresOnlyMobile() {
        assertNull(partnerPhoneError(PartnerPhoneState(phone = "9876543210")))
    }
}
