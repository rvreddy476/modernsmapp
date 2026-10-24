package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.isSealed
import com.us.android.core.model.SessionState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.Authenticator
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.CopyOnWriteArrayList
import javax.inject.Provider

/**
 * The hidden-chat calls through a real Retrofit and OkHttp stack, against a
 * scripted server: which headers ride which call, that a refused secret is
 * never replayed, how search quietly tries a key, and how each refusal
 * re-locks.
 */
class HiddenChatsHttpTest {

    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<RecordedRequest>()
    private val routes = mutableMapOf<String, () -> MockResponse>()
    private var refreshes = 0
    private lateinit var tokens: HiddenChatTokens
    private val vault = FakeVault()

    // Hidden chats × outbox: the store's queue, drains and key, observable.
    private val chatDao = FakeChatDao()
    private val outboxScheduler = CountingScheduler()
    private val outboxKeys = SoftwareOutboxKeys()
    private lateinit var store: com.us.android.core.chat.data.ChatStore

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                requests += request
                val key = "${request.method} ${request.url.encodedPath}"
                return routes[key]?.invoke() ?: ok("""{"data":{}}""")
            }
        }
        server.start()
    }

    /** The idle timer's scope: real time, so a minute never passes inside a test. */
    private val timerScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    @After
    fun tearDown() {
        timerScope.cancel()
        server.close()
    }

    private fun ok(body: String, vararg headers: Pair<String, String>) =
        MockResponse.Builder().code(200).body(body).apply { headers.forEach { (k, v) -> addHeader(k, v) } }.build()

    private fun status(code: Int, body: String) = MockResponse.Builder().code(code).body(body).build()

    private fun route(method: String, path: String, response: () -> MockResponse) {
        routes["$method $path"] = response
    }

    /** Counts refreshes; answers with a new bearer so OkHttp would replay. */
    private val sharedAuthenticator = Authenticator { _, response ->
        refreshes++
        if (response.priorResponse != null) null else response.request.newBuilder().header("Authorization", "Bearer fresh").build()
    }

    private fun retrofit(): Retrofit {
        val client = OkHttpClient.Builder()
            .addInterceptor(HiddenChatHeadersInterceptor(tokens) { "device-1" })
            .authenticator(HiddenAwareAuthenticator(sharedAuthenticator))
            .build()
        return Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(client)
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
    }

    private fun hidden(scope: CoroutineScope): HiddenChats {
        tokens = HiddenChatTokens(timerScope)
        val api = retrofit().create(HiddenChatsApi::class.java)
        val session = object : SessionStateProvider {
            override val sessionState: StateFlow<SessionState> = MutableStateFlow(SessionState.Authenticated("me", "s1"))
        }
        val store = testStore(chatDao, InboxApi(), tokens, outboxScheduler, AesGcmOutboxCipher(outboxKeys))
        this.store = store
        return HiddenChats(api, tokens, vault, Provider { store }, Provider { session }, scope)
    }

    private fun sent(path: String) = requests.filter { it.url.encodedPath == path }

    private val configured = """{"data":{"configured":true,"strict":false,"shortcut":null}}"""
    private val notConfigured = """{"data":{"configured":false,"strict":false,"shortcut":null}}"""
    private val revealed = """{"data":{"reveal_token":"rv-1","expires_at":"2099-01-01T00:00:00Z"}}"""
    private val opened = """{"data":{"open_token":"op-1","expires_at":"2099-01-01T00:00:00Z"}}"""
    private val hiddenList = """{"data":[{"id":"h1","type":"direct","locked":true,"members":[]}]}"""

    // ── Search as key ─────────────────────────────────────────────────────

    @Test
    fun `an account without hidden chats never sends a key`() = runTest {
        route("GET", "/v1/chat/hidden/settings") { ok(notConfigured) }
        val h = hidden(backgroundScope)
        h.loadSettings()

        assertThat(h.revealFromSearch("abcd1234")).isFalse()
        assertThat(sent("/v1/chat/hidden/reveal")).isEmpty()
    }

    @Test
    fun `settings not yet read means no key is sent either`() = runTest {
        val h = hidden(backgroundScope)
        assertThat(h.revealFromSearch("abcd1234")).isFalse()
        assertThat(sent("/v1/chat/hidden/reveal")).isEmpty()
    }

    @Test
    fun `text that is not key-shaped is never sent`() = runTest {
        route("GET", "/v1/chat/hidden/settings") { ok(configured) }
        val h = hidden(backgroundScope)
        h.loadSettings()

        assertThat(h.revealFromSearch("hello world")).isFalse()
        assertThat(h.revealFromSearch("abc")).isFalse()
        assertThat(sent("/v1/chat/hidden/reveal")).isEmpty()
    }

    @Test
    fun `a wrong key looks like a normal search, and the same text is not tried twice`() = runTest {
        route("GET", "/v1/chat/hidden/settings") { ok(configured) }
        route("POST", "/v1/chat/hidden/reveal") { status(404, """{"error":{"code":"NOT_FOUND","message":"Not found"}}""") }
        val h = hidden(backgroundScope)
        h.loadSettings()

        assertThat(h.revealFromSearch("wrongkey")).isFalse()
        assertThat(h.revealFromSearch("wrongkey")).isFalse()
        assertThat(sent("/v1/chat/hidden/reveal")).hasSize(1)
        assertThat(h.tokens.snapshot.value.revealed).isFalse()
        assertThat(h.rows.value).isNull()

        assertThat(h.revealFromSearch("otherkey")).isFalse()
        assertThat(sent("/v1/chat/hidden/reveal")).hasSize(2)
    }

    @Test
    fun `a lockout during search is just as quiet`() = runTest {
        route("GET", "/v1/chat/hidden/settings") { ok(configured) }
        route("POST", "/v1/chat/hidden/reveal") { status(429, """{"error":{"code":"LOCKED","details":{"retry_after":900}}}""") }
        val h = hidden(backgroundScope)
        h.loadSettings()

        assertThat(h.revealFromSearch("abcd1234")).isFalse()
        assertThat(h.tokens.snapshot.value.revealed).isFalse()
    }

    @Test
    fun `the right key reveals the list and marks its chats hidden`() = runTest {
        route("GET", "/v1/chat/hidden/settings") { ok(configured) }
        route("POST", "/v1/chat/hidden/reveal") { ok(revealed) }
        route("GET", "/v1/chat/hidden/conversations") { ok(hiddenList) }
        val h = hidden(backgroundScope)
        h.loadSettings()

        assertThat(h.revealFromSearch("  abcd1234 ")).isTrue()

        val reveal = sent("/v1/chat/hidden/reveal").single()
        assertThat(reveal.body!!.utf8()).isEqualTo("""{"secret_key":"abcd1234"}""")
        assertThat(reveal.headers["X-Device-Id"]).isEqualTo("device-1")
        val list = sent("/v1/chat/hidden/conversations").single()
        assertThat(list.headers["X-Chat-Reveal"]).isEqualTo("rv-1")
        assertThat(h.rows.value!!.map { it.id }).containsExactly("h1")
        assertThat(h.tokens.isLocked("h1")).isTrue()
    }

    // ── Open ──────────────────────────────────────────────────────────────

    private suspend fun revealedHidden(scope: CoroutineScope): HiddenChats {
        route("GET", "/v1/chat/hidden/settings") { ok(configured) }
        route("POST", "/v1/chat/hidden/reveal") { ok(revealed) }
        route("GET", "/v1/chat/hidden/conversations") { ok(hiddenList) }
        val h = hidden(scope)
        h.loadSettings()
        check(h.revealFromSearch("abcd1234"))
        return h
    }

    @Test
    fun `a wrong chat password is one try, never refreshed and replayed`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") {
            status(401, """{"error":{"code":"WRONG_PASSWORD","message":"Wrong chat password","details":{"attempts_left":4}}}""")
        }

        val result = h.open("not-it-1")

        assertThat(result).isEqualTo(HiddenResult.Err(HiddenError.WrongPassword(4)))
        assertThat(sent("/v1/chat/hidden/open")).hasSize(1)
        assertThat(refreshes).isEqualTo(0)
        assertThat(h.tokens.snapshot.value.opened).isFalse()
        assertThat(sent("/v1/chat/hidden/open").single().headers["X-Chat-Reveal"]).isEqualTo("rv-1")
    }

    @Test
    fun `an expired bearer is still refreshed and replayed`() = runTest {
        val h = revealedHidden(backgroundScope)
        var first = true
        route("POST", "/v1/chat/hidden/open") {
            if (first) {
                first = false
                status(401, """{"error":{"code":"AUTH_FAILED"}}""")
            } else {
                ok(opened)
            }
        }

        assertThat(h.open("right-pass")).isEqualTo(HiddenResult.Ok(Unit))
        assertThat(refreshes).isEqualTo(1)
        assertThat(sent("/v1/chat/hidden/open")).hasSize(2)
    }

    @Test
    fun `the open token rides a hidden chat's calls and nothing else`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        assertThat(h.open("right-pass")).isEqualTo(HiddenResult.Ok(Unit))
        val chat = retrofit().create(ChatApi::class.java)

        runCatching { chat.messages("h1", limit = 20) }
        runCatching { chat.messages("c2", limit = 20) }
        runCatching { chat.conversations() }

        val hiddenCall = sent("/v1/chat/conversations/h1/messages").single()
        assertThat(hiddenCall.headers["X-Chat-Open"]).isEqualTo("op-1")
        assertThat(hiddenCall.headers["X-Device-Id"]).isEqualTo("device-1")
        val normal = sent("/v1/chat/conversations/c2/messages").single()
        assertThat(normal.headers["X-Chat-Open"]).isNull()
        assertThat(normal.headers["X-Device-Id"]).isNull()
        assertThat(sent("/v1/chat/conversations").single().headers["X-Chat-Open"]).isNull()
    }

    @Test
    fun `a 403 CHAT_LOCKED marks the chat hidden and drops a refused open token`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        h.open("right-pass")
        route("GET", "/v1/chat/conversations/h1/messages") { status(403, """{"error":{"code":"CHAT_LOCKED"}}""") }
        route("GET", "/v1/chat/conversations/x9/messages") { status(403, """{"error":{"code":"CHAT_LOCKED"}}""") }
        val chat = retrofit().create(ChatApi::class.java)

        // A chat this process did not know was hidden: learned, nothing dropped.
        runCatching { chat.messages("x9", limit = 20) }
        assertThat(h.tokens.isLocked("x9")).isTrue()
        assertThat(h.tokens.snapshot.value.opened).isTrue()

        // The token itself refused: the chats close; the list stays.
        runCatching { chat.messages("h1", limit = 20) }
        assertThat(h.tokens.snapshot.value.opened).isFalse()
        assertThat(h.tokens.snapshot.value.revealed).isTrue()
    }

    @Test
    fun `a refused reveal token re-locks the list`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("GET", "/v1/chat/hidden/conversations") { status(404, """{"error":{"code":"NOT_FOUND"}}""") }

        assertThat(h.refreshList()).isEqualTo(HiddenResult.Err(HiddenError.NotFound))
        assertThat(h.tokens.snapshot.value.revealed).isFalse()
        assertThat(h.tokens.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Server)
    }

    @Test
    fun `opening without a reveal token sends nothing`() = runTest {
        val h = hidden(backgroundScope)
        assertThat(h.open("whatever1")).isEqualTo(HiddenResult.Err(HiddenError.NotFound))
        assertThat(sent("/v1/chat/hidden/open")).isEmpty()
    }

    // ── Settings, hide, unhide, sign-out ─────────────────────────────────

    @Test
    fun `setup and change send the pinned partial bodies`() = runTest {
        route("PUT", "/v1/chat/hidden/settings") { ok(configured) }
        val h = hidden(backgroundScope)

        h.setUp("abcd1234", "secret-pass")
        h.change("secret-pass", newSecretKey = "wxyz9876")

        val puts = sent("/v1/chat/hidden/settings").filter { it.method == "PUT" }
        assertThat(puts[0].body!!.utf8()).isEqualTo("""{"secret_key":"abcd1234","chat_password":"secret-pass"}""")
        assertThat(puts[1].body!!.utf8()).isEqualTo("""{"secret_key":"wxyz9876","current_chat_password":"secret-pass"}""")
        assertThat(puts[0].headers["X-Device-Id"]).isEqualTo("device-1")
    }

    @Test
    fun `a changed secret re-locks this device and a changed password forgets the sealed copy`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("PUT", "/v1/chat/hidden/settings") { ok(configured) }
        vault.sealed["me"] = "old-pass"

        h.change("old-pass", newChatPassword = "new-pass-1")

        assertThat(h.tokens.snapshot.value.revealed).isFalse()
        assertThat(h.tokens.snapshot.value.lastDrop).isEqualTo(HiddenDropReason.Changed)
        assertThat(vault.has("me")).isFalse()
    }

    @Test
    fun `strict mode alone re-locks nothing`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("PUT", "/v1/chat/hidden/settings") { ok("""{"data":{"configured":true,"strict":true}}""") }

        h.change("pass-word", strict = true)

        assertThat(sent("/v1/chat/hidden/settings").last().body!!.utf8())
            .isEqualTo("""{"current_chat_password":"pass-word","strict":true}""")
        assertThat(h.tokens.snapshot.value.revealed).isTrue()
        assertThat(h.settings.value!!.strict).isTrue()
    }

    @Test
    fun `the settings refusal keeps its meaning`() = runTest {
        route("PUT", "/v1/chat/hidden/settings") { status(400, """{"error":{"code":"SECRETS_MUST_DIFFER"}}""") }
        val h = hidden(backgroundScope)
        assertThat(h.setUp("abcd1234", "abcd1234")).isEqualTo(HiddenResult.Err(HiddenError.SecretsMustDiffer))
    }

    @Test
    fun `reset carries the step-up token, or only the fresh bearer`() = runTest {
        route("POST", "/v1/chat/hidden/reset") { ok("""{"data":{}}""") }
        val h = hidden(backgroundScope)

        h.reset("abcd1234", "secret-pass", stepUpToken = "su-1")
        h.reset("abcd1234", "secret-pass", stepUpToken = null, strict = true)

        val resets = sent("/v1/chat/hidden/reset")
        assertThat(resets[0].headers["X-Step-Up-Token"]).isEqualTo("su-1")
        assertThat(resets[0].body!!.utf8()).isEqualTo("""{"secret_key":"abcd1234","chat_password":"secret-pass"}""")
        assertThat(resets[1].headers["X-Step-Up-Token"]).isNull()
        assertThat(resets[1].body!!.utf8()).contains("\"strict\":true")
    }

    @Test
    fun `reset without re-auth is STEP_UP_REQUIRED`() = runTest {
        route("POST", "/v1/chat/hidden/reset") { status(403, """{"error":{"code":"STEP_UP_REQUIRED"}}""") }
        val h = hidden(backgroundScope)
        assertThat(h.reset("abcd1234", "secret-pass", null)).isEqualTo(HiddenResult.Err(HiddenError.StepUpRequired))
    }

    @Test
    fun `a wrong 2-step code is not replayed either`() = runTest {
        route("POST", "/v1/auth/step-up") { status(401, """{"error":{"code":"INVALID_OTP"}}""") }
        val h = hidden(backgroundScope)
        assertThat(h.stepUp("123456")).isEqualTo(StepUpOutcome.WrongCode)
        assertThat(sent("/v1/auth/step-up")).hasSize(1)
        assertThat(refreshes).isEqualTo(0)
    }

    @Test
    fun `a step-up answers the token`() = runTest {
        route("POST", "/v1/auth/step-up") { ok("""{"data":{"access_token":"su-9"}}""") }
        val h = hidden(backgroundScope)
        assertThat(h.stepUp(" 123456 ")).isEqualTo(StepUpOutcome.Ok("su-9"))
        assertThat(sent("/v1/auth/step-up").single().body!!.utf8()).isEqualTo("""{"otp":"123456"}""")
    }

    @Test
    fun `hide before setup is HIDDEN_CHATS_NOT_CONFIGURED, and hide marks the chat hidden`() = runTest {
        route("POST", "/v1/chat/conversations/c1/hide") { status(409, """{"error":{"code":"HIDDEN_CHATS_NOT_CONFIGURED"}}""") }
        val h = hidden(backgroundScope)
        assertThat(h.hide("c1")).isEqualTo(HiddenResult.Err(HiddenError.NotConfigured))
        assertThat(h.tokens.isLocked("c1")).isFalse()

        route("POST", "/v1/chat/conversations/c1/hide") { ok("""{"data":{"locked":true}}""") }
        assertThat(h.hide("c1")).isEqualTo(HiddenResult.Ok(Unit))
        assertThat(h.tokens.isLocked("c1")).isTrue()
    }

    @Test
    fun `unhide carries the open token and brings the chat back`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        h.open("right-pass")

        assertThat(h.unhide("h1")).isEqualTo(HiddenResult.Ok(Unit))

        assertThat(sent("/v1/chat/conversations/h1/unhide").single().headers["X-Chat-Open"]).isEqualTo("op-1")
        assertThat(h.tokens.isLocked("h1")).isFalse()
        assertThat(h.rows.value!!.map { it.id }).doesNotContain("h1")
    }

    @Test
    fun `unhide needs the chats open`() = runTest {
        val h = revealedHidden(backgroundScope)
        assertThat(h.unhide("h1")).isEqualTo(HiddenResult.Err(HiddenError.ChatLocked))
        assertThat(sent("/v1/chat/conversations/h1/unhide")).isEmpty()
    }

    @Test
    fun `background re-locks, empties the list and tells the server`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        h.open("right-pass")

        h.onAppBackgrounded()
        // The lock call runs on the app (background) scope.
        testScheduler.runCurrent()

        assertThat(h.tokens.reveal()).isNull()
        assertThat(h.tokens.open()).isNull()
        assertThat(h.rows.value).isNull()
        // The ids stay known, so the normal list keeps leaving them out.
        assertThat(h.tokens.isLocked("h1")).isTrue()
        awaitRequest("/v1/chat/hidden/lock")
    }

    @Test
    fun `sign-out forgets everything and tells nobody`() = runTest {
        val h = revealedHidden(backgroundScope)
        vault.sealed["me"] = "pass"

        h.forgetForSignOut()
        testScheduler.advanceUntilIdle()

        assertThat(h.tokens.snapshot.value.revealed).isFalse()
        assertThat(h.tokens.lockedIds.value).isEmpty()
        assertThat(h.rows.value).isNull()
        assertThat(h.settings.value).isNull()
        assertThat(vault.forgotten).contains(null)
        assertThat(sent("/v1/chat/hidden/lock")).isEmpty()
    }

    @Test
    fun `a stale sealed password is forgotten the first time it is refused`() = runTest {
        val h = revealedHidden(backgroundScope)
        vault.sealed["me"] = "stale"
        route("POST", "/v1/chat/hidden/open") { status(401, """{"error":{"code":"WRONG_PASSWORD","details":{"attempts_left":3}}}""") }

        h.openWithSealedPassword("stale")

        assertThat(vault.has("me")).isFalse()
    }

    // ── Hidden chats × outbox ────────────────────────────────────────────

    private fun queued(key: String, conversationId: String, text: String) = com.us.android.core.database.ChatPendingSendEntity(
        idempotencyKey = key,
        conversationId = conversationId,
        text = text,
        mediaId = null,
        createdAtMillis = 1,
        attempts = 0,
        failed = false,
    )

    @Test
    fun `reading the hidden list seals a plain row queued for one of its chats`() = runTest {
        chatDao.sends["old"] = queued("old", "h1", "typed before the fix")
        chatDao.sends["n"] = queued("n", "c2", "normal")

        revealedHidden(backgroundScope)

        assertThat(chatDao.sends["old"]!!.isSealed()).isTrue()
        assertThat(chatDao.sends["old"].toString()).doesNotContain("typed before the fix")
        assertThat(chatDao.sends["n"]!!.text).isEqualTo("normal")
    }

    @Test
    fun `opening the chats sends what waited for them`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        val before = outboxScheduler.drainsAsked

        h.open("right-pass")

        assertThat(outboxScheduler.drainsAsked).isGreaterThan(before)
    }

    @Test
    fun `a refused open asks for no drain`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { status(401, """{"error":{"code":"WRONG_PASSWORD"}}""") }
        val before = outboxScheduler.drainsAsked

        h.open("wrong-pass")

        assertThat(outboxScheduler.drainsAsked).isEqualTo(before)
    }

    @Test
    fun `a reset (strict or not) drops the hidden chats' queued sends and the outbox key`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        h.open("right-pass")
        val hiddenKey = store.enqueueSend("h1", "the secret plan")!!
        val normalKey = store.enqueueSend("c2", "hello")!!
        assertThat(chatDao.sends.keys).containsExactly(hiddenKey, normalKey)
        route("POST", "/v1/chat/hidden/reset") { ok("""{"data":{}}""") }

        h.reset("abcd1234", "secret-pass", stepUpToken = null, strict = true)

        assertThat(chatDao.sends.keys).containsExactly(normalKey)
        assertThat(outboxKeys.destroyed).isEqualTo(1)
    }

    @Test
    fun `a refused reset keeps the queue`() = runTest {
        val h = revealedHidden(backgroundScope)
        route("POST", "/v1/chat/hidden/open") { ok(opened) }
        h.open("right-pass")
        val hiddenKey = store.enqueueSend("h1", "the secret plan")!!
        route("POST", "/v1/chat/hidden/reset") { status(403, """{"error":{"code":"STEP_UP_REQUIRED"}}""") }

        h.reset("abcd1234", "secret-pass", stepUpToken = null)

        assertThat(chatDao.sends.keys).containsExactly(hiddenKey)
        assertThat(outboxKeys.destroyed).isEqualTo(0)
    }

    /** The lock call is fire-and-forget on the app scope; wait for it on the real network thread. */
    private fun awaitRequest(path: String) {
        val deadline = System.currentTimeMillis() + 5_000
        while (sent(path).isEmpty() && System.currentTimeMillis() < deadline) Thread.sleep(20)
        assertWithMessage("seen: " + requests.map { it.method + " " + it.url.encodedPath }).that(sent(path)).isNotEmpty()
    }
}
