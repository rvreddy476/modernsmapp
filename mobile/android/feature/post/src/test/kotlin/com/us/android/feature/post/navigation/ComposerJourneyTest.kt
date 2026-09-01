package com.us.android.feature.post.navigation

import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextInput
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.database.ComposerDraftDao
import com.us.android.core.database.ComposerDraftEntity
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.upload.MediaAltTextRequest
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaConfirmRequest
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaInitRequest
import com.us.android.core.media.upload.MediaStatusDto
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.post.composer.ComposerDraftStore
import com.us.android.feature.post.composer.ComposerScreen
import com.us.android.feature.post.composer.ComposerViewModel
import com.us.android.feature.post.composer.CreationKeyFactory
import com.us.android.feature.post.composer.ImageSourceResolver
import com.us.android.feature.post.composer.PickedImage
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.dto.CreatePostRequest
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.ByteArrayInputStream

/**
 * Stands in for the feed, which is where the composer is opened from.
 *
 * TOP-LEVEL, not nested in the test class: kotlinx.serialization resolves an
 * object serializer reflectively through its INSTANCE field, and a private
 * nested object is not reachable that way.
 */
@Serializable
internal data object FeedStub

/** Stands in for the post the composer navigates to on success. */
@Serializable
internal data class PostStub(val postId: String)

/**
 * The composer as a REGISTERED DESTINATION — C-LB-7.2, 7.3 and 7.4 (NC-C7A).
 *
 * ## WHY THIS EXISTS
 *
 * The Codex review refused the emulator journey as proof. `ComposerViewModelTest`
 * covers orchestration and `ComposerReducerTest` covers meaning, but neither
 * puts the real `ComposerScreen` inside a real `NavHost`, and three of the
 * launch-blocker behaviours only exist at that seam:
 *
 *  - SYSTEM BACK. `BackHandler` is a composition-scoped effect. A test that
 *    calls `viewModel.onDiscardRequested()` proves the reducer, not the
 *    handler — and the defect was precisely that the gesture and the hardware
 *    key bypassed the confirmation entirely while the top-bar arrow honoured
 *    it. Only a real `OnBackPressedDispatcher` dispatch catches that, which is
 *    why the host is a real `ComponentActivity`;
 *  - THE BACK STACK AFTER PUBLISH. `onPublished` replaces the composer
 *    (`popUpTo(inclusive)`), so Back from the new post returns to the feed
 *    rather than to a composer whose content is already public. That is host
 *    wiring; no ViewModel test can see it;
 *  - THE VALIDATION SURFACE. That Post is disabled is a reducer fact; that the
 *    person is TOLD WHY, in the control's own accessibility description and in
 *    visible error text, is a rendering fact.
 *
 * ## WHY THE DESTINATION IS REGISTERED HERE RATHER THAN VIA composerScreen()
 *
 * The real `composerScreen()` builder resolves its ViewModel through
 * `hiltViewModel()`. Hosting it would need a Hilt test graph and would put a
 * network stack behind a navigation assertion, so the test would fail for
 * reasons unrelated to what it asserts. What is kept REAL is everything that
 * can actually drift: the route type `ComposerRoute` (whose serializer is
 * resolved at runtime — a missing serialization plugin killed `:feature:chat`
 * on its first frame while every unit test passed), the whole `ComposerScreen`
 * composable, the real `ComposerViewModel`, and the host's own pop semantics
 * copied from `UsNavHost`.
 *
 * It is a Robolectric UNIT test so it runs on the `testDebugUnitTest` the gate
 * already executes. A test that needs a device attached is a test that does not
 * run.
 */
@RunWith(RobolectricTestRunner::class)
// A real phone viewport. Robolectric's default window is small enough that
// the composer's lower controls land off-screen, where a click resolves to
// nothing and the test fails for a reason that has nothing to do with the
// behaviour under test.
@Config(sdk = [34], qualifiers = "w411dp-h891dp")
class ComposerJourneyTest {

    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private val json = Json { ignoreUnknownKeys = true }

    private lateinit var navController: NavHostController

    /** The real ViewModel the hosted screen is driven by. */
    private lateinit var viewModelUnderTest: ComposerViewModel
    private val dao = FakeDraftDao()
    private val repository = RecordingRepository()
    private val uploadApi = FakeUploadApi()

    private val currentRoute: String?
        get() = navController.currentBackStackEntry?.destination?.route

    /** Builds the graph and hosts the real screen on the real route. */
    private fun launchComposer() {
        val viewModel = ComposerViewModel(
            repository = repository,
            uploader = MediaUploader(uploadApi, AlwaysSucceedingPut(), ErrorMapper(json)),
            imageSources = ImageSourceResolver { uri ->
                PickedImage(
                    uri = uri,
                    mimeType = "image/jpeg",
                    sizeBytes = 1024,
                    source = UploadSource { ByteArrayInputStream(ByteArray(1024)) },
                )
            },
            drafts = ComposerDraftStore(dao, json),
            keys = CreationKeyFactory { "key-1" },
        )
        viewModelUnderTest = viewModel

        composeRule.setContent {
            navController = rememberNavController()
            UsTheme {
                NavHost(navController = navController, startDestination = FeedStub) {
                    composable<FeedStub> { Text(FEED_MARKER) }
                    composable<PostStub> { entry ->
                        Text("post:" + entry.toRoute<PostStub>().postId)
                    }
                    composable<ComposerRoute> {
                        ComposerScreen(
                            onClose = { navController.popBackStack() },
                            // Copied verbatim from UsNavHost: the created post
                            // REPLACES the composer in the back stack.
                            onPublished = { postId ->
                                navController.navigate(PostStub(postId)) {
                                    popUpTo<ComposerRoute> { inclusive = true }
                                }
                            },
                            viewModel = viewModel,
                        )
                    }
                }
            }
        }

        composeRule.runOnUiThread { navController.navigateToComposer() }
        composeRule.waitForIdle()
    }

    /** A real system-Back dispatch, not a call to the ViewModel. */
    private fun pressSystemBack() {
        composeRule.runOnUiThread {
            composeRule.activity.onBackPressedDispatcher.onBackPressed()
        }
        composeRule.waitForIdle()
    }

    /**
     * Types into the post field.
     *
     * Matched by its text-input action UNDER the labelled node rather than by
     * the label itself: the screen sets the description on a wrapper modifier,
     * so the labelled node is the ancestor of the editable one and carries no
     * RequestFocus. Matching the ancestor alone would find a node that cannot
     * be typed into.
     */
    private fun typePost(text: String) {
        composeRule
            // The canvas is a BasicTextField, so ONE node carries both the
            // description and the text action. It used to be a wrapped
            // UsTextField where only a descendant was editable, hence the
            // former ancestor match.
            .onNode(hasSetTextAction() and hasContentDescription("Post text"))
            .performTextInput(text)
        composeRule.waitForIdle()
    }

    // ── C-LB-7.2: system Back goes through the discard decision ─────────

    /**
     * Back with unsaved content ASKS. It does not pop.
     *
     * The defect this pins: only the top-bar arrow was handled, so the gesture
     * and the hardware key popped the destination outright — the draft gone
     * silently, and with it the creation key that stops a retry publishing the
     * same post twice.
     */
    @Test
    fun `system back on a composer with content asks before discarding`() {
        launchComposer()
        typePost("half a thought")

        pressSystemBack()

        composeRule.onNodeWithText("Discard this post?").assertExists()
        assertThat(currentRoute).contains(ComposerRoute::class.qualifiedName)
        composeRule.onNodeWithText(FEED_MARKER).assertDoesNotExist()
    }

    /** Keep editing returns to the draft with the text intact. */
    @Test
    fun `keep editing dismisses the confirmation and keeps the draft`() {
        launchComposer()
        typePost("half a thought")
        pressSystemBack()

        composeRule.onNodeWithText("Keep editing").performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Discard this post?").assertDoesNotExist()
        composeRule.onNodeWithText("half a thought").assertExists()
        assertThat(currentRoute).contains(ComposerRoute::class.qualifiedName)
    }

    /** Confirming discard leaves the composer and returns to the feed. */
    @Test
    fun `confirming discard pops back to the surface the composer opened from`() {
        launchComposer()
        typePost("half a thought")
        pressSystemBack()

        composeRule.onNodeWithText("Discard").performClick()
        composeRule.waitForIdle()

        assertThat(currentRoute).contains(FeedStub::class.qualifiedName)
        composeRule.onNodeWithText(FEED_MARKER).assertExists()
    }

    /**
     * An EMPTY composer just closes.
     *
     * `BackHandler` is enabled only while there is something to lose. Asking
     * "discard this post?" about a post that does not exist trains people to
     * dismiss the dialog without reading it, which is how the confirmation
     * stops protecting anything.
     */
    @Test
    fun `system back on an empty composer closes it without asking`() {
        launchComposer()

        pressSystemBack()

        composeRule.onNodeWithText("Discard this post?").assertDoesNotExist()
        assertThat(currentRoute).contains(FeedStub::class.qualifiedName)
    }

    // ── C-LB-7.3: publish navigates on the server's id and replaces ─────

    /**
     * Success opens the SERVER's post and removes the composer from the stack.
     *
     * Both halves matter. Navigating on a client-invented id would open a post
     * that does not exist; leaving the composer on the stack would put Back
     * from the new post onto a composer whose content is already published,
     * where pressing Post again is an obvious thing to do.
     */
    @Test
    fun `publishing opens the server's post and takes the composer off the stack`() {
        launchComposer()
        typePost("shipping it")

        composeRule.onNodeWithContentDescription("Post").performClick()
        composeRule.waitForIdle()

        assertThat(currentRoute).contains(PostStub::class.qualifiedName)
        assertThat(navController.currentBackStackEntry!!.toRoute<PostStub>().postId)
            .isEqualTo("post-77")
        composeRule.onNodeWithText("post:post-77").assertExists()

        // Back from the new post goes to the feed, NOT to the composer.
        composeRule.runOnUiThread { navController.popBackStack() }
        composeRule.waitForIdle()
        assertThat(currentRoute).contains(FeedStub::class.qualifiedName)
    }

    // ── C-LB-7.4: the composer states WHY it is blocked ─────────────────

    /**
     * An empty composer says what is missing, in the control's own description.
     *
     * "Post, disabled" with no explanation is the most common accessibility
     * failure in a composer: a screen-reader user is told the door is locked
     * and not which key it needs.
     */
    @Test
    fun `an empty composer says why post is unavailable`() {
        launchComposer()

        composeRule
            .onNodeWithContentDescription("Post. Unavailable: add text or a photo first.")
            .assertExists()
            .assertIsNotEnabled()
    }

    /**
     * An attached photo with no accessibility decision blocks the post AND
     * shows the reason as visible text once Post is attempted.
     *
     * The requirement is not merely that the server would refuse it — it is
     * that the person is told, on the screen, what to do about it.
     */
    @Test
    fun `an undescribed photo blocks posting and shows the reason`() {
        launchComposer()
        typePost("look at this")
        pickAPhoto()

        composeRule
            .onNodeWithContentDescription(
                "Post. Unavailable: describe the photo or mark it decorative.",
            )
            .assertExists()
            .assertIsNotEnabled()
        composeRule
            .onNodeWithText("Add a description, or mark the photo as decorative.")
            .assertExists()
        assertThat(repository.keys).isEmpty()
    }

    /**
     * Marking the photo decorative is a complete decision and unblocks Post.
     *
     * The two are mutually exclusive: a described image is not decorative, and
     * a decorative one carries no information to describe.
     */
    @Test
    fun `marking a photo decorative unblocks posting`() {
        launchComposer()
        typePost("look at this")
        pickAPhoto()

        composeRule
            .onNodeWithContentDescription("This photo is decorative")
            .performScrollTo()
            .performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription("Post").assertExists()
        composeRule
            .onNodeWithText("Add a description, or mark the photo as decorative.")
            .assertDoesNotExist()
    }

    /**
     * The audience chip is a CONTROL now: it announces the current choice and
     * that it can be changed. Public is the default, so a fresh composer
     * announces Public.
     */
    @Test
    fun `the audience defaults to public and reads as a control`() {
        launchComposer()

        composeRule
            .onNodeWithContentDescription(
                "Audience: Public. Everyone can see this post. Tap to change.",
            )
            .assertExists()
    }

    /**
     * Attaches a photo without the system picker.
     *
     * `rememberLauncherForActivityResult` cannot deliver a result under
     * Robolectric, so the pick is delivered where the launcher's callback would
     * deliver it. Everything AFTER that point — upload, readiness, the
     * accessibility gate — is the real path.
     */
    private fun pickAPhoto() {
        composeRule.runOnUiThread { viewModelUnderTest.onImagePicked("content://picked/1") }
        composeRule.waitForIdle()
    }

    // ── Fakes ───────────────────────────────────────────────────────────

    private class FakeDraftDao : ComposerDraftDao {
        var stored: ComposerDraftEntity? = null

        override fun observe(id: String): Flow<ComposerDraftEntity?> = flowOf(stored)
        override suspend fun load(id: String): ComposerDraftEntity? = stored
        override suspend fun save(draft: ComposerDraftEntity) {
            stored = draft
        }

        override suspend fun clear(id: String) {
            stored = null
        }
    }

    /** Reports the asset ready and passed on the first poll, so no clock is needed. */
    private class FakeUploadApi : MediaUploadApi {
        override suspend fun init(body: MediaInitRequest): ApiEnvelope<MediaInitDto> =
            ApiEnvelope(MediaInitDto(mediaId = "media-1", uploadUrl = "https://obj/put"))

        override suspend fun confirm(body: MediaConfirmRequest): ApiEnvelope<MediaAssetDto> =
            ApiEnvelope(MediaAssetDto(id = "media-1", processingStatus = "processing"))

        override suspend fun updateAltText(
            mediaId: String,
            body: MediaAltTextRequest,
        ): ApiEnvelope<MediaStatusDto> = ApiEnvelope(MediaStatusDto(mediaId = mediaId))

        override suspend fun status(mediaId: String): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(
                MediaStatusDto(
                    mediaId = mediaId,
                    processingStatus = "ready",
                    moderationStatus = "passed",
                ),
            )

        override suspend fun delete(mediaId: String): ApiEnvelope<MediaStatusDto> =
            ApiEnvelope(MediaStatusDto(mediaId = mediaId))
    }

    private class RecordingRepository : ComposerRepository(
        NeverCalledPostApi(),
        ErrorMapper(Json { ignoreUnknownKeys = true }),
    ) {
        val keys = mutableListOf<String>()
        var result: AppResult<String> = AppResult.Success("post-77")

        override suspend fun createPost(
            creationKey: String,
            request: CreatePostRequest,
        ): AppResult<String> {
            keys += creationKey
            return result
        }
    }

    private class NeverCalledPostApi : PostApi {
        override suspend fun getPost(postId: String): Nothing = error("not used")
        override suspend fun createPost(
            idempotencyKey: String,
            body: CreatePostRequest,
        ): Nothing = error("not used")
    }

    /** A PUT that succeeds without a socket. Transport is covered by MediaUploadWireTest. */
    private class AlwaysSucceedingPut : PresignedUploader(OkHttpClient()) {
        override suspend fun put(
            url: String,
            mimeType: String,
            sizeBytes: Long,
            source: UploadSource,
            onProgress: (Long, Long) -> Unit,
        ): PresignedPutResult {
            onProgress(sizeBytes, sizeBytes)
            return PresignedPutResult.Success
        }
    }

    private companion object {
        const val FEED_MARKER = "feed"
    }
}
