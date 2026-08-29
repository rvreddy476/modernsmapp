package com.us.android.feature.call.ui

import android.Manifest
import android.app.Application
import androidx.activity.result.ActivityResultRegistry
import androidx.activity.result.ActivityResultRegistryOwner
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.core.app.ActivityOptionsCompat
import androidx.lifecycle.SavedStateHandle
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.call.CallAudioController
import com.us.android.core.call.CallNotifier
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.CallState
import com.us.android.core.call.data.CallApi
import com.us.android.core.call.data.CallDto
import com.us.android.core.call.data.CallHistoryItemDto
import com.us.android.core.call.data.CallRepository
import com.us.android.core.call.data.CreateCallRequest
import com.us.android.core.call.data.IceServerConfig
import com.us.android.core.call.data.JoinDto
import com.us.android.core.call.data.PendingInviteDto
import com.us.android.core.call.data.StatusPayload
import com.us.android.core.call.engine.CallEngine
import com.us.android.core.call.engine.CallEngineEvent
import com.us.android.core.call.engine.CallEngineFactory
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.webrtc.EglBase
import org.webrtc.VideoTrack

/**
 * CALL-LB-6, proven on the PRODUCTION surface: the real [CallScreen] with the
 * real [CallViewModel] and real [CallSessionManager], a real Robolectric
 * permission store, and a real (scripted) [ActivityResultRegistry] — no grant
 * boolean is ever injected by hand. The journey the re-review demanded:
 * cold-start video invite → camera denied → still Incoming, no REST accept,
 * no engine → retry (Accept) or Settings-style grant → Accept → exactly one
 * REST accept and engine start.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CallScreenPermissionJourneyTest {

    @get:Rule
    val composeRule = createComposeRule()

    private val app: Application = ApplicationProvider.getApplicationContext()
    private val managerScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    @After
    fun tearDown() {
        managerScope.cancel()
    }

    /**
     * A real registry whose "user" is scripted per permission: every
     * decision is written into the REAL Robolectric permission store before
     * the result is dispatched — exactly what the system dialog does. Every
     * requested permission array is recorded so tests can pin what was (and
     * was NOT) prompted, and when.
     */
    private inner class ScriptedPermissionRegistry(
        var grantCamera: Boolean,
        var grantMic: Boolean = true,
    ) : ActivityResultRegistry() {
        var launches = 0
        val requested = mutableListOf<List<String>>()
        override fun <I, O> onLaunch(
            requestCode: Int,
            contract: ActivityResultContract<I, O>,
            input: I,
            options: ActivityOptionsCompat?,
        ) {
            launches++
            @Suppress("UNCHECKED_CAST")
            val permissions = input as Array<String>
            requested += permissions.toList()
            val result = permissions.associateWith { permission ->
                val grant = when (permission) {
                    Manifest.permission.CAMERA -> grantCamera
                    Manifest.permission.RECORD_AUDIO -> grantMic
                    else -> true
                }
                if (grant) {
                    Shadows.shadowOf(app).grantPermissions(permission)
                } else {
                    Shadows.shadowOf(app).denyPermissions(permission)
                }
                grant
            }
            @Suppress("UNCHECKED_CAST")
            dispatchResult(requestCode, result as O)
        }

        fun cameraEverRequested(): Boolean =
            requested.flatten().contains(Manifest.permission.CAMERA)
    }

    /** The server model: one ringing invite, corrected backend semantics. */
    private class JourneyCallApi(private val video: Boolean) : CallApi {
        val journal = mutableListOf<String>()
        var state = "ringing"
        var inviteStatus = "pending"

        override suspend fun createCall(body: CreateCallRequest): ApiEnvelope<CallDto> =
            error("outgoing is not part of this journey")

        override suspend fun call(callId: String): ApiEnvelope<CallDto> =
            ApiEnvelope(data = CallDto(id = CALL_ID, state = state))

        override suspend fun pendingInvites(): ApiEnvelope<List<PendingInviteDto>> =
            ApiEnvelope(
                data = if (inviteStatus == "pending" && state != "ended") {
                    listOf(
                        PendingInviteDto(
                            inviteId = INVITE_ID,
                            callId = CALL_ID,
                            inviterUserId = PEER_ID,
                            callType = if (video) "direct_video" else "direct_audio",
                            audioOnly = !video,
                            callState = state,
                        ),
                    )
                } else {
                    emptyList()
                },
            )

        override suspend fun join(callId: String): ApiEnvelope<JoinDto> {
            journal += "rest:join"
            if (inviteStatus == "accepted" && state == "ringing") state = "active"
            return ApiEnvelope(data = JoinDto(callId = callId))
        }

        override suspend fun acceptInvite(callId: String, inviteId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:accept"
            inviteStatus = "accepted"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun declineInvite(callId: String, inviteId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:decline"
            state = "ended"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun leave(callId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:leave"
            state = "ended"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun end(callId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:end"
            state = "ended"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun history(): ApiEnvelope<List<CallHistoryItemDto>> =
            ApiEnvelope(data = emptyList())
    }

    private class JourneyEngine : CallEngine {
        private val _events = MutableSharedFlow<CallEngineEvent>(extraBufferCapacity = 16)
        override val events: SharedFlow<CallEngineEvent> = _events.asSharedFlow()
        var started = false
        override fun start(video: Boolean, iceServers: List<IceServerConfig>) {
            started = true
        }
        override suspend fun createOffer(): String = "offer"
        override suspend fun acceptOffer(remoteSdp: String): String = "answer"
        override suspend fun completeWithAnswer(remoteSdp: String) = Unit
        override fun addRemoteIceCandidate(candidate: String, sdpMid: String, sdpMLineIndex: Int) = Unit
        override fun setMuted(muted: Boolean) = Unit
        override fun setVideoEnabled(enabled: Boolean) = Unit
        override fun switchCamera() = Unit
        override fun localVideoTrack(): VideoTrack? = null
        override fun remoteVideoTrack(): VideoTrack? = null
        override fun eglContext(): EglBase.Context? = null
        override fun release() = Unit
    }

    private class QuietSession : ChatSessionManager(
        socket = ChatSocket(OkHttpClient(), "ws://unused"),
        store = journeyStore(),
        tokenProvider = object : com.us.android.core.network.TokenProvider {
            override fun currentAccessToken(): String? = "token"
        },
        repository = ChatRepository(unusedApi(), ErrorMapper(Json { ignoreUnknownKeys = true })),
    ) {
        private val connectionState = MutableStateFlow(ConnectionState.Disconnected)
        override val events: SharedFlow<ChatSocketEvent> =
            MutableSharedFlow<ChatSocketEvent>(extraBufferCapacity = 4).asSharedFlow()
        override val connection: StateFlow<ConnectionState> = connectionState
        override fun start() {
            connectionState.value = ConnectionState.Connected
        }
        override fun subscribeRoom(conversationId: String) = Unit
        override fun unsubscribeRoom(conversationId: String) = Unit
        override fun sendCallFrame(frame: String): Boolean =
            connectionState.value == ConnectionState.Connected
    }

    private class QuietRecorder : CallNotifier, CallAudioController {
        override fun showIncoming(callId: String, callerId: String, video: Boolean) = Unit
        override fun clearIncoming() = Unit
        override fun startOngoing(peerId: String, video: Boolean) = Unit
        override fun stopOngoing() = Unit
        override fun onCallStarted(video: Boolean) = Unit
        override fun setSpeaker(on: Boolean) = Unit
        override fun onCallEnded() = Unit
    }

    private fun buildScreen(
        video: Boolean,
        registry: ScriptedPermissionRegistry,
    ): Triple<JourneyCallApi, JourneyEngine, CallViewModel> {
        val api = JourneyCallApi(video)
        val engine = JourneyEngine()
        val manager = CallSessionManager(
            repository = CallRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
            session = QuietSession(),
            engineFactory = CallEngineFactory { engine },
            notifier = QuietRecorder(),
            audio = QuietRecorder(),
            scope = managerScope,
        )
        // ATTACH mode: no peerId — the cold-start push path. The screen must
        // resolve the invite itself; no chat screen has ever run.
        val viewModel = CallViewModel(manager, SavedStateHandle())
        val owner = object : ActivityResultRegistryOwner {
            override val activityResultRegistry: ActivityResultRegistry = registry
        }
        composeRule.setContent {
            CompositionLocalProvider(
                androidx.activity.compose.LocalActivityResultRegistryOwner provides owner,
            ) {
                CallScreen(onBack = {}, viewModel = viewModel)
            }
        }
        return Triple(api, engine, viewModel)
    }

    @Test
    fun `cold-start video invite - denial keeps it ringing and the accept retry grants and answers once`() {
        // Nothing granted yet; the scripted user will DENY the camera.
        val registry = ScriptedPermissionRegistry(grantCamera = false)
        val (api, engine, viewModel) = buildScreen(video = true, registry = registry)
        composeRule.waitForIdle()

        // Cold start resolved the invite; the surface rings as video.
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)
        assertThat((viewModel.callState.value as CallState.Incoming).video).isTrue()

        // PRE-TAP PIN (CALL-LB-6): rendering an incoming VIDEO invite made
        // exactly one entry request — microphone only. The camera prompt
        // belongs to the Accept gesture, not to the invite appearing.
        assertThat(registry.launches).isEqualTo(1)
        assertThat(registry.cameraEverRequested()).isFalse()

        // ACCEPT with the camera denied: the tap launches the REAL request,
        // the scripted user denies again — still Incoming, no REST accept,
        // no engine, and the refusal line is shown.
        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()
        assertThat(registry.cameraEverRequested()).isTrue()
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)
        assertThat(api.journal).doesNotContain("rest:accept")
        assertThat(engine.started).isFalse()
        composeRule.onNodeWithTag("call-message").assertIsDisplayed()

        // RETRY: the same Accept action launches the request again; this
        // time the user grants. Acceptance continues EXACTLY once.
        registry.grantCamera = true
        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()

        assertThat(api.journal.count { it == "rest:accept" }).isEqualTo(1)
        assertThat(engine.started).isTrue()
        assertThat(api.state).isEqualTo("active")
    }

    @Test
    fun `a revoked microphone blocks the video accept until granted, without declining`() {
        // Camera IS granted; the microphone is the missing leg — the exact
        // trigger the re-review named: only camera was checked at Accept.
        val registry = ScriptedPermissionRegistry(grantCamera = true, grantMic = false)
        Shadows.shadowOf(app).grantPermissions(Manifest.permission.CAMERA)
        val (api, engine, viewModel) = buildScreen(video = true, registry = registry)
        composeRule.waitForIdle()
        // With the mic denied the entry pass never calls onReady, so the
        // invite arrives the way it does in production: the FCM data push
        // wakes the manager's server-verified refresh.
        composeRule.runOnUiThread {
            kotlinx.coroutines.runBlocking { viewModel.manager.refreshIncoming() }
        }
        composeRule.waitForIdle()
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)

        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()

        // No mic → no REST accept, no engine, still Incoming (NOT declined:
        // the permission dialog must never hang up a call).
        assertThat(api.journal).doesNotContain("rest:accept")
        assertThat(api.journal).doesNotContain("rest:decline")
        assertThat(engine.started).isFalse()
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)
        composeRule.onNodeWithTag("call-message").assertIsDisplayed()

        // The user grants the mic on the Accept retry: exactly one accept.
        registry.grantMic = true
        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()

        assertThat(api.journal.count { it == "rest:accept" }).isEqualTo(1)
        assertThat(engine.started).isTrue()
    }

    @Test
    fun `an audio invite accepts with the camera denied`() {
        val registry = ScriptedPermissionRegistry(grantCamera = false)
        val (api, engine, viewModel) = buildScreen(video = false, registry = registry)
        composeRule.waitForIdle()
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)

        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()

        assertThat(api.journal.count { it == "rest:accept" }).isEqualTo(1)
        assertThat(engine.started).isTrue()
    }

    @Test
    fun `a settings-style grant is re-read by the accept action without a restart`() {
        val registry = ScriptedPermissionRegistry(grantCamera = false)
        val (api, engine, viewModel) = buildScreen(video = true, registry = registry)
        composeRule.waitForIdle()

        // Deny through the real dialog first. Robolectric reports no
        // rationale, so this is a PERMANENT denial: the screen must offer
        // Settings and keep the call ringing.
        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()
        assertThat(viewModel.callState.value).isInstanceOf(CallState.Incoming::class.java)
        composeRule.onNodeWithTag("call-open-settings").assertIsDisplayed()
        val launchesAfterDenial = registry.launches

        // The user flips the grant in SETTINGS (no launcher involved) and
        // comes back. The Accept action reads the REAL store, so it must
        // proceed without any new request and without restarting the app.
        Shadows.shadowOf(app).grantPermissions(Manifest.permission.CAMERA)
        composeRule.onNodeWithTag("call-accept").performClick()
        composeRule.waitForIdle()

        assertThat(registry.launches).isEqualTo(launchesAfterDenial)
        assertThat(api.journal.count { it == "rest:accept" }).isEqualTo(1)
        assertThat(engine.started).isTrue()
    }

    private companion object {
        const val CALL_ID = "cccccccc-0000-0000-0000-000000000001"
        const val INVITE_ID = "dddddddd-0000-0000-0000-000000000001"
        const val PEER_ID = "bbbbbbbb-0000-0000-0000-000000000002"
    }
}

// ── minimal chat collaborators (same shape as :core:call's test fakes) ──────

private class JourneyChatDao : ChatDao {
    override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
    override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
    override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
    override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
    override suspend fun clearUnread(conversationId: String) = Unit
    override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
    override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
    override suspend fun rawCheckpoint(query: androidx.sqlite.db.SupportSQLiteQuery): Int = 0
    override suspend fun deleteMessage(messageId: String) = Unit
    override suspend fun deleteConversation(conversationId: String) = Unit
    override suspend fun clearConversations() = Unit
    override suspend fun insertMessages(rows: List<ChatMessageEntity>) = Unit
    override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
    override suspend fun clearMessages() = Unit
    override suspend fun enqueueSend(row: ChatPendingSendEntity) = Unit
    override suspend fun pendingSends(): List<ChatPendingSendEntity> = emptyList()
    override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
        MutableStateFlow(emptyList())
    override suspend fun recordAttempt(key: String) = Unit
    override suspend fun markFailed(key: String) = Unit
    override suspend fun retry(key: String) = Unit
    override suspend fun completeSend(key: String) = Unit
    override suspend fun clearPendingSends() = Unit
}

private fun journeyStore(): ChatStore = ChatStore(
    repository = ChatRepository(unusedApi(), ErrorMapper(Json { ignoreUnknownKeys = true })),
    dao = JourneyChatDao(),
    scheduler = object : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    },
    scrubRecovery = object : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    },
)

private fun unusedApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
