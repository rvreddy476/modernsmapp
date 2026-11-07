package com.us.android.core.auth.oauth

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.di.BareClient
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.Callback
import okhttp3.FormBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.IOException
import java.net.URLDecoder
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64
import java.util.Optional
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * [GoogleSignIn] over Google's OAuth 2.0 endpoints, authorization code + PKCE.
 *
 * Process-scoped on purpose. The browser may hand the redirect to a NEW
 * `MainActivity` instance (it launches the scheme from its own task), so the
 * attempt in flight — verifier and state — lives here, not in a ViewModel,
 * and whichever Login screen is on top can finish it. It is also written to
 * [OAuthAttemptStore] (encrypted, [ATTEMPT_TTL_MILLIS]), because Android may
 * kill the process while the consent page is up; the redirect then starts a
 * fresh process that finds the attempt there instead of failing with a state
 * mismatch.
 *
 * What comes back is Google's ID token, which the server verifies against
 * Google's keys and our client ids. Never the access token: the server refuses
 * it (400 `ID_TOKEN_REQUIRED`), because an access token does not say which app
 * it was issued to.
 *
 * The exchange uses the bare OkHttp client: no auth interceptor (there is no
 * session yet), no CSRF cookie jar, no API-host retry policy. The client id is
 * an installed-app (Android) client, which Google's token endpoint accepts
 * with a `code_verifier` and no client secret — there is none to ship.
 */
@Singleton
class GoogleAuthorizationFlow internal constructor(
    private val config: GoogleOAuthConfig?,
    private val client: OkHttpClient,
    private val json: Json,
    private val endpoints: GoogleEndpoints,
    private val random: SecureRandom,
    private val attemptStore: OAuthAttemptStore = InMemoryOAuthAttemptStore(),
    private val now: () -> Long = System::currentTimeMillis,
) : GoogleSignIn {

    @Inject
    constructor(
        config: Optional<GoogleOAuthConfig>,
        @BareClient client: OkHttpClient,
        json: Json,
        attemptStore: OAuthAttemptStore,
    ) : this(config.orElse(null), client, json, GoogleEndpoints(), SecureRandom(), attemptStore)

    /** Google's hosts. Overridable so a test can point the exchange at MockWebServer. */
    data class GoogleEndpoints(
        val authorization: String = "https://accounts.google.com/o/oauth2/v2/auth",
        val token: String = "https://oauth2.googleapis.com/token",
    )

    @Volatile
    private var attempt: PendingOAuthAttempt? = null

    private val _pendingRedirect = MutableStateFlow<String?>(null)
    override val pendingRedirect: StateFlow<String?> = _pendingRedirect.asStateFlow()

    override val isAvailable: Boolean get() = config?.isConfigured == true

    override fun begin(): String {
        val cfg = requireNotNull(config) { "Google sign-in is not configured" }
        val verifier = randomUrlSafe(VERIFIER_BYTES)
        val state = randomUrlSafe(STATE_BYTES)
        val started = PendingOAuthAttempt(state, verifier, now())
        attempt = started
        attemptStore.save(started)
        _pendingRedirect.value = null
        val params = listOf(
            "client_id" to cfg.clientId,
            "redirect_uri" to cfg.redirectUri,
            "response_type" to "code",
            "scope" to SCOPES,
            "code_challenge" to sha256UrlSafe(verifier),
            "code_challenge_method" to "S256",
            "state" to state,
            // The account chooser every time: a person with two Google
            // accounts must be able to pick, and a cached choice is a trap.
            "prompt" to "select_account",
        )
        return endpoints.authorization + "?" + params.joinToString("&") { (k, v) -> k + "=" + encode(v) }
    }

    override fun offerRedirect(uri: String?): Boolean {
        val cfg = config ?: return false
        if (uri.isNullOrBlank() || !cfg.isConfigured) return false
        if (!uri.startsWith(cfg.redirectUri, ignoreCase = true)) return false
        _pendingRedirect.value = uri
        return true
    }

    override suspend fun complete(redirect: String): AppResult<String> {
        _pendingRedirect.value = null
        val cfg = config ?: return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        val current = currentAttempt() ?: return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        // A redirect another app or a tapped link can send us: a bad escape
        // (`%zz`, a trailing `%`) is a refusal, never a crash, and it does not
        // spend the real attempt still waiting for Google's redirect.
        val query = parseQuery(redirect) ?: return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        // Google names the refusal in `error`; `access_denied` is the person
        // closing the page or declining. Anything else is still a non-event
        // for the user, who gets the generic sentence.
        if (query.containsKey("error")) {
            spend()
            return failed(GoogleSignIn.CODE_CANCELLED)
        }
        if (query["state"] != current.state) return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        val code = query["code"].orEmpty()
        if (code.isBlank()) return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        // One attempt, one exchange: a replayed redirect finds nothing.
        spend()
        return exchange(cfg, code, current.codeVerifier)
    }

    /** The attempt in memory, else the one a dead process left behind, if still fresh. */
    private fun currentAttempt(): PendingOAuthAttempt? {
        val candidate = attempt ?: attemptStore.load() ?: return null
        val age = now() - candidate.createdAtMillis
        if (age < 0 || age > ATTEMPT_TTL_MILLIS) {
            spend()
            return null
        }
        return candidate
    }

    private fun spend() {
        attempt = null
        attemptStore.clear()
    }

    private suspend fun exchange(cfg: GoogleOAuthConfig, code: String, verifier: String): AppResult<String> {
        val body = FormBody.Builder()
            .add("code", code)
            .add("client_id", cfg.clientId)
            .add("redirect_uri", cfg.redirectUri)
            .add("grant_type", "authorization_code")
            .add("code_verifier", verifier)
            .build()
        val request = Request.Builder().url(endpoints.token).post(body).build()
        // The body read is inside the try too: a connection dropped mid-body
        // throws from string(), and that must be "offline", not a crash.
        return try {
            client.newCall(request).await().use { res ->
                val text = res.body?.string().orEmpty()
                if (res.isSuccessful) {
                    idTokenFrom(text)
                } else {
                    AppResult.Failure(AppError.AuthFailed(code = GoogleSignIn.CODE_TOKEN_EXCHANGE))
                }
            }
        } catch (_: IOException) {
            AppResult.Failure(AppError.NoNetwork())
        }
    }

    /**
     * The scope carries `openid`, so Google returns an ID token beside the
     * access token. Only the ID token is read; the access token is never
     * passed on.
     */
    private fun idTokenFrom(text: String): AppResult<String> {
        val idToken = runCatching { json.decodeFromString(TokenResponse.serializer(), text) }
            .getOrNull()?.idToken.orEmpty()
        return if (idToken.isBlank()) {
            AppResult.Failure(AppError.Malformed("token response without id_token"))
        } else {
            AppResult.Success(idToken)
        }
    }

    private fun failed(code: String): AppResult<String> = AppResult.Failure(AppError.AuthFailed(code = code))

    private fun randomUrlSafe(bytes: Int): String {
        val buffer = ByteArray(bytes)
        random.nextBytes(buffer)
        return Base64.getUrlEncoder().withoutPadding().encodeToString(buffer)
    }

    private fun sha256UrlSafe(value: String): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(value.toByteArray(Charsets.US_ASCII))
        return Base64.getUrlEncoder().withoutPadding().encodeToString(digest)
    }

    private fun encode(value: String): String = URLEncoder.encode(value, "UTF-8").replace("+", "%20")

    /** Mirrors Google's token response; only the ID token is read. */
    @Serializable
    private data class TokenResponse(@SerialName("id_token") val idToken: String = "")

    private suspend fun Call.await(): Response = suspendCancellableCoroutine { continuation ->
        enqueue(
            object : Callback {
                override fun onFailure(call: Call, e: IOException) {
                    if (!continuation.isCancelled) continuation.resumeWithException(e)
                }

                override fun onResponse(call: Call, response: Response) {
                    continuation.resume(response)
                }
            },
        )
        continuation.invokeOnCancellation { cancel() }
    }

    internal companion object {
        const val SCOPES = "openid email profile"
        private const val VERIFIER_BYTES = 32
        private const val STATE_BYTES = 16

        /** How long a started attempt may be finished, across a process death too. */
        const val ATTEMPT_TTL_MILLIS = 10 * 60 * 1000L

        /**
         * The redirect's query, decoded. Tolerates a fragment and a missing
         * `?`. Null when an escape is malformed (`%zz`, a trailing `%`):
         * [URLDecoder] throws on those, and this URI comes from outside the app.
         */
        internal fun parseQuery(uri: String): Map<String, String>? {
            val query = uri.substringAfter('?', "").substringBefore('#')
            if (query.isBlank()) return emptyMap()
            return try {
                query.split('&').filter { it.isNotBlank() }.associate { pair ->
                    val key = URLDecoder.decode(pair.substringBefore('='), "UTF-8")
                    val value = URLDecoder.decode(pair.substringAfter('=', ""), "UTF-8")
                    key to value
                }
            } catch (_: IllegalArgumentException) {
                null
            }
        }
    }
}
