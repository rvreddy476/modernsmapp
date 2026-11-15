package com.us.android.feature.profile.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The DigiLocker return rules (2026-11-15): the mock shortcut and the verified App Link. */
class PersonReturnLinksTest {

    @Test
    fun `a mock authorize url that is already the return page is posted directly`() {
        val link = PersonReturnLinks.mockReturnOf(
            "http://localhost:3000/settings/verification/person/return?code=mock-abc&state=s%2B1",
        )

        assertThat(link).isEqualTo(PersonReturn(code = "mock-abc", state = "s+1"))
    }

    @Test
    fun `a mock return on any host and a trailing slash still counts`() {
        val link = PersonReturnLinks.mockReturnOf(
            "https://dev.example.test/settings/verification/person/return/?state=st&code=c1",
        )

        assertThat(link).isEqualTo(PersonReturn(code = "c1", state = "st"))
    }

    @Test
    fun `a real DigiLocker authorize url opens the browser`() {
        val urls = listOf(
            "https://digilocker.meripehchaan.gov.in/public/oauth2/1/authorize?response_type=code&state=x&code=y",
            "http://localhost:3000/settings/verification/person/return?code=only-code",
            "http://localhost:3000/settings/verification/person/return?state=only-state",
            "http://localhost:3000/settings/verification/person/return?code=&state=s",
            "http://localhost:3000/settings/verification/person/returned?code=c&state=s",
            "javascript:alert(1)//settings/verification/person/return?code=c&state=s",
            "",
            null,
        )

        urls.forEach { assertThat(PersonReturnLinks.mockReturnOf(it)).isNull() }
    }

    @Test
    fun `the verified App Link on either web host is claimed`() {
        listOf("cleestudio.com", "app.cleestudio.com", "APP.CLEESTUDIO.COM").forEach { host ->
            val link = PersonReturnLinks.appLinkReturnOf(
                "https://$host/settings/verification/person/return?code=c&state=s",
            )
            assertThat(link).isEqualTo(PersonReturn(code = "c", state = "s"))
        }
    }

    @Test
    fun `any other link is not the App Link`() {
        val links = listOf(
            "http://cleestudio.com/settings/verification/person/return?code=c&state=s",
            "https://evil.example/settings/verification/person/return?code=c&state=s",
            "https://cleestudio.com.evil.example/settings/verification/person/return?code=c&state=s",
            "https://user@cleestudio.com/settings/verification/person/return?code=c&state=s",
            "https://cleestudio.com:8443/settings/verification/person/return?code=c&state=s",
            "https://cleestudio.com/x/settings/verification/person/return?code=c&state=s",
            "https://cleestudio.com/settings/verification/person/return?code=c",
            "https://cleestudio.com/post/abc",
        )

        links.forEach { assertThat(PersonReturnLinks.appLinkReturnOf(it)).isNull() }
    }

    @Test
    fun `the holder claims only its link and hands it out once`() {
        val returns = PersonVerificationReturns()

        assertThat(returns.offerRedirect("https://cleestudio.com/post/abc")).isFalse()
        assertThat(returns.pending.value).isNull()
        assertThat(returns.offerRedirect("https://cleestudio.com/settings/verification/person/return?code=c&state=s"))
            .isTrue()
        assertThat(returns.take()).isEqualTo(PersonReturn("c", "s"))
        assertThat(returns.take()).isNull()
    }

    @Test
    fun `a return never prints its code or state`() {
        assertThat(PersonReturn(code = "secret-code", state = "secret-state").toString())
            .doesNotContain("secret")
    }
}
