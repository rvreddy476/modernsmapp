// The fixtures at the bottom of this file are request and response bodies
// copied VERBATIM from a live capture. Wrapping them to satisfy the
// line-length rule would mean editing recorded evidence, and a reformatted
// fixture is no longer proof of what went over the wire. The suppression is
// scoped to this file only.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Contract tests for `PUT /v1/profiles/me`, against the 2026-08-17 repair
 * recapture (prompt/android-api-contracts.md §5).
 *
 * These assert the exact BYTES this client puts on the wire, not merely that
 * the call succeeded. Two captured facts make that necessary:
 *
 *  - The endpoint is a full replacement. An omitted key is not "unchanged", it
 *    is "cleared" — `{}` returned `200` and wiped `display_name`, `category`,
 *    `profession` and `location`.
 *  - The app-wide `Json` has `encodeDefaults = false`, so a request property
 *    equal to its declared Kotlin default is silently dropped from the body.
 *
 * Together those turn a one-word DTO change (`val bio: String = ""`) into
 * silent data loss that every outcome-level assertion would still pass. Only
 * an exact-body assertion catches it, which is why the tests below compare
 * against a recorded request string rather than a re-derived one.
 */
class EditProfileContractTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: ProfileRepository

    /**
     * Mirrors `:core:network` `NetworkModule.provideJson` exactly, INCLUDING
     * the absent `encodeDefaults`.
     *
     * That omission is the point. Setting `encodeDefaults = true` here would
     * make these tests pass against a DTO that drops fields in production —
     * the test would be measuring a configuration the app does not ship.
     */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            // A bare client, with none of the app's interceptors. The headers
            // asserted below are therefore only the ones Retrofit and this
            // feature produce, which is exactly the question being asked.
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ProfileApi::class.java)
        repository = ProfileRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .setHeader("Content-Type", "application/json")
                .body(body)
                .build(),
        )
    }

    /**
     * The assertion this whole feature exists to protect.
     *
     * [CAPTURED_REQUEST] is the body a live `PUT` was observed to accept. If
     * the serialized bytes still equal it, every editable field is present and
     * the property order matches the recording.
     */
    @Test
    fun `the PUT body is byte-for-byte the captured full snapshot`() = runTest {
        enqueue(200, SAVED_PROFILE)

        repository.updateProfile(CAPTURED_SNAPSHOT)

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PUT")
        assertThat(request.target).isEqualTo("/v1/profiles/me")
        assertThat(request.body?.utf8()).isEqualTo(CAPTURED_REQUEST)
    }

    /**
     * The `encodeDefaults` regression guard.
     *
     * Every field is the empty string here — precisely the value a well-meaning
     * `= ""` default would take. With no declared defaults on the DTO, kotlinx
     * has nothing to consider default and emits all seven keys. If someone adds
     * defaults, this body collapses towards `{}` and the test fails loudly
     * instead of the user's profile being erased quietly.
     */
    @Test
    fun `an all-blank snapshot still serializes all seven keys`() = runTest {
        enqueue(200, SAVED_PROFILE)

        repository.updateProfile(
            EditableProfile(
                displayName = "",
                bio = "",
                category = "",
                profession = "",
                website = "",
                location = "",
                profileThemeColor = "",
            ),
        )

        assertThat(server.takeRequest().body?.utf8()).isEqualTo(
            """{"profile_theme_color":"","website":"","profession":"","display_name":"","location":"","category":"","bio":""}""",
        )
    }

    /**
     * The behaviour a partial-update design would break: the user edits one
     * field, and the six they never touched are still transmitted holding the
     * values `/me` returned.
     */
    @Test
    fun `a field the user never touched is still sent with its loaded value`() = runTest {
        enqueue(200, SAVED_PROFILE)
        enqueue(200, SAVED_PROFILE)
        val loaded = EditableProfile.from((repository.getOwnProfile() as AppResult.Success).data)
        server.takeRequest()

        // Exactly one field edited, through the same path the form uses.
        repository.updateProfile(loaded.with(EditProfileField.LOCATION, "Hyderabad"))

        val body = server.takeRequest().body?.utf8()
        assertThat(body).isEqualTo(
            """{"profile_theme_color":"#1A73E8","website":"","profession":"android-contract","display_name":"Android Repair","location":"Hyderabad","category":"personal","bio":"Native bearer contract verified"}""",
        )
    }

    /**
     * Native clients do not carry CSRF. The 2026-08-17 repair made a validated
     * bearer token sufficient for writes, and the required headers are only
     * `Authorization` and `Content-Type`.
     *
     * Asserting the absence is worth a test because the previous design note on
     * `ProfileApi` claimed this endpoint needed an `X-CSRF-Token`, and reviving
     * that belief would mean a feature-level interceptor or a hand-set header
     * reappearing here.
     */
    @Test
    fun `no CSRF header and no cookie are sent with the write`() = runTest {
        enqueue(200, SAVED_PROFILE)

        repository.updateProfile(CAPTURED_SNAPSHOT)

        val request = server.takeRequest()
        assertThat(request.headers["X-CSRF-Token"]).isNull()
        assertThat(request.headers["Cookie"]).isNull()
        assertThat(request.headers["Content-Type"]).contains("application/json")
    }

    /** The saved owner projection comes back and maps into the domain model. */
    @Test
    fun `the response is the saved owner projection`() = runTest {
        enqueue(200, SAVED_PROFILE)

        val result = repository.updateProfile(CAPTURED_SNAPSHOT)

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val profile = (result as AppResult.Success).data
        assertThat(profile.displayName).isEqualTo("Android Repair")
        assertThat(profile.profession).isEqualTo("android-contract")
        assertThat(profile.profileThemeColor).isEqualTo("#1A73E8")
        // Private fields are present, so this really is the `/me` shape and
        // the form can be re-seeded from it.
        assertThat(profile.isOwnProfile).isTrue()
        assertThat(requireNotNull(profile.personal).firstName).isEqualTo("Android")
    }

    /**
     * A rejected save must surface as a failure so the form keeps what the user
     * typed. Reporting success on a `CSRF_FAILED` would leave them believing a
     * save happened that did not.
     */
    @Test
    fun `a CSRF rejection surfaces as a failure`() = runTest {
        enqueue(403, """{"error":{"code":"CSRF_FAILED","message":"Missing or invalid CSRF token"}}""")

        val result = repository.updateProfile(CAPTURED_SNAPSHOT)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `an expired token surfaces as a failure`() = runTest {
        enqueue(401, """{"error":{"code":"UNAUTHORIZED","message":"Missing access token"}}""")

        val result = repository.updateProfile(CAPTURED_SNAPSHOT)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    private companion object {
        /**
         * Verbatim from a live accepted `PUT /v1/profiles/me` request body.
         * Field order included — kotlinx serializes in declaration order, so
         * `UpdateProfileRequest` is declared to reproduce this exactly.
         */
        const val CAPTURED_REQUEST =
            """{"profile_theme_color":"#1A73E8","website":"","profession":"contract-test","display_name":"Android Contract","location":"India","category":"personal","bio":"Live Android API contract capture"}"""

        /** The same values, as the snapshot the form would hold. */
        val CAPTURED_SNAPSHOT = EditableProfile(
            displayName = "Android Contract",
            bio = "Live Android API contract capture",
            category = "personal",
            profession = "contract-test",
            website = "",
            location = "India",
            profileThemeColor = "#1A73E8",
        )

        // Verbatim from the 2026-08-17 repair viewer's successful response.
        const val SAVED_PROFILE =
            """{"data":{"user_id":"719e2958-f412-44ca-b94a-b00060a7fccb","display_name":"Android Repair","first_name":"Android","last_name":"Repair","bio":"Native bearer contract verified","dob":"1990-01-01T00:00:00Z","gender":"other","category":"personal","profession":"android-contract","website":"","location":"","badge_flags":0,"is_verified":false,"verification_level":"","status_text":"","status_emoji":"","profile_theme_color":"#1A73E8","intro_media_url":"","intro_media_type":"","cta_label":"","cta_url":"","member_since_badge":false,"timezone":"","follower_count":0,"following_count":0,"friend_count":0,"post_count":0,"created_at":"2026-08-16T19:29:00.322767Z","updated_at":"2026-08-16T20:15:16.197106Z"}}"""
    }
}
