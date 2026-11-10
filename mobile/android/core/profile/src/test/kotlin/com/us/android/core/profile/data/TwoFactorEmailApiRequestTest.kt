package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Two-step verification by emailed code (founder, 10 Nov 2026) on the wire,
 * against auth-service `internal/http/twofa_email.go` and `me.go`: the exact
 * paths and JSON bodies (every field binds required), and the bodies the
 * handlers write, including ones with fields missing.
 */
class TwoFactorEmailApiRequestTest {

    /** Mirrors NetworkModule.provideJson(): no encodeDefaults. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }
    private lateinit var server: MockWebServer
    private lateinit var repository: SecuritySettingsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        repository = SecuritySettingsRepository(
            retrofit.create(AccountSecurityApi::class.java),
            retrofit.create(DeviceSecurityApi::class.java),
            ErrorMapper(json),
        )
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    private fun RecordedRequest.bodyText(): String = body!!.utf8()

    @Test
    fun `start posts the password and purpose and reads the challenge`() = runBlocking {
        enqueue(
            200,
            """{"data":{"challenge_token":"ch-1","expires_at":"2026-11-10T10:05:00Z",""" +
                """"email_masked":"r***@gmail.com"}}""",
        )

        val result = repository.startEmailTwoFactor("hunter2", TwoFactorPurpose.Enable)

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/auth/2fa/email/start")
        assertThat(request.bodyText()).isEqualTo("""{"password":"hunter2","purpose":"enable"}""")
        assertThat((result as AppResult.Success).data)
            .isEqualTo(EmailChallenge("ch-1", "2026-11-10T10:05:00Z", "r***@gmail.com"))
    }

    @Test
    fun `start for disable says so`() = runBlocking {
        enqueue(200, """{"data":{"challenge_token":"ch-2"}}""")

        val result = repository.startEmailTwoFactor("hunter2", TwoFactorPurpose.Disable)

        assertThat(server.takeRequest().bodyText()).isEqualTo("""{"password":"hunter2","purpose":"disable"}""")
        // Missing fields decode to blanks rather than failing.
        assertThat((result as AppResult.Success).data).isEqualTo(EmailChallenge("ch-2", "", ""))
    }

    @Test
    fun `enable and the email disable send the challenge and the code to their routes`() = runBlocking {
        enqueue(200, """{"data":{"enabled":true,"method":"email"}}""")
        enqueue(200, """{"data":{"message":"two-step verification disabled","enabled":false,"method":""}}""")

        val enabled = repository.enableEmailTwoFactor("ch-1", " 123456 ")
        val disabled = repository.disableEmailTwoFactor("ch-2", "654321")

        val enable = server.takeRequest()
        assertThat(enable.target).isEqualTo("/v1/auth/2fa/email/enable")
        assertThat(enable.bodyText()).isEqualTo("""{"challenge_token":"ch-1","code":"123456"}""")
        val disable = server.takeRequest()
        assertThat(disable.target).isEqualTo("/v1/auth/2fa/disable")
        assertThat(disable.bodyText()).isEqualTo("""{"challenge_token":"ch-2","code":"654321"}""")
        assertThat(enabled).isInstanceOf(AppResult.Success::class.java)
        assertThat(disabled).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `the authenticator disable keeps its password and code body`() = runBlocking {
        enqueue(200, """{"data":{"message":"ok","enabled":false,"method":""}}""")

        repository.disableTwoFactor("hunter2", "123456")

        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/v1/auth/2fa/disable")
        assertThat(request.bodyText()).isEqualTo("""{"password":"hunter2","code":"123456"}""")
    }

    @Test
    fun `the authenticator setup routes are never called`() {
        val methods = AccountSecurityApi::class.java.declaredMethods.map { it.name }
        assertThat(methods).containsNoneOf("setupTwoFactor", "verifyTwoFactor")
    }

    @Test
    fun `me carries two_factor_method beside two_factor_enabled`() = runBlocking {
        enqueue(200, """{"data":{"user_id":"u1","two_factor_enabled":true,"two_factor_method":"email"}}""")
        enqueue(200, """{"data":{"user_id":"u1","two_factor_enabled":true,"two_factor_method":"totp"}}""")
        // An older server: enabled, no method — an authenticator app.
        enqueue(200, """{"data":{"user_id":"u1","two_factor_enabled":true}}""")
        enqueue(200, """{"data":{"user_id":"u1","two_factor_enabled":false,"two_factor_method":""}}""")
        enqueue(200, """{"data":{"user_id":"u1","two_factor_enabled":true,"two_factor_method":"sms"}}""")

        val methods = List(5) { (repository.account() as AppResult.Success).data.twoFactorMethod }

        assertThat(methods).containsExactly(
            TwoFactorMethod.Email,
            TwoFactorMethod.Authenticator,
            TwoFactorMethod.Authenticator,
            TwoFactorMethod.None,
            TwoFactorMethod.Other,
        ).inOrder()
    }

    @Test
    fun `refusals keep the server's code for the screen to word`() = runBlocking {
        enqueue(401, """{"error":{"code":"INVALID_CREDENTIALS","message":"x"}}""")
        enqueue(409, """{"error":{"code":"EMAIL_NOT_VERIFIED","message":"x"}}""")
        enqueue(400, """{"error":{"code":"INVALID_CODE","message":"x"}}""")
        enqueue(503, """{"error":{"code":"EMAIL_UNAVAILABLE","message":"x"}}""")
        server.enqueue(
            MockResponse.Builder().code(429).addHeader("Retry-After", "60")
                .body("""{"error":{"code":"RATE_LIMITED","message":"x"}}""").build(),
        )

        fun AppResult<*>.error() = (this as AppResult.Failure).error
        val wrongPassword = repository.startEmailTwoFactor("bad", TwoFactorPurpose.Enable).error()
        val unverified = repository.startEmailTwoFactor("pw", TwoFactorPurpose.Enable).error()
        val wrongCode = repository.enableEmailTwoFactor("ch", "000000").error()
        val unavailable = repository.startEmailTwoFactor("pw", TwoFactorPurpose.Enable).error()
        val limited = repository.startEmailTwoFactor("pw", TwoFactorPurpose.Enable).error()

        assertThat((wrongPassword as AppError.AuthFailed).code).isEqualTo("INVALID_CREDENTIALS")
        assertThat((unverified as AppError.Unknown).code).isEqualTo("EMAIL_NOT_VERIFIED")
        assertThat((wrongCode as AppError.Unknown).code).isEqualTo("INVALID_CODE")
        assertThat((unavailable as AppError.Server).code).isEqualTo("EMAIL_UNAVAILABLE")
        assertThat((limited as AppError.RateLimited).retryAfterSeconds).isEqualTo(60)
    }
}
