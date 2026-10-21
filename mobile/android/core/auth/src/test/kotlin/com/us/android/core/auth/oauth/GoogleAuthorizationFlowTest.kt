package com.us.android.core.auth.oauth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.net.URLDecoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

/**
 * The browser-based Google flow, end to end against a fake token endpoint:
 * the authorization URL it builds, which redirects it claims, the state
 * check, and the code-for-token exchange with its PKCE proof.
 */
class GoogleAuthorizationFlowTest {

    private lateinit var server: MockWebServer
    private val json = Json { ignoreUnknownKeys = true }
    private val config = GoogleOAuthConfig("1234-abc.apps.googleusercontent.com")

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() = server.close()

    private fun flow(config: GoogleOAuthConfig? = this.config) = GoogleAuthorizationFlow(
        config = config,
        client = OkHttpClient(),
        json = json,
        endpoints = GoogleAuthorizationFlow.GoogleEndpoints(
            authorization = "https://accounts.example/auth",
            token = server.url("/token").toString(),
        ),
        random = SecureRandom(),
    )

    private fun query(url: String): Map<String, String> = GoogleAuthorizationFlow.parseQuery(url)

    @Test
    fun `the redirect scheme is the client id reversed`() {
        assertThat(config.redirectScheme).isEqualTo("com.googleusercontent.apps.1234-abc")
        assertThat(config.redirectUri).isEqualTo("com.googleusercontent.apps.1234-abc:/oauth2redirect")
        assertThat(GoogleOAuthConfig("").isConfigured).isFalse()
    }

    @Test
    fun `without a client id the flow is unavailable and claims no redirect`() {
        val absent = flow(config = null)
        val blank = flow(config = GoogleOAuthConfig(" "))

        assertThat(absent.isAvailable).isFalse()
        assertThat(blank.isAvailable).isFalse()
        assertThat(absent.offerRedirect("com.googleusercontent.apps.1234-abc:/oauth2redirect?code=x")).isFalse()
    }

    @Test
    fun `begin builds a PKCE S256 authorization URL for the installed-app client`() {
        val url = flow().begin()

        assertThat(url).startsWith("https://accounts.example/auth?")
        val params = query(url)
        assertThat(params["client_id"]).isEqualTo("1234-abc.apps.googleusercontent.com")
        assertThat(params["redirect_uri"]).isEqualTo("com.googleusercontent.apps.1234-abc:/oauth2redirect")
        assertThat(params["response_type"]).isEqualTo("code")
        assertThat(params["scope"]).isEqualTo("openid email profile")
        assertThat(params["code_challenge_method"]).isEqualTo("S256")
        assertThat(params["code_challenge"]).isNotEmpty()
        assertThat(params["state"]).isNotEmpty()
        // Spaces travel as %20, not '+': Google decodes the scope literally.
        assertThat(url).contains("scope=openid%20email%20profile")
    }

    @Test
    fun `only this app's redirect is claimed`() {
        val flow = flow()

        assertThat(flow.offerRedirect(null)).isFalse()
        assertThat(flow.offerRedirect("https://atpost.app/chat/join/abc")).isFalse()
        assertThat(flow.offerRedirect("com.googleusercontent.apps.other:/oauth2redirect?code=x")).isFalse()
        assertThat(flow.pendingRedirect.value).isNull()

        val own = "com.googleusercontent.apps.1234-abc:/oauth2redirect?code=x&state=s"
        assertThat(flow.offerRedirect(own)).isTrue()
        assertThat(flow.pendingRedirect.value).isEqualTo(own)
    }

    @Test
    fun `a redirect with the wrong state is refused without touching the token endpoint`() = runTest {
        val flow = flow()
        flow.begin()

        val result = flow.complete(config.redirectUri + "?code=abc&state=forged")

        assertThat((result as AppResult.Failure).error)
            .isEqualTo(AppError.AuthFailed(code = GoogleSignIn.CODE_STATE_MISMATCH))
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `a redirect without an attempt in flight is refused`() = runTest {
        val result = flow().complete(config.redirectUri + "?code=abc&state=s")

        assertThat(((result as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
    }

    @Test
    fun `the person declining is a cancel, not an error`() = runTest {
        val flow = flow()
        val state = query(flow.begin()).getValue("state")

        val result = flow.complete(config.redirectUri + "?error=access_denied&state=$state")

        assertThat(((result as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_CANCELLED)
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `a good redirect exchanges the code with the PKCE verifier and yields the access token`() = runTest {
        server.enqueue(
            MockResponse.Builder().code(200)
                .body("""{"access_token":"ya29.token","expires_in":3599,"token_type":"Bearer","id_token":"eyJ"}""")
                .build(),
        )
        val flow = flow()
        val authorization = query(flow.begin())
        flow.offerRedirect(config.redirectUri + "?state=${authorization.getValue("state")}&code=4%2Fcode")

        val result = flow.complete(flow.pendingRedirect.value!!)

        assertThat((result as AppResult.Success).data).isEqualTo("ya29.token")
        assertThat(flow.pendingRedirect.value).isNull()
        val request = server.takeRequest()
        assertThat(request.target).isEqualTo("/token")
        assertThat(request.headers["Content-Type"]).startsWith("application/x-www-form-urlencoded")
        val form = formOf(request.body!!.utf8())
        assertThat(form["code"]).isEqualTo("4/code")
        assertThat(form["client_id"]).isEqualTo("1234-abc.apps.googleusercontent.com")
        assertThat(form["redirect_uri"]).isEqualTo(config.redirectUri)
        assertThat(form["grant_type"]).isEqualTo("authorization_code")
        assertThat(form).doesNotContainKey("client_secret")
        // The proof: the challenge in the URL is the SHA-256 of the verifier sent now.
        val verifier = form.getValue("code_verifier")
        val expectedChallenge = Base64.getUrlEncoder().withoutPadding()
            .encodeToString(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))
        assertThat(authorization["code_challenge"]).isEqualTo(expectedChallenge)
        assertThat(verifier.length).isAtLeast(43)
    }

    @Test
    fun `an attempt is spent by its first exchange`() = runTest {
        server.enqueue(MockResponse.Builder().code(200).body("""{"access_token":"t"}""").build())
        val flow = flow()
        val redirect = config.redirectUri + "?state=${query(flow.begin()).getValue("state")}&code=c"

        flow.complete(redirect)
        val replay = flow.complete(redirect)

        assertThat(((replay as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
        assertThat(server.requestCount).isEqualTo(1)
    }

    @Test
    fun `a refused exchange and a token-less body are failures, not sign-ins`() = runTest {
        server.enqueue(MockResponse.Builder().code(400).body("""{"error":"invalid_grant"}""").build())
        server.enqueue(MockResponse.Builder().code(200).body("""{"token_type":"Bearer"}""").build())
        val flow = flow()

        val refused = flow.complete(config.redirectUri + "?state=${query(flow.begin()).getValue("state")}&code=c")
        val empty = flow.complete(config.redirectUri + "?state=${query(flow.begin()).getValue("state")}&code=c")

        assertThat(((refused as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_TOKEN_EXCHANGE)
        assertThat((empty as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
    }

    private fun formOf(body: String): Map<String, String> = body.split('&').associate { pair ->
        URLDecoder.decode(pair.substringBefore('='), "UTF-8") to URLDecoder.decode(pair.substringAfter('='), "UTF-8")
    }
}
