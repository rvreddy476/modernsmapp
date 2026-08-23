package com.us.android.core.auth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * What the auth client puts ON the wire.
 *
 * ## WHY THIS FILE EXISTS
 *
 * `AuthRepositoryTest` drives real HTTP through MockWebServer, but it asserts
 * on RESPONSES. Nothing checked the bytes of a REQUEST, and that is where the
 * defect lived: `ResendVerificationRequestDto.type` has a default,
 * kotlinx.serialization omits a property equal to its default, and the shared
 * `Json` leaves `encodeDefaults` off. `AuthRepository` builds the DTO with only
 * the token, so the body was `{"verification_token":"…"}` — no `type` — while
 * the server binds that field `required`.
 *
 * Every resend-verification request returned 400. That is the RECOVERY path for
 * a user whose first verification email never arrived, so it failed precisely
 * the people who were already stuck.
 *
 * These tests use the SAME `Json` configuration the app builds in
 * `NetworkModule.provideJson()`. A test that turned `encodeDefaults` on would
 * pass while the app kept failing, which is the trap worth naming.
 */
class AuthRequestEncodingTest {

    /** Mirrors NetworkModule.provideJson(). Deliberately NOT `encodeDefaults`. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    /**
     * The exact body, field for field.
     *
     * An exact-equality assertion rather than `contains`: the server binds
     * `type` required, so an omission must fail here, and pinning the whole
     * object also catches a field being renamed or silently added.
     */
    @Test
    fun `resend verification sends type email even though it is the default`() {
        val body = json.encodeToString(
            ResendVerificationRequestDto(verificationToken = "vt-123"),
        )

        assertThat(body).isEqualTo("""{"type":"email","verification_token":"vt-123"}""")
    }

    /** Passing the value explicitly must produce the identical body. */
    @Test
    fun `an explicitly typed resend encodes identically`() {
        val explicit = json.encodeToString(
            ResendVerificationRequestDto(type = "email", verificationToken = "vt-123"),
        )
        val defaulted = json.encodeToString(
            ResendVerificationRequestDto(verificationToken = "vt-123"),
        )

        assertThat(explicit).isEqualTo(defaulted)
    }

    /**
     * The sibling endpoint has no defaulted field, and is pinned so a later
     * "tidy-up" that adds one is caught here rather than in production.
     */
    @Test
    fun `verify email sends both required fields`() {
        val body = json.encodeToString(VerifyEmailRequestDto("vt-123", "079563"))

        assertThat(body).isEqualTo("""{"verification_token":"vt-123","code":"079563"}""")
    }

    @Test
    fun `refresh sends the token under its wire name`() {
        assertThat(json.encodeToString(RefreshRequestDto("rt-1")))
            .isEqualTo("""{"refresh_token":"rt-1"}""")
    }
}
