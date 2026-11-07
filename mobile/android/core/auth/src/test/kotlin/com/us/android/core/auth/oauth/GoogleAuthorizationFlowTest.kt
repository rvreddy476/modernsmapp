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

    /** Survives the flow object, as the encrypted prefs survive a process death. */
    private val attemptStore = InMemoryOAuthAttemptStore()
    private var nowMillis = 1_000_000L

    private fun flow(config: GoogleOAuthConfig? = this.config) = GoogleAuthorizationFlow(
        config = config,
        client = OkHttpClient(),
        json = json,
        endpoints = GoogleAuthorizationFlow.GoogleEndpoints(
            authorization = "https://accounts.example/auth",
            token = server.url("/token").toString(),
        ),
        random = SecureRandom(),
        attemptStore = attemptStore,
        now = { nowMillis },
    )

    private fun query(url: String): Map<String, String> = checkNotNull(GoogleAuthorizationFlow.parseQuery(url))

    private fun tokenBody(idToken: String = "eyJ.id.token") =
        MockResponse.Builder().code(200)
            .body("""{"access_token":"ya29.access","expires_in":3599,"token_type":"Bearer","id_token":"$idToken"}""")
            .build()

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
    fun `a good redirect exchanges the code with the PKCE verifier and yields the ID token`() = runTest {
        server.enqueue(tokenBody(idToken = "eyJhbGciOiJSUzI1NiJ9.claims.sig"))
        val flow = flow()
        val authorization = query(flow.begin())
        flow.offerRedirect(config.redirectUri + "?state=${authorization.getValue("state")}&code=4%2Fcode")

        val result = flow.complete(flow.pendingRedirect.value!!)

        // The server verifies an ID token's audience and refuses an access
        // token (400 ID_TOKEN_REQUIRED), so only the ID token may come out.
        assertThat((result as AppResult.Success).data).isEqualTo("eyJhbGciOiJSUzI1NiJ9.claims.sig")
        assertThat(result.data).isNotEqualTo("ya29.access")
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
        server.enqueue(tokenBody())
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

    @Test
    fun `an access token without an ID token is not a sign-in`() = runTest {
        // What Google answers when `openid` is missing from the scope: the
        // access token alone is useless to the server, so it is never passed on.
        server.enqueue(
            MockResponse.Builder()
                .code(200)
                .body("""{"access_token":"ya29.access","token_type":"Bearer"}""")
                .build(),
        )
        val flow = flow()

        val result = flow.complete(config.redirectUri + "?state=${query(flow.begin()).getValue("state")}&code=c")

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
    }

    // ── L6: malformed redirects and process death ───────────────────────

    @Test
    fun `a malformed escape in the redirect is a refusal, not a crash`() = runTest {
        assertThat(GoogleAuthorizationFlow.parseQuery("x:/r?code=%zz&state=s")).isNull()
        assertThat(GoogleAuthorizationFlow.parseQuery("x:/r?code=abc%")).isNull()

        val flow = flow()
        val state = query(flow.begin()).getValue("state")

        val bad = flow.complete(config.redirectUri + "?code=%zz&state=$state")
        val trailing = flow.complete(config.redirectUri + "?state=$state&code=abc%")

        assertThat(((bad as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
        assertThat(((trailing as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
        assertThat(server.requestCount).isEqualTo(0)

        // A junk redirect does not spend the real attempt: Google's own
        // redirect, arriving after it, still signs in.
        server.enqueue(tokenBody())
        val real = flow.complete(config.redirectUri + "?state=$state&code=c")
        assertThat((real as AppResult.Success).data).isEqualTo("eyJ.id.token")
    }

    @Test
    fun `the attempt survives a process death during the browser step`() = runTest {
        server.enqueue(tokenBody())
        val before = flow()
        val authorization = query(before.begin())

        // The process dies: a new flow object, nothing in memory, the store kept.
        val after = flow()
        nowMillis += 60_000L
        val result = after.complete(config.redirectUri + "?state=${authorization.getValue("state")}&code=c")

        assertThat((result as AppResult.Success).data).isEqualTo("eyJ.id.token")
        val verifier = formOf(server.takeRequest().body!!.utf8()).getValue("code_verifier")
        val challenge = Base64.getUrlEncoder().withoutPadding()
            .encodeToString(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))
        assertThat(challenge).isEqualTo(authorization["code_challenge"])
        // Spent: the stored copy is gone too, so a replay finds nothing.
        assertThat(attemptStore.load()).isNull()
    }

    @Test
    fun `a restored attempt still has to match the state`() = runTest {
        flow().begin()

        val result = flow().complete(config.redirectUri + "?state=forged&code=c")

        assertThat(((result as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
        assertThat(server.requestCount).isEqualTo(0)
    }

    @Test
    fun `a stored attempt older than its lifetime is dropped`() = runTest {
        val state = query(flow().begin()).getValue("state")
        nowMillis += GoogleAuthorizationFlow.ATTEMPT_TTL_MILLIS + 1

        val result = flow().complete(config.redirectUri + "?state=$state&code=c")

        assertThat(((result as AppResult.Failure).error as AppError.AuthFailed).code)
            .isEqualTo(GoogleSignIn.CODE_STATE_MISMATCH)
        assertThat(server.requestCount).isEqualTo(0)
        assertThat(attemptStore.load()).isNull()
    }

    private fun formOf(body: String): Map<String, String> = body.split('&').associate { pair ->
        URLDecoder.decode(pair.substringBefore('='), "UTF-8") to URLDecoder.decode(pair.substringAfter('='), "UTF-8")
    }
}
