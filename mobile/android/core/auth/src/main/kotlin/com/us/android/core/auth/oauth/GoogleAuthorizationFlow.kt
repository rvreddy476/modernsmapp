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
 * and whichever Login screen is on top can finish it.
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
) : GoogleSignIn {

    @Inject
    constructor(
        config: Optional<GoogleOAuthConfig>,
        @BareClient client: OkHttpClient,
        json: Json,
    ) : this(config.orElse(null), client, json, GoogleEndpoints(), SecureRandom())

    /** Google's hosts. Overridable so a test can point the exchange at MockWebServer. */
    data class GoogleEndpoints(
        val authorization: String = "https://accounts.google.com/o/oauth2/v2/auth",
        val token: String = "https://oauth2.googleapis.com/token",
    )

    private class Attempt(val state: String, val codeVerifier: String)

    @Volatile
    private var attempt: Attempt? = null

    private val _pendingRedirect = MutableStateFlow<String?>(null)
    override val pendingRedirect: StateFlow<String?> = _pendingRedirect.asStateFlow()

    override val isAvailable: Boolean get() = config?.isConfigured == true

    override fun begin(): String {
        val cfg = requireNotNull(config) { "Google sign-in is not configured" }
        val verifier = randomUrlSafe(VERIFIER_BYTES)
        val state = randomUrlSafe(STATE_BYTES)
        attempt = Attempt(state, verifier)
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
        val current = attempt ?: return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        val query = parseQuery(redirect)
        // Google names the refusal in `error`; `access_denied` is the person
        // closing the page or declining. Anything else is still a non-event
        // for the user, who gets the generic sentence.
        if (query.containsKey("error")) {
            attempt = null
            return failed(GoogleSignIn.CODE_CANCELLED)
        }
        if (query["state"] != current.state) return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        val code = query["code"].orEmpty()
        if (code.isBlank()) return failed(GoogleSignIn.CODE_STATE_MISMATCH)
        // One attempt, one exchange: a replayed redirect finds nothing.
        attempt = null
        return exchange(cfg, code, current.codeVerifier)
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
        val response = try {
            client.newCall(request).await()
        } catch (e: IOException) {
            return AppResult.Failure(AppError.NoNetwork())
        }
        return response.use { res ->
            val text = res.body?.string().orEmpty()
            if (!res.isSuccessful) {
                AppResult.Failure(AppError.AuthFailed(code = GoogleSignIn.CODE_TOKEN_EXCHANGE))
            } else {
                val token = runCatching { json.decodeFromString(TokenResponse.serializer(), text) }
                    .getOrNull()?.accessToken.orEmpty()
                if (token.isBlank()) {
                    AppResult.Failure(AppError.Malformed("token response without access_token"))
                } else {
                    AppResult.Success(token)
                }
            }
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

    /** Mirrors Google's token response; only the access token is read. */
    @Serializable
    private data class TokenResponse(@SerialName("access_token") val accessToken: String = "")

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

        /** The redirect's query, decoded. Tolerates a fragment and a missing `?`. */
        internal fun parseQuery(uri: String): Map<String, String> {
            val query = uri.substringAfter('?', "").substringBefore('#')
            if (query.isBlank()) return emptyMap()
            return query.split('&').filter { it.isNotBlank() }.associate { pair ->
                val key = URLDecoder.decode(pair.substringBefore('='), "UTF-8")
                val value = URLDecoder.decode(pair.substringAfter('=', ""), "UTF-8")
                key to value
            }
        }
    }
}
